package aws

import (
	"context"

	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHookClient starts an httptest server that runs handle for each request
// and returns a client for it. The handler must return when the request
// context ends, or the server cannot close.
func newHookClient(t *testing.T, handle http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices that the client went away only after the
		// handler has read the body, so a stalled handler can end.
		_, _ = io.Copy(io.Discard, r.Body)
		handle(w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return c
}

// stall blocks until the request ends.
func stall(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

// setVar sets a package variable for one test.
func setVar[T any](t *testing.T, v *T, val T) {
	t.Helper()
	old := *v
	*v = val
	t.Cleanup(func() { *v = old })
}

func TestHTTPClientSettings(t *testing.T) {
	c := newHTTPClient(60 * time.Second)
	assert.Equal(t, 30*time.Second, c.GetTransport().IdleConnTimeout,
		"a pooled connection must leave a new request at least half the read timeout")
	assert.Equal(t, dialTimeout, c.GetDialer().Timeout)
	assert.Equal(t, time.Duration(0), c.GetTimeout(), "no limit on the whole request: it would stop long downloads")
}

func TestReadTimeoutStopsAStalledCall(t *testing.T) {
	setVar(t, &readTimeout, 100*time.Millisecond)
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	c := newHookClient(t, stall)

	start := time.Now()
	_, err := NewS3Service(c).ListBuckets(t.Context())
	require.Error(t, err)
	var rte *awshttp.ResponseTimeoutError
	require.ErrorAs(t, err, &rte, "a silent server fails with the read timeout")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestContextDeadlineStopsAStalledCall(t *testing.T) {
	c := newHookClient(t, stall)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := NewEC2Service(c).ListInstancesPage(ctx, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// counter counts requests whose body or URL contains a marker.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) add() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func TestDeleteObjectsStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls counter
	c := newHookClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
			calls.add()
			cancel() // the user cancels while the first batch runs
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	keys := make([]string, 2500) // three batches
	for i := range keys {
		keys[i] = fmt.Sprintf("k%04d", i)
	}
	err := NewS3Service(c).DeleteObjects(ctx, "b", keys)

	require.ErrorIs(t, err, context.Canceled)
	var de *DeleteObjectsError
	require.ErrorAs(t, err, &de)
	assert.Len(t, de.Failed, 2500, "every key counts as not deleted")
	assert.Equal(t, "Canceled", de.Failed[2499].Code)
	assert.Equal(t, 1, calls.get(), "no call after the cancel")
}

func TestDeleteObjectsPartialFailureIsNotACancel(t *testing.T) {
	f, svc := newFakeS3(t)
	f.deleteFail["x"] = true
	err := svc.DeleteObjects(t.Context(), "b", []string{"x", "y"})
	var de *DeleteObjectsError
	require.ErrorAs(t, err, &de)
	require.NotErrorIs(t, err, context.Canceled)
	assert.NoError(t, de.Unwrap())
}

func TestGetBucketPropertiesReportsACancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newHookClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("versioning") {
			cancel()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	props, err := NewS3Service(c).GetBucketProperties(ctx, "b")
	require.ErrorIs(t, err, context.Canceled, "empty fields must not look like the bucket's settings")
	assert.Nil(t, props)
}

// sqsTarget returns the SQS operation of a request.
func sqsTarget(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSQS.")
}

func TestFetchAllQueueAttributesReportsACancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newHookClient(t, func(_ http.ResponseWriter, r *http.Request) {
		if sqsTarget(r) == "GetQueueAttributes" {
			cancel()
		}
		<-r.Context().Done()
	})

	urls := []string{"https://sqs/1/a", "https://sqs/1/b", "https://sqs/1/c"}
	queues, err := NewSQSService(c).FetchAllQueueAttributes(ctx, urls)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, queues)
}

func TestDeleteMessageBatchStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls counter
	c := newHookClient(t, func(_ http.ResponseWriter, r *http.Request) {
		if sqsTarget(r) == "DeleteMessageBatch" {
			calls.add()
			cancel()
		}
		<-r.Context().Done()
	})

	handles := make([]string, 25) // three calls of up to 10
	for i := range handles {
		handles[i] = fmt.Sprintf("h%d", i)
	}
	err := NewSQSService(c).DeleteMessageBatch(ctx, "https://sqs/1/q", handles)

	require.ErrorIs(t, err, context.Canceled)
	var be *BatchDeleteError
	require.ErrorAs(t, err, &be)
	assert.Len(t, be.Failed, 25)
	assert.Equal(t, 1, calls.get())
}

func TestMultipartAbortHasADeadline(t *testing.T) {
	setVar(t, &abortTimeout, 100*time.Millisecond)
	setVar(t, &multipartCopyThreshold, 10)
	setVar(t, &copyPartSize, 5)
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	var aborts counter
	c := newHookClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/src"):
			w.Header().Set("Content-Length", "12")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && q.Has("uploads"):
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>u1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && q.Has("partNumber"):
			writeS3Error(w, http.StatusInternalServerError, "InternalError")
		case r.Method == http.MethodDelete && q.Has("uploadId"):
			aborts.add()
			<-r.Context().Done() // the abort call hangs
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	start := time.Now()
	err := NewS3Service(c).CopyObject(t.Context(), "b", "src", "b", "dst", false)
	require.Error(t, err)
	assert.Equal(t, 1, aborts.get())
	assert.Less(t, time.Since(start), 5*time.Second, "a hanging abort must not block the copy forever")
}
