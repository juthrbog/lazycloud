package aws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contentService returns an S3 service whose GetObject calls go to handle.
func contentService(t *testing.T, handle http.HandlerFunc) *S3ServiceImpl {
	t.Helper()
	srv := httptest.NewServer(handle)
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return NewS3Service(c)
}

// L16: S3 answers a range request on an empty object with 416
// InvalidRange. GetObjectContent gives an empty string for it.
func TestGetObjectContentOfAnEmptyObject(t *testing.T) {
	var gotRange string
	svc := contentService(t, func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		writeS3Error(w, http.StatusRequestedRangeNotSatisfiable, "InvalidRange")
	})

	content, err := svc.GetObjectContent(context.Background(), "b", "empty.txt", 1<<20)

	require.NoError(t, err)
	assert.Empty(t, content)
	assert.Equal(t, "bytes=0-1048575", gotRange)
}

// Other errors still reach the caller.
func TestGetObjectContentKeepsOtherErrors(t *testing.T) {
	svc := contentService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeS3Error(w, http.StatusForbidden, "AccessDenied")
	})

	_, err := svc.GetObjectContent(context.Background(), "b", "secret.txt", 1<<20)

	require.Error(t, err)
	assert.Equal(t, "AccessDenied", ErrorCode(err))
}

// The content is read up to maxBytes.
func TestGetObjectContentReadsTheBody(t *testing.T) {
	svc := contentService(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("hello world"))
	})

	content, err := svc.GetObjectContent(context.Background(), "b", "a.txt", 5)

	require.NoError(t, err)
	assert.Equal(t, "hello", content)
}
