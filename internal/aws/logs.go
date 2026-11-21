package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// LogsService defines the CloudWatch Logs calls that views depend on.
// Every call reads. See docs/design/cwlogs.md.
type LogsService interface {
	ListLogGroupsPage(ctx context.Context, token *string) (*LogGroupPage, error)
	GetLogGroup(ctx context.Context, name string) (*LogGroup, error)
	ListLogStreamsPage(ctx context.Context, group string, token *string) (*LogStreamPage, error)
	GetStreamEvents(ctx context.Context, q EventQuery) (*EventPage, error)
	FilterEvents(ctx context.Context, q EventQuery) (*EventPage, error)
	ListLogGroupTags(ctx context.Context, arn string) (map[string]string, error)
	StartLiveTail(ctx context.Context, q TailQuery) (LiveTail, error)
}

// LogsServiceImpl is the real AWS-backed implementation of LogsService.
type LogsServiceImpl struct {
	client *Client
}

// NewLogsService creates a real CloudWatch Logs service backed by client.
func NewLogsService(client *Client) *LogsServiceImpl {
	return &LogsServiceImpl{client: client}
}

var _ LogsService = (*LogsServiceImpl)(nil)

// ErrLogGroupNotFound is returned by GetLogGroup when no group has the name.
var ErrLogGroupNotFound = errors.New("log group not found")

// ─── Domain types ───────────────────────────────────────────────────────────

// LogGroup is a CloudWatch Logs log group.
type LogGroup struct {
	Name string
	// ARN has no ":*" at the end. Tags and StartLiveTail need this form;
	// the other ARN of the API (with ":*") is for IAM policies.
	ARN                string
	Class              string // STANDARD, INFREQUENT_ACCESS or DELIVERY
	RetentionDays      int    // 0: the events never expire
	StoredBytes        int64
	Created            time.Time
	MetricFilters      int
	KMSKeyID           string
	DataProtection     string
	DeletionProtection bool
}

// LogGroupPage is one page of log groups.
type LogGroupPage struct {
	Groups []LogGroup
	Next   *string // nil: the last page
}

// LogStream is a log stream of a group.
type LogStream struct {
	Name string
	ARN  string
	// LastEvent and LastIngestion are eventually consistent: AWS says they
	// typically update in less than an hour.
	Created       time.Time
	FirstEvent    time.Time
	LastEvent     time.Time
	LastIngestion time.Time
}

// LogStreamPage is one page of log streams.
type LogStreamPage struct {
	Streams []LogStream
	Next    *string // nil: the last page
}

// LogEvent is one log event.
type LogEvent struct {
	Time     time.Time
	Ingested time.Time
	Message  string
	Stream   string // empty for GetStreamEvents, which reads one stream
	ID       string // empty for GetStreamEvents
}

// EventQuery selects the events of one call. Zero times leave the range
// open on that side.
type EventQuery struct {
	Group   string
	Stream  string   // GetStreamEvents: the stream
	Streams []string // FilterEvents: only these streams; nil: all
	Pattern string   // FilterEvents: the filter pattern; "": all events
	Start   time.Time
	End     time.Time
	Limit   int32 // 0: the default of AWS (up to 10,000 events)
	Token   *string
}

// EventPage is one page of events, in ascending time order. Next goes back
// in time. End is true when no older page exists: a nil token from
// FilterLogEvents, or a GetLogEvents token equal to the token of the call.
type EventPage struct {
	Events []LogEvent
	Next   *string
	End    bool
}

// ─── Log groups ─────────────────────────────────────────────────────────────

// ListLogGroupsPage returns one page of log groups, sorted by name.
func (svc *LogsServiceImpl) ListLogGroupsPage(ctx context.Context, token *string) (*LogGroupPage, error) {
	out, err := svc.client.LogsClient().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		Limit:     aws.Int32(50),
		NextToken: token,
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeLogGroups: %w", err)
	}
	page := &LogGroupPage{Next: nonEmpty(out.NextToken)}
	for i := range out.LogGroups {
		page.Groups = append(page.Groups, logGroupFrom(&out.LogGroups[i]))
	}
	return page, nil
}

// GetLogGroup returns the group with exactly this name. It asks for the
// name as a prefix: the answer is sorted by name, so the exact name comes
// first when it exists. A pattern search would return only three fields.
func (svc *LogsServiceImpl) GetLogGroup(ctx context.Context, name string) (*LogGroup, error) {
	out, err := svc.client.LogsClient().DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(name),
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeLogGroups: %w", err)
	}
	for i := range out.LogGroups {
		if aws.ToString(out.LogGroups[i].LogGroupName) == name {
			g := logGroupFrom(&out.LogGroups[i])
			return &g, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrLogGroupNotFound, name)
}

func logGroupFrom(g *cwltypes.LogGroup) LogGroup {
	return LogGroup{
		Name:               aws.ToString(g.LogGroupName),
		ARN:                aws.ToString(g.LogGroupArn),
		Class:              string(g.LogGroupClass),
		RetentionDays:      int(aws.ToInt32(g.RetentionInDays)),
		StoredBytes:        aws.ToInt64(g.StoredBytes),
		Created:            parseEpochMillis(aws.ToInt64(g.CreationTime)),
		MetricFilters:      int(aws.ToInt32(g.MetricFilterCount)),
		KMSKeyID:           aws.ToString(g.KmsKeyId),
		DataProtection:     string(g.DataProtectionStatus),
		DeletionProtection: aws.ToBool(g.DeletionProtectionEnabled),
	}
}

// ListLogGroupTags returns the tags of a group. arn is LogGroup.ARN; the
// ARN with ":*" gives no tags.
func (svc *LogsServiceImpl) ListLogGroupTags(ctx context.Context, arn string) (map[string]string, error) {
	out, err := svc.client.LogsClient().ListTagsForResource(ctx, &cloudwatchlogs.ListTagsForResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		return nil, fmt.Errorf("ListTagsForResource: %w", err)
	}
	return out.Tags, nil
}

// ─── Log streams ────────────────────────────────────────────────────────────

// ListLogStreamsPage returns one page of the streams of group, the stream
// with the newest event first.
func (svc *LogsServiceImpl) ListLogStreamsPage(ctx context.Context, group string, token *string) (*LogStreamPage, error) {
	out, err := svc.client.LogsClient().DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName: aws.String(group),
		OrderBy:      cwltypes.OrderByLastEventTime,
		Descending:   aws.Bool(true),
		Limit:        aws.Int32(50),
		NextToken:    token,
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeLogStreams: %w", err)
	}
	page := &LogStreamPage{Next: nonEmpty(out.NextToken)}
	for _, s := range out.LogStreams {
		page.Streams = append(page.Streams, LogStream{
			Name:          aws.ToString(s.LogStreamName),
			ARN:           aws.ToString(s.Arn),
			Created:       parseEpochMillis(aws.ToInt64(s.CreationTime)),
			FirstEvent:    parseEpochMillis(aws.ToInt64(s.FirstEventTimestamp)),
			LastEvent:     parseEpochMillis(aws.ToInt64(s.LastEventTimestamp)),
			LastIngestion: parseEpochMillis(aws.ToInt64(s.LastIngestionTime)),
		})
	}
	return page, nil
}

// ─── Events ─────────────────────────────────────────────────────────────────

// GetStreamEvents returns one page of the events of one stream, newest
// first: the first call reads the newest page, and Next goes back. At the
// start of the stream, AWS returns the token that the call sent, and the
// page has End set. It uses LogGroupName, because LocalStack does not find
// a stream through the group ARN.
func (svc *LogsServiceImpl) GetStreamEvents(ctx context.Context, q EventQuery) (*EventPage, error) {
	in := &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(q.Group),
		LogStreamName: aws.String(q.Stream),
		StartFromHead: aws.Bool(false),
		StartTime:     millis(q.Start),
		EndTime:       millis(q.End),
		NextToken:     q.Token,
	}
	if q.Limit > 0 {
		in.Limit = aws.Int32(q.Limit)
	}
	out, err := svc.client.LogsClient().GetLogEvents(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("GetLogEvents: %w", err)
	}
	page := &EventPage{Next: out.NextBackwardToken}
	page.End = out.NextBackwardToken == nil ||
		(q.Token != nil && aws.ToString(out.NextBackwardToken) == *q.Token)
	for _, e := range out.Events {
		page.Events = append(page.Events, LogEvent{
			Time:     parseEpochMillis(aws.ToInt64(e.Timestamp)),
			Ingested: parseEpochMillis(aws.ToInt64(e.IngestionTime)),
			Message:  aws.ToString(e.Message),
		})
	}
	return page, nil
}

// FilterEvents returns one page of the events of a group, newest first,
// with the filter pattern of q. AWS sends the page in descending order, and
// FilterEvents turns it around, so the page is ascending like every
// EventPage. A page can be empty and still have a
// token; End is set only when AWS returns no token. AWS takes newest first
// only with a start time on or after 2024-01-01 UTC, so the caller keeps
// q.Start after that date.
func (svc *LogsServiceImpl) FilterEvents(ctx context.Context, q EventQuery) (*EventPage, error) {
	in := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  aws.String(q.Group),
		StartFromHead: aws.Bool(false),
		StartTime:     millis(q.Start),
		EndTime:       millis(q.End),
		NextToken:     q.Token,
	}
	if len(q.Streams) > 0 {
		in.LogStreamNames = q.Streams
	}
	if q.Pattern != "" {
		in.FilterPattern = aws.String(q.Pattern)
	}
	if q.Limit > 0 {
		in.Limit = aws.Int32(q.Limit)
	}
	out, err := svc.client.LogsClient().FilterLogEvents(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("FilterLogEvents: %w", err)
	}
	page := &EventPage{Next: nonEmpty(out.NextToken)}
	page.End = page.Next == nil
	for _, e := range out.Events {
		page.Events = append(page.Events, LogEvent{
			Time:     parseEpochMillis(aws.ToInt64(e.Timestamp)),
			Ingested: parseEpochMillis(aws.ToInt64(e.IngestionTime)),
			Message:  aws.ToString(e.Message),
			Stream:   aws.ToString(e.LogStreamName),
			ID:       aws.ToString(e.EventId),
		})
	}
	// Newest first comes in descending order. Every EventPage is ascending.
	slices.Reverse(page.Events)
	return page, nil
}

// millis returns t in milliseconds since the epoch, or nil for a zero t.
func millis(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	return aws.Int64(t.UnixMilli())
}

// nonEmpty returns nil for a nil or empty token.
func nonEmpty(tok *string) *string {
	if tok == nil || *tok == "" {
		return nil
	}
	return tok
}
