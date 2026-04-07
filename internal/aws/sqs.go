package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"golang.org/x/sync/errgroup"
)

// SQSService defines all SQS operations that views depend on.
type SQSService interface {
	ListQueuesPage(ctx context.Context, token *string) (*QueuePage, error)
	GetQueueAttributes(ctx context.Context, queueURL string) (*Queue, error)
	FetchAllQueueAttributes(ctx context.Context, urls []string) ([]Queue, error)
	ReceiveMessages(ctx context.Context, queueURL string, maxMessages int32) ([]SQSMessage, error)
	SendMessage(ctx context.Context, queueURL, body string, delaySeconds int32, groupID, dedupID string) error
	DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error
	DeleteMessageBatch(ctx context.Context, queueURL string, receiptHandles []string) error
	PurgeQueue(ctx context.Context, queueURL string) error
	DeleteQueue(ctx context.Context, queueURL string) error
	ListDeadLetterSourceQueues(ctx context.Context, queueURL string) ([]string, error)
	StartMessageMoveTask(ctx context.Context, sourceArn, destArn string) (string, error)
	ListMessageMoveTasks(ctx context.Context, sourceArn string) ([]MessageMoveTask, error)
}

// SQSServiceImpl is the real AWS-backed implementation of SQSService.
type SQSServiceImpl struct {
	client *Client
}

// NewSQSService creates a real SQS service backed by the given AWS client.
func NewSQSService(client *Client) *SQSServiceImpl {
	return &SQSServiceImpl{client: client}
}

var _ SQSService = (*SQSServiceImpl)(nil)

// ─── Domain types ───────────────────────────────────────────────────────────

// QueuePage holds one page of SQS queue URLs for progressive loading.
type QueuePage struct {
	QueueURLs    []string
	HasMorePages bool
	Token        *string
}

// Queue represents an SQS queue with parsed attributes.
type Queue struct {
	URL                        string
	Name                       string
	ARN                        string
	Type                       string // "Standard" or "FIFO"
	ApproximateMessageCount    int
	ApproximateInFlightCount   int
	ApproximateDelayedCount    int
	CreatedTimestamp            time.Time
	LastModifiedTimestamp       time.Time
	VisibilityTimeout          int // seconds
	DelaySeconds               int
	MaximumMessageSize         int // bytes
	MessageRetentionPeriod     int // seconds
	ReceiveMessageWaitTime     int // seconds
	RedrivePolicy              *RedrivePolicy
	RedriveAllowPolicy         *RedriveAllowPolicy
	SqsManagedSseEnabled       bool
	KmsMasterKeyID             string
	KmsDataKeyReusePeriod      int // seconds
	FifoQueue                  bool
	ContentBasedDeduplication  bool
	DeduplicationScope         string
	FifoThroughputLimit        string
}

// RedrivePolicy configures dead-letter queue routing.
type RedrivePolicy struct {
	DeadLetterTargetArn string `json:"deadLetterTargetArn"`
	MaxReceiveCount     int    `json:"maxReceiveCount"`
}

// RedriveAllowPolicy controls which source queues can use this queue as a DLQ.
type RedriveAllowPolicy struct {
	RedrivePermission string   `json:"redrivePermission"`
	SourceQueueArns   []string `json:"sourceQueueArns,omitempty"`
}

// SQSMessage represents a message received from an SQS queue.
type SQSMessage struct {
	MessageID              string
	ReceiptHandle          string
	Body                   string
	MD5OfBody              string
	SentTimestamp          time.Time
	ApproximateReceiveCount int
	MessageGroupID         string // FIFO only
	MessageDeduplicationID string // FIFO only
	Attributes             map[string]string
	MessageAttributes      map[string]string
}

// MessageMoveTask represents a DLQ redrive task.
type MessageMoveTask struct {
	TaskHandle                    string
	SourceArn                     string
	DestinationArn                string
	Status                        string
	ApproximateNumberOfMessages   int64
	ApproximateNumberOfMsgsMoved  int64
	StartedTimestamp              time.Time
	FailureReason                 string
}

// ─── Queue listing ──────────────────────────────────────────────────────────

// ListQueuesPage returns one page of queue URLs.
func (svc *SQSServiceImpl) ListQueuesPage(ctx context.Context, token *string) (*QueuePage, error) {
	client := svc.client.SQSClient()
	input := &sqs.ListQueuesInput{
		MaxResults: aws.Int32(100),
	}
	if token != nil {
		input.NextToken = token
	}

	out, err := client.ListQueues(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("ListQueues: %w", err)
	}

	return &QueuePage{
		QueueURLs:    out.QueueUrls,
		HasMorePages: out.NextToken != nil,
		Token:        out.NextToken,
	}, nil
}

// GetQueueAttributes fetches and parses all attributes for a queue.
func (svc *SQSServiceImpl) GetQueueAttributes(ctx context.Context, queueURL string) (*Queue, error) {
	client := svc.client.SQSClient()
	out, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		return nil, fmt.Errorf("GetQueueAttributes: %w", err)
	}

	q := parseQueueAttributes(queueURL, out.Attributes)
	return &q, nil
}

// FetchAllQueueAttributes fetches attributes for all queue URLs concurrently.
func (svc *SQSServiceImpl) FetchAllQueueAttributes(ctx context.Context, urls []string) ([]Queue, error) {
	queues := make([]Queue, len(urls))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for i, url := range urls {
		g.Go(func() error {
			q, err := svc.GetQueueAttributes(ctx, url)
			if err != nil {
				// Fall back to a minimal queue with just the URL/name
				queues[i] = Queue{
					URL:  url,
					Name: QueueNameFromURL(url),
				}
				return nil
			}
			queues[i] = *q
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return queues, nil
}

// ─── Message operations ─────────────────────────────────────────────────────

// ReceiveMessages peeks at messages without hiding them from other consumers.
func (svc *SQSServiceImpl) ReceiveMessages(ctx context.Context, queueURL string, maxMessages int32) ([]SQSMessage, error) {
	if maxMessages > 10 {
		maxMessages = 10
	}
	client := svc.client.SQSClient()
	out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:              aws.String(queueURL),
		MaxNumberOfMessages:   maxMessages,
		VisibilityTimeout:     0, // peek: don't hide from other consumers
		WaitTimeSeconds:       0, // short poll
		AttributeNames:        []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
		MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
			sqstypes.MessageSystemAttributeNameAll,
		},
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		return nil, fmt.Errorf("ReceiveMessage: %w", err)
	}

	messages := make([]SQSMessage, len(out.Messages))
	for i, m := range out.Messages {
		messages[i] = parseSQSMessage(m)
	}
	return messages, nil
}

// SendMessage sends a message to a queue.
func (svc *SQSServiceImpl) SendMessage(ctx context.Context, queueURL, body string, delaySeconds int32, groupID, dedupID string) error {
	client := svc.client.SQSClient()
	input := &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(body),
	}
	if delaySeconds > 0 {
		input.DelaySeconds = delaySeconds
	}
	if groupID != "" {
		input.MessageGroupId = aws.String(groupID)
	}
	if dedupID != "" {
		input.MessageDeduplicationId = aws.String(dedupID)
	}

	_, err := client.SendMessage(ctx, input)
	if err != nil {
		return fmt.Errorf("SendMessage: %w", err)
	}
	return nil
}

// DeleteMessage deletes a single message by receipt handle.
func (svc *SQSServiceImpl) DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error {
	client := svc.client.SQSClient()
	_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: aws.String(receiptHandle),
	})
	if err != nil {
		return fmt.Errorf("DeleteMessage: %w", err)
	}
	return nil
}

// DeleteMessageBatch deletes up to 10 messages at a time. Handles chunking internally.
func (svc *SQSServiceImpl) DeleteMessageBatch(ctx context.Context, queueURL string, receiptHandles []string) error {
	client := svc.client.SQSClient()
	for i := 0; i < len(receiptHandles); i += 10 {
		end := i + 10
		if end > len(receiptHandles) {
			end = len(receiptHandles)
		}
		chunk := receiptHandles[i:end]

		entries := make([]sqstypes.DeleteMessageBatchRequestEntry, len(chunk))
		for j, rh := range chunk {
			entries[j] = sqstypes.DeleteMessageBatchRequestEntry{
				Id:            aws.String(fmt.Sprintf("msg-%d", j)),
				ReceiptHandle: aws.String(rh),
			}
		}

		out, err := client.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
			QueueUrl: aws.String(queueURL),
			Entries:  entries,
		})
		if err != nil {
			return fmt.Errorf("DeleteMessageBatch: %w", err)
		}
		if len(out.Failed) > 0 {
			return fmt.Errorf("DeleteMessageBatch: %d messages failed to delete", len(out.Failed))
		}
	}
	return nil
}

// ─── Queue management ───────────────────────────────────────────────────────

// PurgeQueue removes all messages from a queue.
func (svc *SQSServiceImpl) PurgeQueue(ctx context.Context, queueURL string) error {
	client := svc.client.SQSClient()
	_, err := client.PurgeQueue(ctx, &sqs.PurgeQueueInput{
		QueueUrl: aws.String(queueURL),
	})
	if err != nil {
		return fmt.Errorf("PurgeQueue: %w", err)
	}
	return nil
}

// DeleteQueue deletes a queue.
func (svc *SQSServiceImpl) DeleteQueue(ctx context.Context, queueURL string) error {
	client := svc.client.SQSClient()
	_, err := client.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: aws.String(queueURL),
	})
	if err != nil {
		return fmt.Errorf("DeleteQueue: %w", err)
	}
	return nil
}

// ─── DLQ operations ─────────────────────────────────────────────────────────

// ListDeadLetterSourceQueues returns URLs of queues that use this queue as their DLQ.
func (svc *SQSServiceImpl) ListDeadLetterSourceQueues(ctx context.Context, queueURL string) ([]string, error) {
	client := svc.client.SQSClient()
	var urls []string
	paginator := sqs.NewListDeadLetterSourceQueuesPaginator(client, &sqs.ListDeadLetterSourceQueuesInput{
		QueueUrl: aws.String(queueURL),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("ListDeadLetterSourceQueues: %w", err)
		}
		urls = append(urls, page.QueueUrls...)
	}
	return urls, nil
}

// StartMessageMoveTask starts a DLQ redrive, returning the task handle.
func (svc *SQSServiceImpl) StartMessageMoveTask(ctx context.Context, sourceArn, destArn string) (string, error) {
	client := svc.client.SQSClient()
	input := &sqs.StartMessageMoveTaskInput{
		SourceArn: aws.String(sourceArn),
	}
	if destArn != "" {
		input.DestinationArn = aws.String(destArn)
	}
	out, err := client.StartMessageMoveTask(ctx, input)
	if err != nil {
		return "", fmt.Errorf("StartMessageMoveTask: %w", err)
	}
	return aws.ToString(out.TaskHandle), nil
}

// ListMessageMoveTasks returns active and recent move tasks for a source queue.
func (svc *SQSServiceImpl) ListMessageMoveTasks(ctx context.Context, sourceArn string) ([]MessageMoveTask, error) {
	client := svc.client.SQSClient()
	out, err := client.ListMessageMoveTasks(ctx, &sqs.ListMessageMoveTasksInput{
		SourceArn: aws.String(sourceArn),
	})
	if err != nil {
		return nil, fmt.Errorf("ListMessageMoveTasks: %w", err)
	}

	tasks := make([]MessageMoveTask, len(out.Results))
	for i, r := range out.Results {
		toMove := int64(0)
		if r.ApproximateNumberOfMessagesToMove != nil {
			toMove = *r.ApproximateNumberOfMessagesToMove
		}
		tasks[i] = MessageMoveTask{
			TaskHandle:                    aws.ToString(r.TaskHandle),
			SourceArn:                     aws.ToString(r.SourceArn),
			DestinationArn:                aws.ToString(r.DestinationArn),
			Status:                        aws.ToString(r.Status),
			ApproximateNumberOfMessages:   r.ApproximateNumberOfMessagesMoved + toMove,
			ApproximateNumberOfMsgsMoved:  r.ApproximateNumberOfMessagesMoved,
			StartedTimestamp:              parseEpochMillis(r.StartedTimestamp),
			FailureReason:                 aws.ToString(r.FailureReason),
		}
	}
	return tasks, nil
}

// ─── Attribute parsing ──────────────────────────────────────────────────────

// parseQueueAttributes converts the string attribute map from GetQueueAttributes into a Queue.
func parseQueueAttributes(queueURL string, attrs map[string]string) Queue {
	q := Queue{
		URL:  queueURL,
		Name: QueueNameFromURL(queueURL),
		Type: "Standard",
	}

	q.ARN = attrs["QueueArn"]
	q.ApproximateMessageCount = atoi(attrs["ApproximateNumberOfMessages"])
	q.ApproximateInFlightCount = atoi(attrs["ApproximateNumberOfMessagesNotVisible"])
	q.ApproximateDelayedCount = atoi(attrs["ApproximateNumberOfMessagesDelayed"])
	q.CreatedTimestamp = parseEpochSeconds(attrs["CreatedTimestamp"])
	q.LastModifiedTimestamp = parseEpochSeconds(attrs["LastModifiedTimestamp"])
	q.VisibilityTimeout = atoi(attrs["VisibilityTimeout"])
	q.DelaySeconds = atoi(attrs["DelaySeconds"])
	q.MaximumMessageSize = atoi(attrs["MaximumMessageSize"])
	q.MessageRetentionPeriod = atoi(attrs["MessageRetentionPeriod"])
	q.ReceiveMessageWaitTime = atoi(attrs["ReceiveMessageWaitTimeSeconds"])
	q.SqsManagedSseEnabled = attrs["SqsManagedSseEnabled"] == "true"
	q.KmsMasterKeyID = attrs["KmsMasterKeyId"]
	q.KmsDataKeyReusePeriod = atoi(attrs["KmsDataKeyReusePeriodSeconds"])

	if attrs["FifoQueue"] == "true" {
		q.FifoQueue = true
		q.Type = "FIFO"
	}
	q.ContentBasedDeduplication = attrs["ContentBasedDeduplication"] == "true"
	q.DeduplicationScope = attrs["DeduplicationScope"]
	q.FifoThroughputLimit = attrs["FifoThroughputLimit"]

	if rp := attrs["RedrivePolicy"]; rp != "" {
		var policy RedrivePolicy
		if json.Unmarshal([]byte(rp), &policy) == nil {
			q.RedrivePolicy = &policy
		}
	}
	if rap := attrs["RedriveAllowPolicy"]; rap != "" {
		var policy RedriveAllowPolicy
		if json.Unmarshal([]byte(rap), &policy) == nil {
			q.RedriveAllowPolicy = &policy
		}
	}

	return q
}

// parseSQSMessage converts an SDK message to our domain type.
func parseSQSMessage(m sqstypes.Message) SQSMessage {
	msg := SQSMessage{
		MessageID:     aws.ToString(m.MessageId),
		ReceiptHandle: aws.ToString(m.ReceiptHandle),
		Body:          aws.ToString(m.Body),
		MD5OfBody:     aws.ToString(m.MD5OfBody),
		Attributes:    m.Attributes,
	}

	if ts, ok := m.Attributes["SentTimestamp"]; ok {
		msg.SentTimestamp = parseEpochMillis(atoi64(ts))
	}
	if rc, ok := m.Attributes["ApproximateReceiveCount"]; ok {
		msg.ApproximateReceiveCount = atoi(rc)
	}
	if gid, ok := m.Attributes["MessageGroupId"]; ok {
		msg.MessageGroupID = gid
	}
	if did, ok := m.Attributes["MessageDeduplicationId"]; ok {
		msg.MessageDeduplicationID = did
	}

	// Convert message attributes to simple string map
	if len(m.MessageAttributes) > 0 {
		msg.MessageAttributes = make(map[string]string, len(m.MessageAttributes))
		for k, v := range m.MessageAttributes {
			msg.MessageAttributes[k] = aws.ToString(v.StringValue)
		}
	}

	return msg
}

// QueueNameFromURL extracts the queue name from an SQS queue URL.
func QueueNameFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return url
}

// ─── Parsing helpers ────────────────────────────────────────────────────────

func atoi(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseEpochSeconds(s string) time.Time {
	sec := atoi64(s)
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func parseEpochMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
