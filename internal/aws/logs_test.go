package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLogs answers CloudWatch Logs calls in the awsjson 1.1 protocol. Each
// operation has a reply; the fake records the JSON input of each call.
type fakeLogs struct {
	mu      sync.Mutex
	replies map[string]func(in map[string]any) (int, string)
	calls   []fakeLogsCall
}

type fakeLogsCall struct {
	op string
	in map[string]any
}

func newFakeLogs(t *testing.T) (*fakeLogs, *LogsServiceImpl) {
	t.Helper()
	f := &fakeLogs{replies: map[string]func(map[string]any) (int, string){}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	// Keep the developer's AWS setup out of the test.
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewLogsService(c)
}

// reply sets the answer of op to a fixed JSON body.
func (f *fakeLogs) reply(op, body string) {
	f.replies[op] = func(map[string]any) (int, string) { return http.StatusOK, body }
}

func (f *fakeLogs) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "Logs_20140328.")
	raw, _ := io.ReadAll(r.Body)
	in := map[string]any{}
	_ = json.Unmarshal(raw, &in)
	f.calls = append(f.calls, fakeLogsCall{op: op, in: in})
	fn, ok := f.replies[op]
	if !ok {
		http.Error(w, "unexpected operation "+op, http.StatusNotImplemented)
		return
	}
	status, body := fn(in)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	if status != http.StatusOK {
		var e struct {
			Type string `json:"__type"`
		}
		_ = json.Unmarshal([]byte(body), &e)
		w.Header().Set("X-Amzn-Errortype", e.Type)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// last returns the input of the last call of op.
func (f *fakeLogs) last(t *testing.T, op string) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range slices.Backward(f.calls) {
		if c.op == op {
			return c.in
		}
	}
	t.Fatalf("no %s call", op)
	return nil
}

const groupJSON = `{"logGroupName":"/aws/lambda/orders","arn":"arn:aws:logs:us-east-1:123:log-group:/aws/lambda/orders:*",
"logGroupArn":"arn:aws:logs:us-east-1:123:log-group:/aws/lambda/orders","logGroupClass":"STANDARD",
"retentionInDays":30,"storedBytes":2048,"creationTime":1763600000000,"metricFilterCount":2,
"kmsKeyId":"arn:aws:kms:us-east-1:123:key/k","dataProtectionStatus":"ACTIVATED","deletionProtectionEnabled":true}`

func TestListLogGroupsPageMapsFields(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.reply("DescribeLogGroups", `{"logGroups":[`+groupJSON+`,{"logGroupName":"/never","logGroupArn":"arn:x"}],"nextToken":"t2"}`)

	tok := "t1"
	page, err := svc.ListLogGroupsPage(context.Background(), &tok)
	require.NoError(t, err)
	in := f.last(t, "DescribeLogGroups")
	assert.InDelta(t, 50, in["limit"], 0)
	assert.Equal(t, "t1", in["nextToken"])

	require.Len(t, page.Groups, 2)
	g := page.Groups[0]
	assert.Equal(t, LogGroup{
		Name:               "/aws/lambda/orders",
		ARN:                "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/orders",
		Class:              "STANDARD",
		RetentionDays:      30,
		StoredBytes:        2048,
		Created:            time.UnixMilli(1763600000000),
		MetricFilters:      2,
		KMSKeyID:           "arn:aws:kms:us-east-1:123:key/k",
		DataProtection:     "ACTIVATED",
		DeletionProtection: true,
	}, g, "the ARN is the one without :*")
	assert.Equal(t, 0, page.Groups[1].RetentionDays, "no retention means never expire")
	assert.True(t, page.Groups[1].Created.IsZero())
	require.NotNil(t, page.Next)
	assert.Equal(t, "t2", *page.Next)
}

func TestListLogGroupsPageEmptyTokenEnds(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.reply("DescribeLogGroups", `{"logGroups":[],"nextToken":""}`)
	page, err := svc.ListLogGroupsPage(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, page.Next)
	_, sent := f.last(t, "DescribeLogGroups")["nextToken"]
	assert.False(t, sent, "the first page sends no token")
}

func TestGetLogGroupTakesTheExactName(t *testing.T) {
	f, svc := newFakeLogs(t)
	// The answer is sorted by name, but the test puts the longer name first
	// to show that the exact match wins, not the first row.
	f.reply("DescribeLogGroups", `{"logGroups":[{"logGroupName":"/aws/lambda/orders-api"},`+groupJSON+`]}`)
	g, err := svc.GetLogGroup(context.Background(), "/aws/lambda/orders")
	require.NoError(t, err)
	assert.Equal(t, "/aws/lambda/orders", g.Name)
	assert.Equal(t, "STANDARD", g.Class)
	assert.Equal(t, "/aws/lambda/orders", f.last(t, "DescribeLogGroups")["logGroupNamePrefix"])
	_, pattern := f.last(t, "DescribeLogGroups")["logGroupNamePattern"]
	assert.False(t, pattern, "a pattern search returns only three fields")
}

func TestGetLogGroupNotFound(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.reply("DescribeLogGroups", `{"logGroups":[{"logGroupName":"/aws/lambda/orders-api"}]}`)
	_, err := svc.GetLogGroup(context.Background(), "/aws/lambda/orders")
	require.ErrorIs(t, err, ErrLogGroupNotFound)
	assert.Contains(t, err.Error(), "/aws/lambda/orders")
}

func TestListLogStreamsPageNewestFirst(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.reply("DescribeLogStreams", `{"logStreams":[{"logStreamName":"2026/11/20/[$LATEST]abc","arn":"arn:s",
"creationTime":1763600000000,"firstEventTimestamp":1763600001000,"lastEventTimestamp":1763600002000,
"lastIngestionTime":1763600003000,"storedBytes":0}],"nextToken":"n"}`)
	page, err := svc.ListLogStreamsPage(context.Background(), "/aws/lambda/orders", nil)
	require.NoError(t, err)
	in := f.last(t, "DescribeLogStreams")
	assert.Equal(t, "/aws/lambda/orders", in["logGroupName"])
	assert.Equal(t, "LastEventTime", in["orderBy"])
	assert.Equal(t, true, in["descending"])
	assert.InDelta(t, 50, in["limit"], 0)
	require.Len(t, page.Streams, 1)
	assert.Equal(t, LogStream{
		Name:          "2026/11/20/[$LATEST]abc",
		ARN:           "arn:s",
		Created:       time.UnixMilli(1763600000000),
		FirstEvent:    time.UnixMilli(1763600001000),
		LastEvent:     time.UnixMilli(1763600002000),
		LastIngestion: time.UnixMilli(1763600003000),
	}, page.Streams[0])
	assert.Equal(t, "n", *page.Next)
}

func TestGetStreamEventsNewestFirstAndRepeatedTokenEnds(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.replies["GetLogEvents"] = func(in map[string]any) (int, string) {
		if in["nextToken"] == "b/1" {
			// The start of the stream: an empty page, and the same token back.
			return http.StatusOK, `{"events":[],"nextBackwardToken":"b/1","nextForwardToken":"f/1"}`
		}
		return http.StatusOK, `{"events":[{"timestamp":1763600000000,"ingestionTime":1763600000500,"message":"a"},
{"timestamp":1763600001000,"ingestionTime":1763600001500,"message":"b"}],"nextBackwardToken":"b/1","nextForwardToken":"f/2"}`
	}
	start, end := time.UnixMilli(1763590000000), time.UnixMilli(1763610000000)

	page, err := svc.GetStreamEvents(context.Background(), EventQuery{
		Group: "/g", Stream: "s", Start: start, End: end, Limit: 500,
	})
	require.NoError(t, err)
	in := f.last(t, "GetLogEvents")
	assert.Equal(t, "/g", in["logGroupName"], "the group name, because LocalStack does not take the ARN")
	assert.Equal(t, "s", in["logStreamName"])
	assert.Equal(t, false, in["startFromHead"])
	assert.InDelta(t, 1763590000000, in["startTime"], 0)
	assert.InDelta(t, 1763610000000, in["endTime"], 0)
	assert.InDelta(t, 500, in["limit"], 0)
	require.Len(t, page.Events, 2)
	assert.Equal(t, LogEvent{Time: time.UnixMilli(1763600000000), Ingested: time.UnixMilli(1763600000500), Message: "a"}, page.Events[0])
	assert.False(t, page.End, "the first page is not the start of the stream")
	assert.Equal(t, "b/1", *page.Next, "Next goes back in time")

	page, err = svc.GetStreamEvents(context.Background(), EventQuery{Group: "/g", Stream: "s", Token: page.Next})
	require.NoError(t, err)
	assert.Empty(t, page.Events)
	assert.True(t, page.End, "the token came back: the start of the stream")
	in = f.last(t, "GetLogEvents")
	for _, k := range []string{"limit", "startTime", "endTime"} {
		_, sent := in[k]
		assert.False(t, sent, "%s is not sent when it is zero", k)
	}
}

func TestFilterEventsNewestFirstWithPattern(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.replies["FilterLogEvents"] = func(in map[string]any) (int, string) {
		if in["nextToken"] == nil {
			// An empty page that still has a token is not the end.
			return http.StatusOK, `{"events":[],"nextToken":"n2"}`
		}
		return http.StatusOK, `{"events":[{"eventId":"e1","logStreamName":"s1","timestamp":1763600000000,
"ingestionTime":1763600000100,"message":"ERROR boom"}]}`
	}
	start := time.UnixMilli(1763590000000)
	page, err := svc.FilterEvents(context.Background(), EventQuery{
		Group: "/g", Streams: []string{"s1"}, Pattern: "ERROR", Start: start, Limit: 1000,
	})
	require.NoError(t, err)
	in := f.last(t, "FilterLogEvents")
	assert.Equal(t, "/g", in["logGroupName"])
	assert.Equal(t, []any{"s1"}, in["logStreamNames"])
	assert.Equal(t, "ERROR", in["filterPattern"])
	assert.Equal(t, false, in["startFromHead"], "newest first")
	assert.InDelta(t, 1763590000000, in["startTime"], 0)
	_, end := in["endTime"]
	assert.False(t, end, "a relative range sends no end time")
	assert.InDelta(t, 1000, in["limit"], 0)
	assert.Empty(t, page.Events)
	assert.False(t, page.End)

	page, err = svc.FilterEvents(context.Background(), EventQuery{Group: "/g", Token: page.Next, Streams: []string{}})
	require.NoError(t, err)
	assert.True(t, page.End, "no token is the end")
	assert.Equal(t, []LogEvent{{
		Time: time.UnixMilli(1763600000000), Ingested: time.UnixMilli(1763600000100),
		Message: "ERROR boom", Stream: "s1", ID: "e1",
	}}, page.Events)
	in = f.last(t, "FilterLogEvents")
	for _, k := range []string{"filterPattern", "logStreamNames"} {
		_, sent := in[k]
		assert.False(t, sent, "%s is not sent when it is empty, also as an empty list", k)
	}
}

// FilterLogEvents gives a newest-first page in descending order. Every
// EventPage is ascending, so FilterEvents turns the page around.
func TestFilterEventsPageIsAscending(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.reply("FilterLogEvents", `{"events":[
{"eventId":"e3","logStreamName":"s","timestamp":1763600003000,"message":"c"},
{"eventId":"e2","logStreamName":"s","timestamp":1763600002000,"message":"b"},
{"eventId":"e1","logStreamName":"s","timestamp":1763600001000,"message":"a"}]}`)
	page, err := svc.FilterEvents(context.Background(), EventQuery{Group: "/g"})
	require.NoError(t, err)
	var got []string
	for _, e := range page.Events {
		got = append(got, e.Message)
	}
	assert.Equal(t, []string{"a", "b", "c"}, got)
}

func TestListLogGroupTagsUsesTheARN(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.reply("ListTagsForResource", `{"tags":{"team":"orders"}}`)
	tags, err := svc.ListLogGroupTags(context.Background(), "arn:aws:logs:us-east-1:123:log-group:/g")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"team": "orders"}, tags)
	assert.Equal(t, "arn:aws:logs:us-east-1:123:log-group:/g", f.last(t, "ListTagsForResource")["resourceArn"])
}

func TestLogsThrottlingGivesTheHint(t *testing.T) {
	f, svc := newFakeLogs(t)
	f.replies["FilterLogEvents"] = func(map[string]any) (int, string) {
		return http.StatusBadRequest, `{"__type":"ThrottlingException","message":"Rate exceeded"}`
	}
	_, err := svc.FilterEvents(context.Background(), EventQuery{Group: "/g"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FilterLogEvents")
	assert.Contains(t, ErrorText(err), "AWS limits the request rate")
}

// recordingHTTP fails each request after it records the host.
type recordingHTTP struct {
	mu    sync.Mutex
	hosts []string
}

var errRecorded = errors.New("recorded")

func (r *recordingHTTP) Do(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, req.URL.Host)
	return nil, errRecorded
}

// H5 of docs/design/cwlogs.md: with an endpoint override, StartLiveTail
// goes to the endpoint host with "stream-" in front, which does not
// resolve for LocalStack. PR 6 turns the prefix off for that call. Other
// calls go to the endpoint host as it is.
func TestLogsHazard_LiveTailHostPrefixWithEndpoint(t *testing.T) {
	// Keep the developer's AWS setup out of the test.
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	c, err := NewClient("", "us-east-1", "http://localhost:4566")
	require.NoError(t, err)
	rec := &recordingHTTP{}
	c.Config.HTTPClient = rec

	_, err = c.LogsClient().StartLiveTail(context.Background(), &cloudwatchlogs.StartLiveTailInput{
		LogGroupIdentifiers: []string{"arn:aws:logs:us-east-1:000000000000:log-group:/g"},
	})
	require.ErrorIs(t, err, errRecorded)
	_, err = c.LogsClient().DescribeLogGroups(context.Background(), &cloudwatchlogs.DescribeLogGroupsInput{})
	require.ErrorIs(t, err, errRecorded)
	assert.Equal(t, []string{"stream-localhost:4566", "localhost:4566"}, rec.hosts)
}
