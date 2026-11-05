package aws

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M4: the bucket region lookup was slow and incomplete.

// regionServer is a fake S3 that records each request with the region of
// its signature.
type regionServer struct {
	mu   sync.Mutex
	reqs []string // for example "HEAD /b us-east-1"
}

var scopeRegion = regexp.MustCompile(`Credential=[^/]+/\d+/([^/]+)/s3/`)

func (rs *regionServer) record(r *http.Request) {
	region := ""
	if m := scopeRegion.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
		region = m[1]
	}
	q := ""
	if r.URL.RawQuery != "" {
		q = "?" + strings.SplitN(r.URL.RawQuery, "&", 2)[0]
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.reqs = append(rs.reqs, fmt.Sprintf("%s %s%s %s", r.Method, r.URL.Path, q, region))
}

func (rs *regionServer) requests() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.reqs...)
}

// newRegionService returns an S3 service that has no endpoint override, so
// it looks up bucket regions as it does against AWS. AWS_ENDPOINT_URL
// sends its calls to handle.
func newRegionService(t *testing.T, handle func(rs *regionServer, w http.ResponseWriter, r *http.Request)) (*regionServer, *S3ServiceImpl) {
	t.Helper()
	rs := &regionServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.record(r)
		handle(rs, w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)
	t.Setenv("AWS_S3_US_EAST_1_REGIONAL_ENDPOINT", "")
	c, err := NewClient("", "us-east-1", "")
	require.NoError(t, err)
	require.Empty(t, c.Endpoint, "the lookup runs only without an endpoint override")
	return rs, NewS3Service(c)
}

const emptyListing = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult><Name>b</Name><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated></ListBucketResult>`

func isHead(r *http.Request) bool { return r.Method == http.MethodHead }

// The region of a bucket is looked up once, and later calls go to that
// region without a lookup.
func TestBucketRegionIsLookedUpOnce(t *testing.T) {
	rs, svc := newRegionService(t, func(_ *regionServer, w http.ResponseWriter, r *http.Request) {
		if isHead(r) {
			// S3 answers a HeadBucket in the wrong region with a redirect
			// that names the region of the bucket.
			w.Header().Set("X-Amz-Bucket-Region", "eu-central-1")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(emptyListing))
	})

	for range 3 {
		_, err := svc.ListObjectsPage(t.Context(), "b", "", nil)
		require.NoError(t, err)
	}
	reqs := rs.requests()
	heads := 0
	for _, r := range reqs {
		if strings.HasPrefix(r, "HEAD") {
			heads++
			continue
		}
		assert.True(t, strings.HasSuffix(r, " eu-central-1"), "signed for the bucket region: %s", r)
	}
	assert.Equal(t, 1, heads, "one lookup for three pages: %v", reqs)
}

// ListBuckets reads every page and fills the cache with the region of each
// bucket, so no lookup follows.
func TestListBucketsPagesAndCachesRegions(t *testing.T) {
	setVar(t, &listBucketsPageSize, 2)
	rs, svc := newRegionService(t, func(_ *regionServer, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.URL.Path == "/" && r.URL.Query().Get("continuation-token") == "":
			assert.Equal(t, "2", r.URL.Query().Get("max-buckets"))
			_, _ = w.Write([]byte(`<ListAllMyBucketsResult><Buckets>
<Bucket><Name>a</Name><BucketRegion>us-west-2</BucketRegion></Bucket>
<Bucket><Name>b</Name><BucketRegion>ap-south-1</BucketRegion></Bucket>
</Buckets><ContinuationToken>next</ContinuationToken></ListAllMyBucketsResult>`))
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`<ListAllMyBucketsResult><Buckets>
<Bucket><Name>c</Name><BucketRegion>eu-west-1</BucketRegion></Bucket>
</Buckets></ListAllMyBucketsResult>`))
		default:
			_, _ = w.Write([]byte(emptyListing))
		}
	})

	buckets, err := svc.ListBuckets(t.Context())
	require.NoError(t, err)
	require.Len(t, buckets, 3, "every page")
	assert.Equal(t, "c", buckets[2].Name)

	for bucket, want := range map[string]string{"a": "us-west-2", "b": "ap-south-1", "c": "eu-west-1"} {
		got, err := svc.GetBucketRegion(t.Context(), bucket)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err = svc.ListObjectsPage(t.Context(), "b", "", nil)
	require.NoError(t, err)
	for _, r := range rs.requests() {
		assert.False(t, strings.HasPrefix(r, "HEAD"), "no lookup after ListBuckets: %s", r)
	}
	last := rs.requests()[len(rs.requests())-1]
	assert.True(t, strings.HasPrefix(last, "GET /b?") && strings.HasSuffix(last, " ap-south-1"),
		"the listing goes to the region from ListBuckets: %s", last)
}

// When HeadBucket gives no region, GetBucketLocation is the last resort.
// Its old value "EU" means eu-west-1.
func TestBucketRegionFallbackMapsEU(t *testing.T) {
	_, svc := newRegionService(t, func(_ *regionServer, w http.ResponseWriter, r *http.Request) {
		if isHead(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">EU</LocationConstraint>`))
	})
	region, err := svc.GetBucketRegion(t.Context(), "old")
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", region)
}

// A bucket that does not exist reports the error of GetBucketLocation,
// which names the problem, and nothing is cached.
func TestBucketRegionOfAMissingBucket(t *testing.T) {
	rs, svc := newRegionService(t, func(_ *regionServer, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		if !isHead(r) {
			_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist</Message></Error>`))
		}
	})
	_, err := svc.GetBucketRegion(t.Context(), "gone")
	require.Error(t, err)
	assert.Equal(t, "NoSuchBucket", ErrorCode(err))
	_, err = svc.GetBucketRegion(t.Context(), "gone")
	require.Error(t, err)
	assert.Len(t, rs.requests(), 4, "an error is not cached")
}

// Creating a bucket caches its region, and deleting it forgets the region.
func TestCreateAndDeleteUpdateTheRegionCache(t *testing.T) {
	rs, svc := newRegionService(t, func(_ *regionServer, w http.ResponseWriter, r *http.Request) {
		if isHead(r) {
			w.Header().Set("X-Amz-Bucket-Region", "us-east-1")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, svc.CreateBucket(t.Context(), "fresh", "ap-southeast-2"))
	region, err := svc.GetBucketRegion(t.Context(), "fresh")
	require.NoError(t, err)
	assert.Equal(t, "ap-southeast-2", region, "known from the create")

	require.NoError(t, svc.DeleteBucket(t.Context(), "fresh"))
	before := len(rs.requests())
	region, err = svc.GetBucketRegion(t.Context(), "fresh")
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", region, "looked up again after the delete")
	assert.Len(t, rs.requests(), before+1)
}
