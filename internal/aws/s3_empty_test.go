package aws

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVersionedBucket is an in-memory S3 endpoint for one bucket, with the
// calls that EmptyAndDeleteBucket makes.
type fakeVersionedBucket struct {
	mu            sync.Mutex
	versions      []ObjectVersion
	failKey       string // DeleteObjects reports an error for this key
	lists         int
	markerLists   int // lists that sent a key marker
	deleteBatches []int
	bucketDeleted bool
}

func newFakeVersionedBucket(t *testing.T, n int) (*fakeVersionedBucket, *S3ServiceImpl) {
	t.Helper()
	f := &fakeVersionedBucket{}
	for i := range n {
		f.versions = append(f.versions, ObjectVersion{
			Key:            fmt.Sprintf("k%05d", i/2),
			VersionID:      fmt.Sprintf("v%d", i),
			IsDeleteMarker: i%2 == 1,
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewS3Service(c)
}

func (f *fakeVersionedBucket) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && q.Has("versions"):
		f.lists++
		if q.Get("key-marker") != "" {
			f.markerLists++
		}
		n := min(1000, len(f.versions))
		var b strings.Builder
		fmt.Fprintf(&b, `<ListVersionsResult><Name>b</Name><IsTruncated>%t</IsTruncated>`, n < len(f.versions))
		for _, v := range f.versions[:n] {
			tag := "Version"
			if v.IsDeleteMarker {
				tag = "DeleteMarker"
			}
			fmt.Fprintf(&b, `<%s><Key>%s</Key><VersionId>%s</VersionId></%s>`, tag, v.Key, v.VersionID, tag)
		}
		b.WriteString(`</ListVersionsResult>`)
		_, _ = w.Write([]byte(b.String()))

	case r.Method == http.MethodPost && q.Has("delete"):
		var req struct {
			Objects []struct {
				Key       string `xml:"Key"`
				VersionID string `xml:"VersionId"`
			} `xml:"Object"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.deleteBatches = append(f.deleteBatches, len(req.Objects))
		gone := map[string]bool{}
		var errs strings.Builder
		for _, o := range req.Objects {
			if o.Key == f.failKey {
				fmt.Fprintf(&errs, `<Error><Key>%s</Key><VersionId>%s</VersionId><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`, o.Key, o.VersionID)
				continue
			}
			gone[o.Key+"\x00"+o.VersionID] = true
		}
		kept := f.versions[:0]
		for _, v := range f.versions {
			if !gone[v.Key+"\x00"+v.VersionID] {
				kept = append(kept, v)
			}
		}
		f.versions = kept
		_, _ = w.Write([]byte(`<DeleteResult>` + errs.String() + `</DeleteResult>`))

	case r.Method == http.MethodDelete:
		if len(f.versions) > 0 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`<Error><Code>BucketNotEmpty</Code><Message>not empty</Message></Error>`))
			return
		}
		f.bucketDeleted = true
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusNotImplemented)
	}
}

// C4: a bucket is emptied one page at a time, never by loading every
// version into memory first.
func TestEmptyAndDeleteBucketPageByPage(t *testing.T) {
	f, svc := newFakeVersionedBucket(t, 2500)

	require.NoError(t, svc.EmptyAndDeleteBucket(context.Background(), "b"))

	assert.True(t, f.bucketDeleted)
	assert.Empty(t, f.versions)
	assert.Equal(t, []int{1000, 1000, 500}, f.deleteBatches)
	assert.Equal(t, 4, f.lists, "three pages, then an empty listing")
	assert.Zero(t, f.markerLists, "each round lists from the start")
}

func TestEmptyAndDeleteBucketStopsOnDeleteError(t *testing.T) {
	f, svc := newFakeVersionedBucket(t, 10)
	f.failKey = "k00002"

	err := svc.EmptyAndDeleteBucket(context.Background(), "b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "k00002")
	assert.False(t, f.bucketDeleted)
	assert.Len(t, f.deleteBatches, 1, "no second round after an error")
}

func TestEmptyAndDeleteBucketEmptyBucket(t *testing.T) {
	f, svc := newFakeVersionedBucket(t, 0)
	require.NoError(t, svc.EmptyAndDeleteBucket(context.Background(), "b"))
	assert.True(t, f.bucketDeleted)
	assert.Empty(t, f.deleteBatches)
}
