package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLogsAPI keeps groups and streams in memory, as LocalStack does.
type fakeLogsAPI struct {
	groups    map[string]map[string]string // group → tags
	retention map[string]int32
	streams   map[string][]cwltypes.InputLogEvent // "group/stream" → events
	puts      []int                               // the event count of each PutLogEvents
}

func newFakeLogsAPI() *fakeLogsAPI {
	return &fakeLogsAPI{
		groups:    map[string]map[string]string{},
		retention: map[string]int32{},
		streams:   map[string][]cwltypes.InputLogEvent{},
	}
}

func (f *fakeLogsAPI) CreateLogGroup(_ context.Context, in *cloudwatchlogs.CreateLogGroupInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error) {
	name := aws.ToString(in.LogGroupName)
	if _, ok := f.groups[name]; ok {
		return nil, &cwltypes.ResourceAlreadyExistsException{Message: aws.String("exists")}
	}
	f.groups[name] = in.Tags
	return &cloudwatchlogs.CreateLogGroupOutput{}, nil
}

func (f *fakeLogsAPI) PutRetentionPolicy(_ context.Context, in *cloudwatchlogs.PutRetentionPolicyInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error) {
	f.retention[aws.ToString(in.LogGroupName)] = aws.ToInt32(in.RetentionInDays)
	return &cloudwatchlogs.PutRetentionPolicyOutput{}, nil
}

func (f *fakeLogsAPI) CreateLogStream(_ context.Context, in *cloudwatchlogs.CreateLogStreamInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	k := aws.ToString(in.LogGroupName) + "/" + aws.ToString(in.LogStreamName)
	if _, ok := f.streams[k]; ok {
		return nil, &cwltypes.ResourceAlreadyExistsException{Message: aws.String("exists")}
	}
	f.streams[k] = nil
	return &cloudwatchlogs.CreateLogStreamOutput{}, nil
}

func (f *fakeLogsAPI) DescribeLogStreams(_ context.Context, in *cloudwatchlogs.DescribeLogStreamsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogStreamsOutput, error) {
	group := aws.ToString(in.LogGroupName)
	out := &cloudwatchlogs.DescribeLogStreamsOutput{}
	for k, events := range f.streams {
		name, ok := strings.CutPrefix(k, group+"/")
		if !ok || !strings.HasPrefix(name, aws.ToString(in.LogStreamNamePrefix)) {
			continue
		}
		s := cwltypes.LogStream{LogStreamName: aws.String(name)}
		if len(events) > 0 {
			s.FirstEventTimestamp = events[0].Timestamp
		}
		out.LogStreams = append(out.LogStreams, s)
	}
	return out, nil
}

func (f *fakeLogsAPI) PutLogEvents(_ context.Context, in *cloudwatchlogs.PutLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error) {
	k := aws.ToString(in.LogGroupName) + "/" + aws.ToString(in.LogStreamName)
	f.streams[k] = append(f.streams[k], in.LogEvents...)
	f.puts = append(f.puts, len(in.LogEvents))
	return &cloudwatchlogs.PutLogEventsOutput{}, nil
}

func TestSeedLogGroupFillsAndRunsTwiceWithoutDoubling(t *testing.T) {
	f := newFakeLogsAPI()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	g := logGroupDef{
		Name: "/aws/lambda/orders-api", RetentionDays: 30, Tags: map[string]string{"team": "orders"},
		Streams: []logStreamDef{{Name: "big", Events: 2500}, {Name: "small", Events: 3}},
	}
	require.NoError(t, seedLogGroup(context.Background(), f, g, now))
	assert.Equal(t, map[string]string{"team": "orders"}, f.groups[g.Name])
	assert.Equal(t, int32(30), f.retention[g.Name])
	assert.Len(t, f.streams[g.Name+"/big"], 2500)
	assert.Len(t, f.streams[g.Name+"/small"], 3)
	assert.Equal(t, []int{2500, 3}, f.puts, "each stream fits in one PutLogEvents call")

	require.NoError(t, seedLogGroup(context.Background(), f, g, now.Add(time.Hour)))
	assert.Len(t, f.streams[g.Name+"/big"], 2500, "a second run adds no events")
	assert.Len(t, f.puts, 2)
}

func TestEventBatchesKeepTheLimits(t *testing.T) {
	ev := func(n, size int) []cwltypes.InputLogEvent {
		out := make([]cwltypes.InputLogEvent, n)
		for i := range out {
			out[i] = cwltypes.InputLogEvent{Timestamp: aws.Int64(int64(i)), Message: aws.String(strings.Repeat("x", size))}
		}
		return out
	}
	lens := func(bs [][]cwltypes.InputLogEvent) []int {
		var out []int
		for _, b := range bs {
			out = append(out, len(b))
		}
		return out
	}
	assert.Equal(t, []int{2500}, lens(eventBatches(ev(2500, 40))), "small events fit in one call")
	assert.Equal(t, []int{10000, 5}, lens(eventBatches(ev(10005, 1))), "at most 10,000 events")
	// 1,000 events of 1,000 bytes are about 1 MB, more than the byte limit.
	got := eventBatches(ev(1000, 1000))
	require.Len(t, got, 2, "the byte limit splits the events")
	for _, b := range got {
		size := 0
		for _, e := range b {
			size += len(aws.ToString(e.Message)) + 26
		}
		assert.LessOrEqual(t, size, putMaxBytes)
	}
	assert.Empty(t, eventBatches(nil))
}

func TestSeedLogGroupWithoutStreamsOrRetention(t *testing.T) {
	f := newFakeLogsAPI()
	require.NoError(t, seedLogGroup(context.Background(), f, logGroupDef{Name: "/empty/group"}, time.Now()))
	assert.Contains(t, f.groups, "/empty/group")
	assert.NotContains(t, f.retention, "/empty/group", "no retention means never expire")
	assert.Empty(t, f.puts)
}

func TestSeedEventsAreSortedAndMixed(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	events := seedEvents(100, now)
	require.Len(t, events, 100)
	var jsonCount, errorCount int
	for i, e := range events {
		ts := time.UnixMilli(aws.ToInt64(e.Timestamp))
		assert.False(t, ts.After(now), "no event is in the future")
		assert.False(t, ts.Before(now.Add(-50*time.Minute)), "every event is in the last 50 minutes")
		if i > 0 {
			assert.GreaterOrEqual(t, aws.ToInt64(e.Timestamp), aws.ToInt64(events[i-1].Timestamp), "PutLogEvents needs ascending times")
		}
		m := aws.ToString(e.Message)
		if json.Valid([]byte(m)) {
			jsonCount++
		}
		if strings.HasPrefix(m, "ERROR") {
			errorCount++
		}
	}
	assert.Equal(t, 10, jsonCount)
	assert.Equal(t, 10, errorCount)
}

func TestLoadConfigHasLogs(t *testing.T) {
	small, err := loadConfig("small")
	require.NoError(t, err)
	names := map[string]logGroupDef{}
	for _, g := range small.Logs.Groups {
		names[g.Name] = g
	}
	require.Contains(t, names, "/aws/lambda/orders-api")
	assert.Equal(t, 2500, names["/aws/lambda/orders-api"].Streams[0].Events, "one stream needs paging")
	assert.Equal(t, "orders", names["/aws/lambda/orders-api"].Tags["team"])
	require.Contains(t, names, "/empty/group")
	assert.Empty(t, names["/empty/group"].Streams)
	assert.Zero(t, small.Logs.ExtraGroups)

	big, err := loadConfig("enterprise")
	require.NoError(t, err)
	assert.Equal(t, 1200, big.Logs.ExtraGroups, "more than the page budget of 1000 groups")
	assert.Equal(t, 2, big.Logs.StreamsPerExtraGroup)
}

func TestBuildSeedersHasLogs(t *testing.T) {
	cfg, err := loadConfig("small")
	require.NoError(t, err)
	got := filterSeeders(buildSeeders(nil, cfg, "us-east-1"), "logs")
	require.Len(t, got, 1)
	assert.Equal(t, "logs", got[0].Name())
}
