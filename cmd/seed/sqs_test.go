package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueue answers the two calls of seedMessages.
type fakeQueue struct {
	attrs   map[string]string
	attrErr error
	sent    []*sqs.SendMessageInput
}

func (f *fakeQueue) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if f.attrErr != nil {
		return nil, f.attrErr
	}
	return &sqs.GetQueueAttributesOutput{Attributes: f.attrs}, nil
}

func (f *fakeQueue) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.sent = append(f.sent, in)
	return &sqs.SendMessageOutput{}, nil
}

func emptyCounts() map[string]string {
	return map[string]string{
		"ApproximateNumberOfMessages":           "0",
		"ApproximateNumberOfMessagesNotVisible": "0",
		"ApproximateNumberOfMessagesDelayed":    "0",
	}
}

func TestSeedMessagesFillsAnEmptyQueue(t *testing.T) {
	f := &fakeQueue{attrs: emptyCounts()}
	sent, err := seedMessages(context.Background(), f, queueDef{Name: "orders", SeedMessages: 3}, "url")
	require.NoError(t, err)
	assert.True(t, sent)
	assert.Len(t, f.sent, 3)
	assert.Nil(t, f.sent[0].MessageGroupId, "a standard queue has no group")
}

// L26: a second seed run doubled the message counts.
func TestSeedMessagesSkipsAQueueWithMessages(t *testing.T) {
	for _, attr := range []string{
		"ApproximateNumberOfMessages",
		"ApproximateNumberOfMessagesNotVisible",
		"ApproximateNumberOfMessagesDelayed",
	} {
		f := &fakeQueue{attrs: emptyCounts()}
		f.attrs[attr] = "2"
		sent, err := seedMessages(context.Background(), f, queueDef{Name: "orders", SeedMessages: 3}, "url")
		require.NoError(t, err)
		assert.False(t, sent, attr)
		assert.Empty(t, f.sent, attr)
	}
}

func TestSeedMessagesFIFOSetsGroupAndDedupID(t *testing.T) {
	f := &fakeQueue{attrs: emptyCounts()}
	_, err := seedMessages(context.Background(), f, queueDef{Name: "pay.fifo", FIFO: true, SeedMessages: 2}, "url")
	require.NoError(t, err)
	require.Len(t, f.sent, 2)
	assert.Equal(t, "default", aws.ToString(f.sent[1].MessageGroupId))
	assert.Equal(t, "msg-1", aws.ToString(f.sent[1].MessageDeduplicationId))
}

func TestSeedMessagesReportsAttributeErrors(t *testing.T) {
	f := &fakeQueue{attrErr: errors.New("denied")}
	_, err := seedMessages(context.Background(), f, queueDef{Name: "orders", SeedMessages: 1}, "url")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders")
	assert.Empty(t, f.sent)
}
