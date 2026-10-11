package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSQS is an httptest SQS endpoint. It answers by X-Amz-Target with a
// canned JSON body per operation and records each request body.
type fakeSQS struct {
	mu        sync.Mutex
	responses map[string]fakeResponse
	requests  map[string][]map[string]any
}

type fakeResponse struct {
	status int
	body   string
}

func newFakeSQS(t *testing.T, responses map[string]fakeResponse) (*fakeSQS, *Client) {
	t.Helper()
	f := &fakeSQS{responses: responses, requests: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	// Keep the developer's AWS setup out of the test.
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")

	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, c
}

func (f *fakeSQS) serve(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSQS.")
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	f.mu.Lock()
	f.requests[op] = append(f.requests[op], body)
	resp, ok := f.responses[op]
	f.mu.Unlock()

	if !ok {
		resp = fakeResponse{status: http.StatusBadRequest, body: `{"__type":"UnsupportedOperation","message":"` + op + `"}`}
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	if resp.status == 0 {
		resp.status = http.StatusOK
	}
	w.WriteHeader(resp.status)
	_, _ = io.WriteString(w, resp.body)
}

func (f *fakeSQS) calls(op string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[op]
}

const twoMessages = `{"Messages":[
	{"MessageId":"m1","ReceiptHandle":"h1","Body":"a","Attributes":{"ApproximateReceiveCount":"1"}},
	{"MessageId":"m2","ReceiptHandle":"h2","Body":"b","Attributes":{"ApproximateReceiveCount":"3"}}
]}`

// A peek must make every received message visible again at once.
func TestReceiveMessagesResetsVisibility(t *testing.T) {
	f, c := newFakeSQS(t, map[string]fakeResponse{
		"ReceiveMessage":               {body: twoMessages},
		"ChangeMessageVisibilityBatch": {body: `{"Successful":[{"Id":"0"},{"Id":"1"}],"Failed":[]}`},
	})

	msgs, err := NewSQSService(c).ReceiveMessages(context.Background(), "http://q/test", 10)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, 3, msgs[1].ApproximateReceiveCount)

	// The receive itself cannot avoid the queue default, so it must not
	// pretend to: no VisibilityTimeout field is sent.
	recv := f.calls("ReceiveMessage")
	require.Len(t, recv, 1)
	assert.NotContains(t, recv[0], "VisibilityTimeout")

	batch := f.calls("ChangeMessageVisibilityBatch")
	require.Len(t, batch, 1, "visibility must be reset in one batch call")
	entries, ok := batch[0]["Entries"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 2)
	for i, want := range []string{"h1", "h2"} {
		e := entries[i].(map[string]any)
		assert.Equal(t, want, e["ReceiptHandle"])
		assert.EqualValues(t, 0, e["VisibilityTimeout"], "VisibilityTimeout 0 must be sent explicitly")
	}
}

func TestReceiveMessagesEmptyQueueSkipsReset(t *testing.T) {
	f, c := newFakeSQS(t, map[string]fakeResponse{
		"ReceiveMessage": {body: `{}`},
	})

	msgs, err := NewSQSService(c).ReceiveMessages(context.Background(), "http://q/test", 10)
	require.NoError(t, err)
	assert.Empty(t, msgs)
	assert.Empty(t, f.calls("ChangeMessageVisibilityBatch"))
}

// When the reset fails, the messages are still returned, with an error that
// says how many stay hidden.
func TestReceiveMessagesResetFailure(t *testing.T) {
	tests := []struct {
		name       string
		batch      fakeResponse
		wantFailed int
	}{
		{
			name:       "per-entry failure",
			batch:      fakeResponse{body: `{"Successful":[{"Id":"0"}],"Failed":[{"Id":"1","Code":"ReceiptHandleIsInvalid","Message":"bad handle","SenderFault":true}]}`},
			wantFailed: 1,
		},
		{
			name:       "whole call denied",
			batch:      fakeResponse{status: http.StatusBadRequest, body: `{"__type":"com.amazon.coral.service#AccessDeniedException","message":"denied"}`},
			wantFailed: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newFakeSQS(t, map[string]fakeResponse{
				"ReceiveMessage":               {body: twoMessages},
				"ChangeMessageVisibilityBatch": tt.batch,
			})

			msgs, err := NewSQSService(c).ReceiveMessages(context.Background(), "http://q/test", 10)
			assert.Len(t, msgs, 2, "messages are returned even when the reset fails")
			var vre *VisibilityResetError
			require.True(t, errors.As(err, &vre), "expected VisibilityResetError, got %v", err)
			assert.Equal(t, tt.wantFailed, vre.Failed)
		})
	}
}
