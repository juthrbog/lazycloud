package aws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLambda answers Lambda calls in the REST JSON protocol. Each
// operation has a reply; the fake records the path and query of each call.
type fakeLambda struct {
	mu      sync.Mutex
	replies map[string]func(q url.Values) (int, string)
	calls   []fakeLambdaCall
}

type fakeLambdaCall struct {
	op   string
	path string
	q    url.Values
}

func newFakeLambda(t *testing.T) (*fakeLambda, *LambdaServiceImpl) {
	t.Helper()
	f := &fakeLambda{replies: map[string]func(url.Values) (int, string){}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, NewLambdaService(testClient(t, srv.URL))
}

func (f *fakeLambda) reply(op, body string) {
	f.replies[op] = func(url.Values) (int, string) { return http.StatusOK, body }
}

// lambdaOp names the operation of a request from its path.
func lambdaOp(path string) string {
	switch {
	case strings.HasPrefix(path, "/2015-03-31/event-source-mappings"):
		return "ListEventSourceMappings"
	case strings.HasPrefix(path, "/2021-10-31/functions/") && strings.HasSuffix(path, "/url"):
		return "GetFunctionUrlConfig"
	case strings.HasSuffix(path, "/versions"):
		return "ListVersionsByFunction"
	case strings.HasSuffix(path, "/aliases"):
		return "ListAliases"
	case strings.TrimSuffix(path, "/") == "/2015-03-31/functions":
		return "ListFunctions"
	case strings.HasPrefix(path, "/2015-03-31/functions/"):
		return "GetFunction"
	}
	return "unknown"
}

func (f *fakeLambda) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := lambdaOp(r.URL.Path)
	f.calls = append(f.calls, fakeLambdaCall{op: op, path: r.URL.Path, q: r.URL.Query()})
	fn, ok := f.replies[op]
	if !ok {
		http.Error(w, "unexpected operation "+op, http.StatusNotImplemented)
		return
	}
	status, body := fn(r.URL.Query())
	w.Header().Set("Content-Type", "application/json")
	if status != http.StatusOK {
		if _, rest, ok := strings.Cut(body, `"Type":"`); ok {
			typ, _, _ := strings.Cut(rest, `"`)
			w.Header().Set("X-Amzn-Errortype", typ)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

const listFunctionsBody = `{"Functions":[{
	"FunctionName":"orders-api","FunctionArn":"arn:aws:lambda:us-east-1:1:function:orders-api",
	"Runtime":"python3.12","Handler":"index.handler","MemorySize":256,"Timeout":30,
	"CodeSize":266,"Version":"$LATEST","Description":"orders","PackageType":"Zip",
	"Architectures":["x86_64"],"LastModified":"2026-11-21T01:02:16.333+0000",
	"Environment":{"Variables":{"DB_PASSWORD":"hunter2"}},
	"LoggingConfig":{"LogGroup":"/aws/lambda/orders-api","LogFormat":"Text"}
},{
	"FunctionName":"thumbnailer","FunctionArn":"arn:aws:lambda:us-east-1:1:function:thumbnailer",
	"PackageType":"Image","CodeSize":0,"LastModified":"2026-11-20T10:00:00Z",
	"LoggingConfig":{"LogFormat":"JSON","SystemLogLevel":"WARN","ApplicationLogLevel":"ERROR"}
}],"NextMarker":"m2"}`

// ListFunctions sends the marker and a page size of 50, and maps the
// fields of each function. The environment values of the list result are
// not kept (H2).
func TestLambdaListFunctions(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("ListFunctions", listFunctionsBody)
	p, err := svc.ListFunctionsPage(context.Background(), new("m1"))
	require.NoError(t, err)
	require.Len(t, f.calls, 1)
	assert.Equal(t, "m1", f.calls[0].q.Get("Marker"))
	assert.Equal(t, "50", f.calls[0].q.Get("MaxItems"))
	require.NotNil(t, p.Next)
	assert.Equal(t, "m2", *p.Next)
	require.Len(t, p.Functions, 2)

	fn := p.Functions[0]
	assert.Equal(t, Function{
		Name: "orders-api", ARN: "arn:aws:lambda:us-east-1:1:function:orders-api",
		Version: "$LATEST", Description: "orders", Runtime: "python3.12", Handler: "index.handler",
		PackageType: "Zip", Architectures: []string{"x86_64"}, MemoryMB: 256, TimeoutSec: 30,
		CodeSize: 266, LastModified: time.Date(2026, 11, 21, 1, 2, 16, 333e6, time.UTC),
		LogGroup: "/aws/lambda/orders-api", LogFormat: "Text",
	}, fn.withUTC())
	assert.Equal(t, "/aws/lambda/orders-api", fn.LogGroupName())
	assert.Equal(t, "/custom/g", Function{Name: "fn", LogGroup: "/custom/g"}.LogGroupName(), "a custom group wins")

	img := p.Functions[1]
	assert.Empty(t, img.Runtime)
	assert.Equal(t, "Image", img.PackageType)
	assert.Equal(t, "JSON", img.LogFormat)
	assert.Equal(t, "WARN", img.SystemLogLevel)
	assert.Equal(t, "ERROR", img.AppLogLevel)
	assert.True(t, img.LastModified.Equal(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)), "RFC 3339 also reads")
	assert.Equal(t, "/aws/lambda/thumbnailer", img.LogGroupName(), "the default group when AWS leaves it out")

	for f := range reflect.TypeFor[Function]().Fields() {
		assert.NotContains(t, strings.ToLower(f.Name), "env", "Function keeps no environment values")
	}
}

// withUTC returns f with LastModified in UTC, for a compare.
func (f Function) withUTC() Function {
	f.LastModified = f.LastModified.UTC()
	return f
}

// An empty NextMarker ends the list (H11). LocalStack sends "" on the last
// page of event source mappings.
func TestLambdaEmptyMarkerEndsTheList(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("ListFunctions", `{"Functions":[],"NextMarker":""}`)
	f.reply("ListVersionsByFunction", `{"Versions":[],"NextMarker":""}`)
	f.reply("ListAliases", `{"Aliases":[],"NextMarker":""}`)
	f.reply("ListEventSourceMappings", `{"EventSourceMappings":[],"NextMarker":""}`)
	ctx := context.Background()
	p, err := svc.ListFunctionsPage(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, p.Next)
	v, err := svc.ListVersionsPage(ctx, "fn", nil)
	require.NoError(t, err)
	assert.Nil(t, v.Next)
	a, err := svc.ListAliasesPage(ctx, "fn", nil)
	require.NoError(t, err)
	assert.Nil(t, a.Next)
	tr, err := svc.ListTriggersPage(ctx, "fn", nil)
	require.NoError(t, err)
	assert.Nil(t, tr.Next)
}

// GetFunction maps the state, the configuration, the environment, the VPC,
// the layers, the concurrency and the tags. It keeps no code URL.
func TestLambdaGetFunction(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("GetFunction", `{"Configuration":{
		"FunctionName":"orders-api","FunctionArn":"arn:aws:lambda:us-east-1:1:function:orders-api:live",
		"Version":"1","Runtime":"python3.12","State":"Active","StateReason":"ok","StateReasonCode":"Idle",
		"LastUpdateStatus":"InProgress","LastUpdateStatusReason":"updating",
		"Role":"arn:aws:iam::1:role/r","CodeSha256":"abc","KMSKeyArn":"arn:kms",
		"DeadLetterConfig":{"TargetArn":"arn:sqs:dlq"},"TracingConfig":{"Mode":"Active"},
		"EphemeralStorage":{"Size":1024},
		"Environment":{"Variables":{"DB_PASSWORD":"hunter2"},"Error":{"ErrorCode":"KMSAccessDenied","Message":"no key"}},
		"VpcConfig":{"VpcId":"vpc-1","SubnetIds":["subnet-1"],"SecurityGroupIds":["sg-1"]},
		"Layers":[{"Arn":"arn:aws:lambda:us-east-1:1:layer:deps:3","CodeSize":100}],
		"SnapStart":{"ApplyOn":"None","OptimizationStatus":"Off"}},
		"Code":{"Location":"https://example.com/presigned","RepositoryType":"S3"},
		"Concurrency":{"ReservedConcurrentExecutions":5},
		"TagsError":{"ErrorCode":"AccessDeniedException","Message":"no lambda:ListTags"}}`)
	d, err := svc.GetFunction(context.Background(), "orders-api", "live")
	require.NoError(t, err)
	require.Len(t, f.calls, 1)
	assert.Equal(t, "live", f.calls[0].q.Get("Qualifier"))

	assert.Equal(t, "orders-api", d.Name)
	assert.Equal(t, "1", d.Version)
	assert.Equal(t, "Active", d.State)
	assert.Equal(t, "ok", d.StateReason)
	assert.Equal(t, "Idle", d.StateReasonCode)
	assert.Equal(t, "InProgress", d.LastUpdateStatus)
	assert.Equal(t, "updating", d.LastUpdateReason)
	assert.Equal(t, "arn:aws:iam::1:role/r", d.Role)
	assert.Equal(t, "abc", d.CodeSHA256)
	assert.Equal(t, "arn:kms", d.KMSKeyARN)
	assert.Equal(t, "arn:sqs:dlq", d.DeadLetterARN)
	assert.Equal(t, "Active", d.TracingMode)
	assert.Equal(t, int32(1024), d.EphemeralMB)
	assert.Equal(t, map[string]string{"DB_PASSWORD": "hunter2"}, d.Env)
	assert.Equal(t, "KMSAccessDenied no key", d.EnvError)
	assert.Equal(t, "vpc-1", d.VPCID)
	assert.Equal(t, []string{"subnet-1"}, d.SubnetIDs)
	assert.Equal(t, []string{"sg-1"}, d.SecurityGroupIDs)
	require.Len(t, d.Layers, 1)
	assert.Equal(t, "deps", d.Layers[0].Name())
	assert.Equal(t, "3", d.Layers[0].Version())
	assert.Equal(t, "None", d.SnapStartApplyOn)
	assert.Equal(t, "Off", d.SnapStartStatus)
	require.NotNil(t, d.ReservedConcurrency)
	assert.Equal(t, int32(5), *d.ReservedConcurrency)
	assert.Empty(t, d.Tags)
	assert.Equal(t, "AccessDeniedException: no lambda:ListTags", d.TagsError)

	for f := range reflect.TypeFor[FunctionDetail]().Fields() {
		assert.NotEqual(t, "CodeLocation", f.Name)
	}
}

// A function outside a VPC sends empty fields, not nil. A function with
// no reserved concurrency has none.
func TestLambdaGetFunctionWithoutVPC(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("GetFunction", `{"Configuration":{"FunctionName":"fn",
		"VpcConfig":{"VpcId":"","SubnetIds":[],"SecurityGroupIds":[]}},
		"Tags":{"team":"orders"}}`)
	d, err := svc.GetFunction(context.Background(), "fn", "")
	require.NoError(t, err)
	assert.False(t, f.calls[0].q.Has("Qualifier"))
	assert.Empty(t, d.VPCID)
	assert.Nil(t, d.ReservedConcurrency)
	assert.Equal(t, map[string]string{"team": "orders"}, d.Tags)
	assert.Empty(t, d.TagsError)
}

// A missing function is a ResourceNotFoundException with the call name.
func TestLambdaGetFunctionNotFound(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.replies["GetFunction"] = func(url.Values) (int, string) {
		return http.StatusNotFound, `{"Type":"ResourceNotFoundException","Message":"Function not found"}`
	}
	_, err := svc.GetFunction(context.Background(), "nope", "")
	require.Error(t, err)
	_, ok := errors.AsType[*lambdatypes.ResourceNotFoundException](err)
	assert.True(t, ok, "%v", err)
	assert.Contains(t, ErrorText(err), "Lambda GetFunction")
}

// GetFunctionURL maps a URL, gives nil for a function without one, and
// returns any other error.
func TestLambdaGetFunctionURL(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("GetFunctionUrlConfig", `{"FunctionUrl":"https://abc.lambda-url.us-east-1.on.aws/",
		"AuthType":"AWS_IAM","InvokeMode":"RESPONSE_STREAM",
		"CreationTime":"2026-11-21T01:02:16.333+0000","LastModifiedTime":"2026-11-21T02:00:00.000+0000"}`)
	u, err := svc.GetFunctionURL(context.Background(), "fn")
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "https://abc.lambda-url.us-east-1.on.aws/", u.URL)
	assert.Equal(t, "AWS_IAM", u.AuthType)
	assert.Equal(t, "RESPONSE_STREAM", u.InvokeMode)
	assert.True(t, u.Created.Equal(time.Date(2026, 11, 21, 1, 2, 16, 333e6, time.UTC)))
	assert.True(t, u.Modified.Equal(time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC)))

	f.replies["GetFunctionUrlConfig"] = func(url.Values) (int, string) {
		return http.StatusNotFound, `{"Type":"ResourceNotFoundException","Message":"The resource you requested does not exist."}`
	}
	u, err = svc.GetFunctionURL(context.Background(), "fn")
	require.NoError(t, err)
	assert.Nil(t, u)

	f.replies["GetFunctionUrlConfig"] = func(url.Values) (int, string) {
		return http.StatusTooManyRequests, `{"Type":"TooManyRequestsException","Message":"Rate exceeded"}`
	}
	_, err = svc.GetFunctionURL(context.Background(), "fn")
	require.Error(t, err)
	assert.Contains(t, ErrorText(err), "AWS limits the request rate")
}

func TestLambdaListAliases(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("ListAliases", `{"Aliases":[
		{"Name":"live","AliasArn":"arn:a:live","FunctionVersion":"1","Description":"prod",
		 "RoutingConfig":{"AdditionalVersionWeights":{"2":0.1}}},
		{"Name":"dev","AliasArn":"arn:a:dev","FunctionVersion":"$LATEST"}],"NextMarker":"arn:a:next"}`)
	p, err := svc.ListAliasesPage(context.Background(), "orders-api", new("t1"))
	require.NoError(t, err)
	assert.Equal(t, "/2015-03-31/functions/orders-api/aliases", f.calls[0].path)
	assert.Equal(t, "t1", f.calls[0].q.Get("Marker"))
	assert.Equal(t, []Alias{
		{Name: "live", ARN: "arn:a:live", Version: "1", Description: "prod", Weights: map[string]float64{"2": 0.1}},
		{Name: "dev", ARN: "arn:a:dev", Version: "$LATEST"},
	}, p.Aliases)
	assert.Equal(t, "arn:a:next", *p.Next)
}

func TestLambdaListVersions(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("ListVersionsByFunction", `{"Versions":[
		{"FunctionName":"orders-api","Version":"$LATEST","Runtime":"python3.12"},
		{"FunctionName":"orders-api","Version":"1","Description":"first","LastModified":"2026-11-20T10:00:00.000+0000"}]}`)
	p, err := svc.ListVersionsPage(context.Background(), "orders-api", nil)
	require.NoError(t, err)
	assert.Equal(t, "/2015-03-31/functions/orders-api/versions", f.calls[0].path)
	assert.Equal(t, "50", f.calls[0].q.Get("MaxItems"))
	require.Len(t, p.Functions, 2)
	assert.Equal(t, "$LATEST", p.Functions[0].Version)
	assert.Equal(t, "first", p.Functions[1].Description)
	assert.False(t, p.Functions[1].LastModified.IsZero())
	assert.Nil(t, p.Next)
}

// The mappings of a function: the fields, the time (epoch seconds on the
// wire), the filters, and the source type from the ARN.
func TestLambdaListTriggers(t *testing.T) {
	f, svc := newFakeLambda(t)
	f.reply("ListEventSourceMappings", `{"EventSourceMappings":[{
		"UUID":"u1","EventSourceArn":"arn:aws:sqs:us-east-1:1:orders","FunctionArn":"arn:aws:lambda:us-east-1:1:function:orders-api",
		"State":"Enabled","StateTransitionReason":"USER_INITIATED","LastProcessingResult":"OK",
		"BatchSize":10,"MaximumBatchingWindowInSeconds":5,"LastModified":1795222978.492,
		"FilterCriteria":{"Filters":[{"Pattern":"{\"body\":{\"type\":[\"order\"]}}"}]}}],"NextMarker":"u2"}`)
	p, err := svc.ListTriggersPage(context.Background(), "orders-api", nil)
	require.NoError(t, err)
	assert.Equal(t, "orders-api", f.calls[0].q.Get("FunctionName"))
	assert.Equal(t, "100", f.calls[0].q.Get("MaxItems"))
	require.Len(t, p.Triggers, 1)
	tr := p.Triggers[0]
	assert.Equal(t, "u1", tr.UUID)
	assert.Equal(t, "Enabled", tr.State)
	assert.Equal(t, "USER_INITIATED", tr.StateReason)
	assert.Equal(t, "OK", tr.LastResult)
	assert.Equal(t, int32(10), tr.BatchSize)
	assert.Equal(t, int32(5), tr.BatchWindowSec)
	assert.Equal(t, int64(1795222978), tr.LastModified.Unix())
	assert.Equal(t, []string{`{"body":{"type":["order"]}}`}, tr.Filters)
	assert.Equal(t, "SQS", tr.SourceType())
	assert.Equal(t, "u2", *p.Next)
}

func TestTriggerSourceType(t *testing.T) {
	for arn, want := range map[string]string{
		"arn:aws:sqs:us-east-1:1:q":                        "SQS",
		"arn:aws:kinesis:us-east-1:1:stream/s":             "Kinesis",
		"arn:aws:dynamodb:us-east-1:1:table/t/stream/2026": "DynamoDB",
		"arn:aws:kafka:us-east-1:1:cluster/c/1":            "Kafka",
		"arn:aws:mq:us-east-1:1:broker:b:1":                "MQ",
		"arn:aws:rds:us-east-1:1:cluster:docs":             "DocumentDB",
		"":                                                 "Kafka",
		"garbage":                                          "unknown",
		"arn:aws-cn:sqs:cn-north-1:1:q":                    "SQS",
		"arn:aws:somethingnew:us-east-1:1:x":               "somethingnew",
	} {
		assert.Equal(t, want, Trigger{SourceARN: arn}.SourceType(), arn)
	}
}

// H3: the time form of Lambda has no colon in the zone, so RFC 3339 does
// not read it. parseLambdaTime reads both forms.
func TestParseLambdaTime(t *testing.T) {
	_, err := time.Parse(time.RFC3339, "2019-09-26T20:28:40.438+0000")
	require.Error(t, err, "H3: RFC 3339 does not read the form of Lambda")
	want := time.Date(2019, 9, 26, 20, 28, 40, 438e6, time.UTC)
	assert.True(t, parseLambdaTime("2019-09-26T20:28:40.438+0000").Equal(want))
	assert.True(t, parseLambdaTime("2019-09-26T22:28:40.438+0200").Equal(want))
	assert.True(t, parseLambdaTime("2019-09-26T20:28:40+0000").Equal(want.Truncate(time.Second)))
	assert.True(t, parseLambdaTime("2019-09-26T20:28:40.438Z").Equal(want))
	assert.True(t, parseLambdaTime("").IsZero())
	assert.True(t, parseLambdaTime("yesterday").IsZero())
}

func TestLayerNameAndVersion(t *testing.T) {
	l := Layer{ARN: "arn:aws:lambda:us-east-1:123456789012:layer:my-deps:12"}
	assert.Equal(t, "my-deps", l.Name())
	assert.Equal(t, "12", l.Version())
	fn := Layer{ARN: "arn:aws:lambda:us-east-1:123456789012:function:my-fn:12"}
	assert.Equal(t, fn.ARN, fn.Name(), "not a layer ARN")
	assert.Empty(t, fn.Version())
	odd := Layer{ARN: "not-an-arn"}
	assert.Equal(t, "not-an-arn", odd.Name())
	assert.Empty(t, odd.Version())
}
