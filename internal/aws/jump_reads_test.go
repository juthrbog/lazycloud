package aws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reads that jump PR 3 adds (docs/design/jump.md, "The lookup").

// s3Head is a fake S3 with an endpoint that answers HEAD /BUCKET with the
// status in status, and records the expected owner of each request.
type s3Head struct {
	mu     sync.Mutex
	status map[string]int
	owners []string
}

func newS3Head(t *testing.T, status map[string]int) (*s3Head, *S3ServiceImpl) {
	t.Helper()
	h := &s3Head{status: status}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.owners = append(h.owners, r.Header.Get("X-Amz-Expected-Bucket-Owner"))
		code, ok := h.status[strings.Trim(r.URL.Path, "/")]
		h.mu.Unlock()
		if !ok {
			code = http.StatusNotFound
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	isolateConfig(t)
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return h, NewS3Service(c)
}

func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
}

func TestHeadBucket(t *testing.T) {
	h, svc := newS3Head(t, map[string]int{"logs": http.StatusOK, "theirs": http.StatusForbidden})

	require.NoError(t, svc.HeadBucket(context.Background(), "logs", "111122223333"))
	require.NoError(t, svc.HeadBucket(context.Background(), "logs", ""))
	assert.Equal(t, []string{"111122223333", ""}, h.owners, "the owner goes as ExpectedBucketOwner")

	err := svc.HeadBucket(context.Background(), "missing", "")
	require.ErrorIs(t, err, ErrBucketNotFound)
	assert.Contains(t, err.Error(), "missing")

	err = svc.HeadBucket(context.Background(), "theirs", "111122223333")
	require.ErrorIs(t, err, ErrBucketDenied)
}

// Without an endpoint, HeadBucket first finds the region of the bucket and
// then asks the client of that region.
func TestHeadBucket_UsesTheBucketRegion(t *testing.T) {
	rs, svc := newRegionService(t, func(rs *regionServer, w http.ResponseWriter, r *http.Request) {
		switch {
		case isHead(r) && len(rs.requests()) == 1:
			w.Header().Set("X-Amz-Bucket-Region", "eu-central-1")
			w.WriteHeader(http.StatusMovedPermanently)
		case isHead(r):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	require.NoError(t, svc.HeadBucket(context.Background(), "b", ""))
	reqs := rs.requests()
	require.Len(t, reqs, 2)
	assert.True(t, strings.HasSuffix(reqs[1], " eu-central-1"), reqs[1])
}

// On AWS, a missing bucket gives a 404 with no region header, and the
// region lookup falls back to GetBucketLocation, which says NoSuchBucket.
func TestHeadBucket_MissingOnAWS(t *testing.T) {
	_, svc := newRegionService(t, func(_ *regionServer, w http.ResponseWriter, r *http.Request) {
		if isHead(r) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist</Message></Error>`))
	})
	require.ErrorIs(t, svc.HeadBucket(context.Background(), "gone", ""), ErrBucketNotFound)
}

func TestGetQueueURL(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		if body["QueueName"] == "missing" {
			writeSQSResultsError(w, "QueueDoesNotExist", "The specified queue does not exist.")
			return
		}
		if body["QueueName"] == "denied" {
			writeSQSResultsError(w, "AccessDenied", "not allowed")
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_ = json.NewEncoder(w).Encode(map[string]string{"QueueUrl": "https://sqs.us-east-1.amazonaws.com/111122223333/orders"})
	}))
	t.Cleanup(srv.Close)
	isolateConfig(t)
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	svc := NewSQSService(c)

	url, err := svc.GetQueueURL(context.Background(), "orders", "111122223333")
	require.NoError(t, err)
	assert.Equal(t, "https://sqs.us-east-1.amazonaws.com/111122223333/orders", url)
	assert.Equal(t, "111122223333", got[0]["QueueOwnerAWSAccountId"])

	_, err = svc.GetQueueURL(context.Background(), "orders", "")
	require.NoError(t, err)
	_, hasOwner := got[1]["QueueOwnerAWSAccountId"]
	assert.False(t, hasOwner, "no owner, no field")

	_, err = svc.GetQueueURL(context.Background(), "missing", "")
	require.ErrorIs(t, err, ErrQueueNotFound)
	assert.Contains(t, err.Error(), "missing")

	_, err = svc.GetQueueURL(context.Background(), "denied", "")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrQueueNotFound)
}

func TestDescribeCluster(t *testing.T) {
	f, svc := newFakeECS(t)
	arn := "arn:aws:ecs:us-east-1:111122223333:cluster/prod"
	f.reply("DescribeClusters", `{"clusters":[{"clusterArn":"`+arn+`","clusterName":"prod","status":"ACTIVE",
		"tags":[{"key":"team","value":"orders"}]}]}`)
	got, err := svc.DescribeCluster(context.Background(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", got.Name)
	assert.Equal(t, arn, got.ARN)
	assert.Equal(t, map[string]string{"team": "orders"}, got.Tags)
	in := f.last(t, "DescribeClusters")
	assert.Equal(t, []string{"prod"}, strs(in["clusters"]))
	assert.ElementsMatch(t, []string{"TAGS", "SETTINGS", "CONFIGURATIONS"}, strs(in["include"]))

	f.reply("DescribeClusters", `{"clusters":[],"failures":[{"arn":"`+arn+`","reason":"MISSING"}]}`)
	_, err = svc.DescribeCluster(context.Background(), arn)
	require.ErrorIs(t, err, ErrClusterNotFound)
	assert.Contains(t, err.Error(), "prod")

	f.reply("DescribeClusters", `{"clusters":[{"clusterArn":"`+arn+`","clusterName":"prod","status":"INACTIVE"}]}`)
	_, err = svc.DescribeCluster(context.Background(), arn)
	require.ErrorIs(t, err, ErrClusterNotFound, "a deleted cluster counts as missing")

	f.reply("DescribeClusters", `{"clusters":[],"failures":[{"arn":"`+arn+`","reason":"ACCESS_DENIED"}]}`)
	_, err = svc.DescribeCluster(context.Background(), arn)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrClusterNotFound)
	assert.Contains(t, err.Error(), "ACCESS_DENIED")
}
