package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

// logsAPI is the part of the CloudWatch Logs client that the seeder uses,
// so a test can give a fake.
type logsAPI interface {
	CreateLogGroup(ctx context.Context, in *cloudwatchlogs.CreateLogGroupInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error)
	PutRetentionPolicy(ctx context.Context, in *cloudwatchlogs.PutRetentionPolicyInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
	CreateLogStream(ctx context.Context, in *cloudwatchlogs.CreateLogStreamInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error)
	DescribeLogStreams(ctx context.Context, in *cloudwatchlogs.DescribeLogStreamsInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogStreamsOutput, error)
	PutLogEvents(ctx context.Context, in *cloudwatchlogs.PutLogEventsInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error)
}

type logsSeeder struct {
	client *awslib.Client
	config LogsSeedConfig
}

func newLogsSeeder(client *awslib.Client, cfg LogsSeedConfig) *logsSeeder {
	return &logsSeeder{client: client, config: cfg}
}

func (s *logsSeeder) Name() string { return "logs" }

func (s *logsSeeder) Seed(ctx context.Context) error {
	api := s.client.LogsClient()
	now := time.Now()

	fmt.Printf("  CloudWatch log groups...")
	for _, g := range s.config.Groups {
		if err := seedLogGroup(ctx, api, g, now); err != nil {
			return err
		}
	}
	for i := range s.config.ExtraGroups {
		g := logGroupDef{Name: fmt.Sprintf("/generated/group-%04d", i+1)}
		for j := range s.config.StreamsPerExtraGroup {
			g.Streams = append(g.Streams, logStreamDef{Name: fmt.Sprintf("stream-%d", j+1), Events: 10})
		}
		if err := seedLogGroup(ctx, api, g, now); err != nil {
			return err
		}
	}
	fmt.Println(" done")
	return nil
}

// seedLogGroup makes the group and its streams, and puts events into each
// stream that has none. A second run adds nothing.
func seedLogGroup(ctx context.Context, api logsAPI, g logGroupDef, now time.Time) error {
	in := &cloudwatchlogs.CreateLogGroupInput{LogGroupName: aws.String(g.Name)}
	if len(g.Tags) > 0 {
		in.Tags = g.Tags
	}
	if _, err := api.CreateLogGroup(ctx, in); err != nil && !alreadyExists(err) {
		return fmt.Errorf("creating log group %s: %w", g.Name, err)
	}
	if g.RetentionDays > 0 {
		if _, err := api.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
			LogGroupName:    aws.String(g.Name),
			RetentionInDays: aws.Int32(g.RetentionDays),
		}); err != nil {
			return fmt.Errorf("setting retention of %s: %w", g.Name, err)
		}
	}
	for _, st := range g.Streams {
		if err := seedLogStream(ctx, api, g.Name, st, now); err != nil {
			return err
		}
	}
	return nil
}

func seedLogStream(ctx context.Context, api logsAPI, group string, st logStreamDef, now time.Time) error {
	if _, err := api.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(group),
		LogStreamName: aws.String(st.Name),
	}); err != nil && !alreadyExists(err) {
		return fmt.Errorf("creating log stream %s/%s: %w", group, st.Name, err)
	}
	has, err := streamHasEvents(ctx, api, group, st.Name)
	if err != nil {
		return err
	}
	if has || st.Events == 0 {
		return nil
	}
	// The check above only sees whether a stream has events. So a stream
	// gets all its events in one call when they fit, and a failed run
	// cannot leave it half full.
	events := seedEvents(st.Events, now)
	if st.Kind != "" {
		events = lambdaEvents(st.Kind, st.Events, now)
	}
	for _, b := range eventBatches(events) {
		if _, err := api.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
			LogGroupName:  aws.String(group),
			LogStreamName: aws.String(st.Name),
			LogEvents:     b,
		}); err != nil {
			return fmt.Errorf("putting events into %s/%s: %w", group, st.Name, err)
		}
	}
	return nil
}

// The limits of one PutLogEvents call: 10,000 events, and 1 MB counted as
// the bytes of each message plus 26 bytes for each event. The byte limit
// here keeps a margin.
const (
	putMaxEvents = 10000
	putMaxBytes  = 900 * 1024
)

// eventBatches splits events into as few PutLogEvents calls as the limits
// allow.
func eventBatches(events []cwltypes.InputLogEvent) [][]cwltypes.InputLogEvent {
	var out [][]cwltypes.InputLogEvent
	start, size := 0, 0
	for i, e := range events {
		n := len(aws.ToString(e.Message)) + 26
		if i > start && (i-start == putMaxEvents || size+n > putMaxBytes) {
			out = append(out, events[start:i])
			start, size = i, 0
		}
		size += n
	}
	if start < len(events) {
		out = append(out, events[start:])
	}
	return out
}

// streamHasEvents reports whether the stream already holds events, so a
// second seed run does not add them again.
func streamHasEvents(ctx context.Context, api logsAPI, group, stream string) (bool, error) {
	out, err := api.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName:        aws.String(group),
		LogStreamNamePrefix: aws.String(stream),
	})
	if err != nil {
		return false, fmt.Errorf("reading log stream %s/%s: %w", group, stream, err)
	}
	for _, s := range out.LogStreams {
		if aws.ToString(s.LogStreamName) == stream {
			return s.FirstEventTimestamp != nil, nil
		}
	}
	return false, nil
}

// seedEvents returns n events spread over the 50 minutes before now, oldest
// first, as PutLogEvents needs. Every 5th message is JSON, and every 10th is
// an error, so the viewer has JSON to format and words to color.
func seedEvents(n int, now time.Time) []cwltypes.InputLogEvent {
	span := 50 * time.Minute
	events := make([]cwltypes.InputLogEvent, n)
	for i := range n {
		at := now.Add(-span + span*time.Duration(i)/time.Duration(n))
		var m string
		switch {
		case i%10 == 9:
			m = fmt.Sprintf("ERROR payment failed order=%d reason=card_declined", i)
		case i%5 == 4:
			m = fmt.Sprintf(`{"level":"info","msg":"request handled","n":%d,"duration_ms":%d}`, i, 10+i%90)
		default:
			m = fmt.Sprintf("INFO request ok id=%d path=/orders/%d", i, i%50)
		}
		events[i] = cwltypes.InputLogEvent{Timestamp: aws.Int64(at.UnixMilli()), Message: aws.String(m)}
	}
	return events
}

func alreadyExists(err error) bool {
	var e *cwltypes.ResourceAlreadyExistsException
	return errors.As(err, &e)
}
