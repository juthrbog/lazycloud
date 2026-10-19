package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSQSResults answers the SQS JSON protocol for the calls that these tests use.
type fakeSQSResults struct {
	mu sync.Mutex
	// deniedQueue makes GetQueueAttributes fail for this queue URL.
	deniedQueue string
	// staleHandles makes DeleteMessageBatch report these handles as failed.
	staleHandles map[string]bool
	// failBatch makes the DeleteMessageBatch call with this number fail
	// completely (1 is the first call).
	failBatch  int
	batchCalls int
	batchSizes []int
}

func newFakeSQSResults(t *testing.T) (*fakeSQSResults, *SQSServiceImpl) {
	t.Helper()
	f := &fakeSQSResults{staleHandles: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewSQSService(c)
}

func writeSQSResultsError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("X-Amzn-ErrorType", code)
	w.Header().Set("X-Amzn-Query-Error", code+";Sender")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"__type": "com.amazonaws.sqs#" + code, "message": message})
}

func (f *fakeSQSResults) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSQS.")
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	switch op {
	case "GetQueueAttributes":
		url, _ := body["QueueUrl"].(string)
		if url == f.deniedQueue {
			writeSQSResultsError(w, "AccessDenied", "not allowed")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Attributes": map[string]string{
			"QueueArn":                    "arn:aws:sqs:us-east-1:123:" + QueueNameFromURL(url),
			"ApproximateNumberOfMessages": "7",
		}})
	case "DeleteMessageBatch":
		f.batchCalls++
		entries, _ := body["Entries"].([]any)
		f.batchSizes = append(f.batchSizes, len(entries))
		if f.batchCalls == f.failBatch {
			writeSQSResultsError(w, "ServiceUnavailable", "try again")
			return
		}
		var ok, failed []map[string]any
		for _, e := range entries {
			entry := e.(map[string]any)
			if f.staleHandles[entry["ReceiptHandle"].(string)] {
				failed = append(failed, map[string]any{
					"Id": entry["Id"], "Code": "ReceiptHandleIsInvalid",
					"Message": "The receipt handle has expired.", "SenderFault": true,
				})
				continue
			}
			ok = append(ok, map[string]any{"Id": entry["Id"]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Successful": ok, "Failed": failed})
	default:
		writeSQSResultsError(w, "InvalidAction", "unexpected call "+op)
	}
}

func testHandles(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("h%02d", i)
	}
	return out
}

// M22: one queue that cannot be read must not look like an empty queue.
func TestFetchAllQueueAttributesKeepsErrorPerQueue(t *testing.T) {
	f, svc := newFakeSQSResults(t)
	f.deniedQueue = "https://sqs.example/123/secret"

	queues, err := svc.FetchAllQueueAttributes(context.Background(), []string{
		"https://sqs.example/123/orders", "https://sqs.example/123/secret",
	})
	require.NoError(t, err)
	require.Len(t, queues, 2)
	require.NoError(t, queues[0].Err)
	assert.Equal(t, 7, queues[0].ApproximateMessageCount)

	assert.Equal(t, "secret", queues[1].Name)
	require.Error(t, queues[1].Err)
	assert.Equal(t, "AccessDenied", ErrorCode(queues[1].Err))
}

// M5: the delete reports each handle that SQS did not delete, also across
// several calls of 10.
func TestDeleteMessageBatchReportsFailedHandles(t *testing.T) {
	f, svc := newFakeSQSResults(t)
	f.staleHandles["h03"] = true
	f.staleHandles["h21"] = true

	err := svc.DeleteMessageBatch(context.Background(), "https://sqs.example/123/orders", testHandles(25))
	var batchErr *BatchDeleteError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, []int{10, 10, 5}, f.batchSizes)
	require.Len(t, batchErr.Failed, 2)
	assert.Equal(t, "h03", batchErr.Failed[0].ReceiptHandle)
	assert.Equal(t, "h21", batchErr.Failed[1].ReceiptHandle, "the entry ID maps back across calls")
	assert.Equal(t, "ReceiptHandleIsInvalid", batchErr.Failed[1].Code)
}

// A call that fails completely marks its 10 handles as failed. The other
// calls still run.
func TestDeleteMessageBatchGoesOnAfterAFailedCall(t *testing.T) {
	f, svc := newFakeSQSResults(t)
	f.failBatch = 2

	err := svc.DeleteMessageBatch(context.Background(), "https://sqs.example/123/orders", testHandles(25))
	var batchErr *BatchDeleteError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 3, f.batchCalls, "the third call still runs")
	require.Len(t, batchErr.Failed, 10)
	assert.Equal(t, "h10", batchErr.Failed[0].ReceiptHandle)
	assert.Equal(t, "h19", batchErr.Failed[9].ReceiptHandle)
	assert.Equal(t, "ServiceUnavailable", batchErr.Failed[0].Code)
}

func TestDeleteMessageBatchAllDeleted(t *testing.T) {
	_, svc := newFakeSQSResults(t)
	assert.NoError(t, svc.DeleteMessageBatch(context.Background(), "https://sqs.example/123/orders", testHandles(3)))
}

func TestErrorCode(t *testing.T) {
	apiErr := &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}
	assert.Equal(t, "AccessDenied", ErrorCode(fmt.Errorf("DeleteQueue: %w", apiErr)))
	assert.Equal(t, "plain failure", ErrorCode(errors.New("plain failure")))
}
