package aws

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseQueueAttributes_Standard(t *testing.T) {
	attrs := map[string]string{
		"QueueArn":                          "arn:aws:sqs:us-east-1:123456789012:order-processing",
		"ApproximateNumberOfMessages":        "42",
		"ApproximateNumberOfMessagesNotVisible": "3",
		"ApproximateNumberOfMessagesDelayed":    "1",
		"CreatedTimestamp":                   "1700000000",
		"LastModifiedTimestamp":              "1700001000",
		"VisibilityTimeout":                  "30",
		"DelaySeconds":                       "0",
		"MaximumMessageSize":                 "262144",
		"MessageRetentionPeriod":             "345600",
		"ReceiveMessageWaitTimeSeconds":      "20",
		"SqsManagedSseEnabled":               "true",
	}

	q := parseQueueAttributes("https://sqs.us-east-1.amazonaws.com/123456789012/order-processing", attrs)

	assert.Equal(t, "order-processing", q.Name)
	assert.Equal(t, "Standard", q.Type)
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:order-processing", q.ARN)
	assert.Equal(t, 42, q.ApproximateMessageCount)
	assert.Equal(t, 3, q.ApproximateInFlightCount)
	assert.Equal(t, 1, q.ApproximateDelayedCount)
	assert.Equal(t, time.Unix(1700000000, 0), q.CreatedTimestamp)
	assert.Equal(t, 30, q.VisibilityTimeout)
	assert.Equal(t, 262144, q.MaximumMessageSize)
	assert.Equal(t, 345600, q.MessageRetentionPeriod)
	assert.Equal(t, 20, q.ReceiveMessageWaitTime)
	assert.True(t, q.SqsManagedSseEnabled)
	assert.False(t, q.FifoQueue)
	assert.Nil(t, q.RedrivePolicy)
}

func TestParseQueueAttributes_FIFO(t *testing.T) {
	attrs := map[string]string{
		"QueueArn":                    "arn:aws:sqs:us-east-1:123456789012:payments.fifo",
		"FifoQueue":                   "true",
		"ContentBasedDeduplication":   "true",
		"DeduplicationScope":          "messageGroup",
		"FifoThroughputLimit":         "perMessageGroupId",
		"CreatedTimestamp":            "1700000000",
	}

	q := parseQueueAttributes("https://sqs.us-east-1.amazonaws.com/123456789012/payments.fifo", attrs)

	assert.Equal(t, "payments.fifo", q.Name)
	assert.Equal(t, "FIFO", q.Type)
	assert.True(t, q.FifoQueue)
	assert.True(t, q.ContentBasedDeduplication)
	assert.Equal(t, "messageGroup", q.DeduplicationScope)
	assert.Equal(t, "perMessageGroupId", q.FifoThroughputLimit)
}

func TestParseQueueAttributes_WithRedrivePolicy(t *testing.T) {
	attrs := map[string]string{
		"QueueArn":       "arn:aws:sqs:us-east-1:123456789012:my-queue",
		"RedrivePolicy":  `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:123456789012:my-dlq","maxReceiveCount":5}`,
		"CreatedTimestamp": "1700000000",
	}

	q := parseQueueAttributes("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue", attrs)

	assert.NotNil(t, q.RedrivePolicy)
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:my-dlq", q.RedrivePolicy.DeadLetterTargetArn)
	assert.Equal(t, 5, q.RedrivePolicy.MaxReceiveCount)
}

func TestParseQueueAttributes_WithRedriveAllowPolicy(t *testing.T) {
	attrs := map[string]string{
		"QueueArn":             "arn:aws:sqs:us-east-1:123456789012:my-dlq",
		"RedriveAllowPolicy":   `{"redrivePermission":"byQueue","sourceQueueArns":["arn:aws:sqs:us-east-1:123456789012:q1"]}`,
		"CreatedTimestamp":     "1700000000",
	}

	q := parseQueueAttributes("https://sqs.us-east-1.amazonaws.com/123456789012/my-dlq", attrs)

	assert.NotNil(t, q.RedriveAllowPolicy)
	assert.Equal(t, "byQueue", q.RedriveAllowPolicy.RedrivePermission)
	assert.Len(t, q.RedriveAllowPolicy.SourceQueueArns, 1)
}

func TestParseQueueAttributes_MalformedJSON(t *testing.T) {
	attrs := map[string]string{
		"QueueArn":       "arn:aws:sqs:us-east-1:123456789012:my-queue",
		"RedrivePolicy":  `not-json`,
		"CreatedTimestamp": "1700000000",
	}

	q := parseQueueAttributes("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue", attrs)

	assert.Nil(t, q.RedrivePolicy)
}

func TestParseQueueAttributes_MissingFields(t *testing.T) {
	q := parseQueueAttributes("https://sqs.us-east-1.amazonaws.com/123456789012/empty-queue", map[string]string{})

	assert.Equal(t, "empty-queue", q.Name)
	assert.Equal(t, "Standard", q.Type)
	assert.Equal(t, 0, q.ApproximateMessageCount)
	assert.True(t, q.CreatedTimestamp.IsZero())
}

func TestQueueNameFromURL(t *testing.T) {
	tests := []struct {
		url  string
		name string
	}{
		{"https://sqs.us-east-1.amazonaws.com/123456789012/my-queue", "my-queue"},
		{"https://sqs.us-east-1.amazonaws.com/123456789012/payments.fifo", "payments.fifo"},
		{"http://localhost:4566/000000000000/test-queue", "test-queue"},
		{"simple-name", "simple-name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, QueueNameFromURL(tt.url))
		})
	}
}

func TestParseEpochSeconds(t *testing.T) {
	assert.True(t, parseEpochSeconds("0").IsZero())
	assert.True(t, parseEpochSeconds("").IsZero())
	assert.Equal(t, time.Unix(1700000000, 0), parseEpochSeconds("1700000000"))
}
