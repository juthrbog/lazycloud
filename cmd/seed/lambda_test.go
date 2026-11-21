package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLambdaAPI keeps functions in memory, as LocalStack does: a new
// function is Pending for two reads, an update is InProgress for two
// reads, and PublishVersion with no change returns the last version. A
// call that changes a function that is not ready fails.
type fakeLambdaAPI struct {
	fns      map[string]*fakeFn
	aliases  map[string]*lambda.CreateAliasInput // "fn/alias"
	mappings []lambdatypes.EventSourceMappingConfiguration
	creates  int
	updates  int
	publish  int
}

type fakeFn struct {
	in          *lambda.CreateFunctionInput
	desc        string
	pending     int // reads left in Pending or InProgress
	versions    []string
	publishDesc string // the description of the last version
}

func newFakeLambdaAPI() *fakeLambdaAPI {
	return &fakeLambdaAPI{fns: map[string]*fakeFn{}, aliases: map[string]*lambda.CreateAliasInput{}}
}

func notFound() error {
	return &lambdatypes.ResourceNotFoundException{Message: aws.String("not found")}
}

func (f *fakeLambdaAPI) CreateFunction(_ context.Context, in *lambda.CreateFunctionInput, _ ...func(*lambda.Options)) (*lambda.CreateFunctionOutput, error) {
	f.creates++
	f.fns[aws.ToString(in.FunctionName)] = &fakeFn{in: in, desc: aws.ToString(in.Description), pending: 2}
	return &lambda.CreateFunctionOutput{}, nil
}

func (f *fakeLambdaAPI) GetFunction(_ context.Context, in *lambda.GetFunctionInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error) {
	fn, ok := f.fns[aws.ToString(in.FunctionName)]
	if !ok {
		return nil, notFound()
	}
	c := &lambdatypes.FunctionConfiguration{State: lambdatypes.StateActive, LastUpdateStatus: lambdatypes.LastUpdateStatusSuccessful}
	if fn.pending > 0 {
		fn.pending--
		c.State, c.LastUpdateStatus = lambdatypes.StatePending, lambdatypes.LastUpdateStatusInProgress
	}
	return &lambda.GetFunctionOutput{Configuration: c}, nil
}

func (f *fakeLambdaAPI) UpdateFunctionConfiguration(_ context.Context, in *lambda.UpdateFunctionConfigurationInput, _ ...func(*lambda.Options)) (*lambda.UpdateFunctionConfigurationOutput, error) {
	f.updates++
	fn := f.fns[aws.ToString(in.FunctionName)]
	if fn.pending > 0 {
		return nil, &lambdatypes.ResourceConflictException{Message: aws.String("An update is in progress")}
	}
	fn.desc, fn.pending = aws.ToString(in.Description), 2
	return &lambda.UpdateFunctionConfigurationOutput{}, nil
}

func (f *fakeLambdaAPI) ListVersionsByFunction(_ context.Context, in *lambda.ListVersionsByFunctionInput, _ ...func(*lambda.Options)) (*lambda.ListVersionsByFunctionOutput, error) {
	fn := f.fns[aws.ToString(in.FunctionName)]
	out := &lambda.ListVersionsByFunctionOutput{Versions: []lambdatypes.FunctionConfiguration{{Version: aws.String("$LATEST")}}}
	for _, v := range fn.versions {
		out.Versions = append(out.Versions, lambdatypes.FunctionConfiguration{Version: aws.String(v)})
	}
	return out, nil
}

func (f *fakeLambdaAPI) PublishVersion(_ context.Context, in *lambda.PublishVersionInput, _ ...func(*lambda.Options)) (*lambda.PublishVersionOutput, error) {
	f.publish++
	fn := f.fns[aws.ToString(in.FunctionName)]
	if fn.pending > 0 {
		return nil, &lambdatypes.ResourceConflictException{Message: aws.String("not ready")}
	}
	if len(fn.versions) > 0 && fn.publishDesc == fn.desc {
		return &lambda.PublishVersionOutput{Version: aws.String(fn.versions[len(fn.versions)-1])}, nil
	}
	v := strconv.Itoa(len(fn.versions) + 1)
	fn.versions, fn.publishDesc = append(fn.versions, v), fn.desc
	return &lambda.PublishVersionOutput{Version: aws.String(v)}, nil
}

func (f *fakeLambdaAPI) GetAlias(_ context.Context, in *lambda.GetAliasInput, _ ...func(*lambda.Options)) (*lambda.GetAliasOutput, error) {
	if _, ok := f.aliases[aws.ToString(in.FunctionName)+"/"+aws.ToString(in.Name)]; !ok {
		return nil, notFound()
	}
	return &lambda.GetAliasOutput{}, nil
}

func (f *fakeLambdaAPI) CreateAlias(_ context.Context, in *lambda.CreateAliasInput, _ ...func(*lambda.Options)) (*lambda.CreateAliasOutput, error) {
	f.aliases[aws.ToString(in.FunctionName)+"/"+aws.ToString(in.Name)] = in
	return &lambda.CreateAliasOutput{}, nil
}

func (f *fakeLambdaAPI) ListEventSourceMappings(_ context.Context, in *lambda.ListEventSourceMappingsInput, _ ...func(*lambda.Options)) (*lambda.ListEventSourceMappingsOutput, error) {
	out := &lambda.ListEventSourceMappingsOutput{}
	for _, m := range f.mappings {
		if aws.ToString(m.FunctionArn) == aws.ToString(in.FunctionName) && aws.ToString(m.EventSourceArn) == aws.ToString(in.EventSourceArn) {
			out.EventSourceMappings = append(out.EventSourceMappings, m)
		}
	}
	return out, nil
}

func (f *fakeLambdaAPI) CreateEventSourceMapping(_ context.Context, in *lambda.CreateEventSourceMappingInput, _ ...func(*lambda.Options)) (*lambda.CreateEventSourceMappingOutput, error) {
	f.mappings = append(f.mappings, lambdatypes.EventSourceMappingConfiguration{
		FunctionArn: in.FunctionName, EventSourceArn: in.EventSourceArn, State: aws.String("Creating"), BatchSize: in.BatchSize,
	})
	return &lambda.CreateEventSourceMappingOutput{}, nil
}

type fakeQueueAPI struct{ created []string }

func (q *fakeQueueAPI) CreateQueue(_ context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
	q.created = append(q.created, aws.ToString(in.QueueName))
	return &sqs.CreateQueueOutput{QueueUrl: aws.String("http://localhost/q/" + aws.ToString(in.QueueName))}, nil
}

func (q *fakeQueueAPI) GetQueueAttributes(_ context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	name := strings.TrimPrefix(aws.ToString(in.QueueUrl), "http://localhost/q/")
	return &sqs.GetQueueAttributesOutput{Attributes: map[string]string{"QueueArn": "arn:aws:sqs:us-east-1:000000000000:" + name}}, nil
}

func fastLambdaWait(t *testing.T) {
	t.Helper()
	w, s := lambdaWait, lambdaWaitStep
	lambdaWait, lambdaWaitStep = time.Second, time.Millisecond
	t.Cleanup(func() { lambdaWait, lambdaWaitStep = w, s })
}

// The seeded config makes every function with its parts, and a second run
// adds nothing.
func TestSeedLambdaRunsTwiceWithoutDoubling(t *testing.T) {
	fastLambdaWait(t)
	cfg, err := loadConfig("small")
	require.NoError(t, err)
	api, queues := newFakeLambdaAPI(), &fakeQueueAPI{}
	require.NoError(t, seedLambda(context.Background(), api, queues, cfg.Lambda))

	require.Contains(t, api.fns, "orders-api")
	orders := api.fns["orders-api"]
	assert.Equal(t, []string{"1", "2"}, orders.versions, "two versions, with a change between them")
	assert.Equal(t, lambdatypes.ArchitectureX8664, orders.in.Architectures[0], "x86_64, as the CI runners")
	assert.Equal(t, "not-a-real-secret", orders.in.Environment.Variables["DB_PASSWORD"])
	live := api.aliases["orders-api/live"]
	require.NotNil(t, live)
	assert.Equal(t, "1", aws.ToString(live.FunctionVersion))
	assert.Equal(t, map[string]float64{"2": 0.1}, live.RoutingConfig.AdditionalVersionWeights)
	require.Len(t, api.mappings, 1)
	assert.Equal(t, "arn:aws:sqs:us-east-1:000000000000:orders-api-jobs", aws.ToString(api.mappings[0].EventSourceArn))

	thumbs := api.fns["thumbnailer"].in.LoggingConfig
	assert.Equal(t, lambdatypes.LogFormatJson, thumbs.LogFormat)
	assert.Equal(t, lambdatypes.SystemLogLevelWarn, thumbs.SystemLogLevel)
	assert.Equal(t, "/custom/report-builder", aws.ToString(api.fns["report-builder"].in.LoggingConfig.LogGroup))
	img := api.fns["legacy-image"].in
	assert.Equal(t, lambdatypes.PackageTypeImage, img.PackageType)
	assert.Empty(t, img.Runtime)

	creates, updates, publish := api.creates, api.updates, api.publish
	require.NoError(t, seedLambda(context.Background(), api, queues, cfg.Lambda))
	assert.Equal(t, creates, api.creates, "no function twice")
	assert.Equal(t, updates, api.updates, "no update on the second run")
	assert.Equal(t, publish, api.publish, "no publish on the second run")
	assert.Len(t, api.mappings, 1, "no trigger twice")
}

// A mapping that LocalStack keeps as a ghost (Deleting) does not count.
func TestSeedLambdaIgnoresAGhostMapping(t *testing.T) {
	fastLambdaWait(t)
	api, queues := newFakeLambdaAPI(), &fakeQueueAPI{}
	api.mappings = []lambdatypes.EventSourceMappingConfiguration{{FunctionArn: aws.String("fn"),
		EventSourceArn: aws.String("arn:aws:sqs:us-east-1:000000000000:q"), State: aws.String("Deleting")}}
	cfg := LambdaSeedConfig{Functions: []functionDef{{Name: "fn", Runtime: "python3.12", SQSTrigger: "q"}}}
	require.NoError(t, seedLambda(context.Background(), api, queues, cfg))
	assert.Len(t, api.mappings, 2)
}

// A run after a failed run waits for a function that exists but is not
// ready yet, before it publishes.
func TestSeedLambdaWaitsForAFunctionThatExists(t *testing.T) {
	fastLambdaWait(t)
	api := newFakeLambdaAPI()
	api.fns["fn"] = &fakeFn{in: &lambda.CreateFunctionInput{}, pending: 2}
	cfg := LambdaSeedConfig{Functions: []functionDef{{Name: "fn", Runtime: "python3.12", Versions: 1}}}
	require.NoError(t, seedLambda(context.Background(), api, &fakeQueueAPI{}, cfg))
	assert.Equal(t, []string{"1"}, api.fns["fn"].versions)
	assert.Zero(t, api.creates)
}

func TestSeedLambdaExtraFunctions(t *testing.T) {
	api := newFakeLambdaAPI()
	require.NoError(t, seedLambda(context.Background(), api, &fakeQueueAPI{}, LambdaSeedConfig{ExtraFunctions: 3}))
	assert.Len(t, api.fns, 3)
	assert.Contains(t, api.fns, "generated-fn-0003")
}

func TestLoadConfigHasLambda(t *testing.T) {
	small, err := loadConfig("small")
	require.NoError(t, err)
	assert.Len(t, small.Lambda.Functions, 4)
	assert.Zero(t, small.Lambda.ExtraFunctions)
	medium, err := loadConfig("medium")
	require.NoError(t, err)
	assert.Equal(t, 60, medium.Lambda.ExtraFunctions, "more than one page of 50")
}

func TestBuildSeedersHasLambda(t *testing.T) {
	cfg, err := loadConfig("small")
	require.NoError(t, err)
	var names []string
	for _, s := range buildSeeders(nil, cfg, "us-east-1") {
		names = append(names, s.Name())
	}
	assert.Contains(t, names, "lambda")
}

var reportRE = regexp.MustCompile(`^REPORT RequestId: \S+\tDuration: [\d.]+ ms\tBilled Duration: \d+ ms\tMemory Size: \d+ MB\tMax Memory Used: \d+ MB\t(Init Duration: [\d.]+ ms\t)?(Status: (error\tError Type: \S+|timeout)\t)?\n$`)

// The text kind writes four lines for each invocation, oldest first, with
// REPORT lines in the form of Lambda: cold starts, an error and a timeout.
func TestLambdaTextEvents(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ev := lambdaEvents(streamKindLambdaText, 20, now)
	require.Len(t, ev, 80)
	var reports, cold, errs, timeouts int
	last := int64(0)
	for _, e := range ev {
		ts := aws.ToInt64(e.Timestamp)
		require.GreaterOrEqual(t, ts, last, "oldest first")
		last = ts
		m := aws.ToString(e.Message)
		if !strings.HasPrefix(m, "REPORT") {
			continue
		}
		reports++
		assert.Regexp(t, reportRE, m)
		cold += strings.Count(m, "Init Duration")
		errs += strings.Count(m, "Status: error")
		timeouts += strings.Count(m, "Status: timeout")
	}
	assert.Equal(t, 20, reports)
	assert.Equal(t, 2, cold)
	assert.Equal(t, 1, errs)
	assert.Equal(t, 1, timeouts)
	assert.True(t, time.UnixMilli(last).Before(now.Add(time.Second)))
}

// The JSON kind writes platform.report objects; the WARN kind writes only
// the failed ones, as Lambda does with SystemLogLevel WARN.
func TestLambdaJSONEvents(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	starts := map[string]int{}
	statuses := func(kind string) []string {
		var out []string
		for _, e := range lambdaEvents(kind, 20, now) {
			var line struct {
				Type   string `json:"type"`
				Record struct {
					Status  string         `json:"status"`
					Metrics map[string]any `json:"metrics"`
				} `json:"record"`
			}
			require.NoError(t, json.Unmarshal([]byte(aws.ToString(e.Message)), &line))
			if line.Type == "platform.start" {
				starts[kind]++
			}
			if line.Type == "platform.report" {
				out = append(out, line.Record.Status)
				assert.Contains(t, line.Record.Metrics, "durationMs")
				assert.Contains(t, line.Record.Metrics, "maxMemoryUsedMB")
			}
		}
		return out
	}
	all := statuses(streamKindLambdaJSON)
	assert.Len(t, all, 20)
	assert.Contains(t, all, "success")
	assert.ElementsMatch(t, []string{"error", "timeout"}, statuses(streamKindLambdaJSONWarn))
	assert.Equal(t, 20, starts[streamKindLambdaJSON])
	assert.Zero(t, starts[streamKindLambdaJSONWarn], "WARN drops the start lines (INFO)")
}

// The logs seeder writes the invocations of a Lambda kind.
func TestSeedLogGroupWithALambdaStream(t *testing.T) {
	api := newFakeLogsAPI()
	g := logGroupDef{Name: "/aws/lambda/fn", Streams: []logStreamDef{{Name: "s", Events: 10, Kind: streamKindLambdaText}}}
	require.NoError(t, seedLogGroup(context.Background(), api, g, time.Now()))
	assert.Len(t, api.streams["/aws/lambda/fn/s"], 40)
}
