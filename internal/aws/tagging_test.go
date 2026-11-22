package aws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTagging answers GetResources over JSON 1.1 and records each request.
type fakeTagging struct {
	mu      sync.Mutex
	targets []string
	types   []string
	bodies  []map[string]any
	reply   string
	status  int
}

func newFakeTagging(t *testing.T) (*fakeTagging, *TaggingServiceImpl) {
	t.Helper()
	f := &fakeTagging{reply: `{"ResourceTagMappingList":[]}`, status: http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.targets = append(f.targets, r.Header.Get("X-Amz-Target"))
		f.types = append(f.types, r.Header.Get("Content-Type"))
		f.bodies = append(f.bodies, body)
		reply, status := f.reply, f.status
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	isolateConfig(t)
	c, err := NewClient("", "us-east-1", srv.URL)
	require.NoError(t, err)
	return f, NewTaggingService(c)
}

// H9, pinned in PR 6: the Tagging API module defaults to the Smithy RPC v2
// CBOR protocol since v1.42.0, and LocalStack rejects CBOR. The client must
// send JSON 1.1. A Dependabot bump that changes the protocol fails here.
func TestJumpTaggingClientSpeaksJSON11(t *testing.T) {
	f, svc := newFakeTagging(t)
	_, err := svc.GetResourcesPage(context.Background(), nil, nil)
	require.NoError(t, err)
	require.Len(t, f.targets, 1)
	assert.Equal(t, "ResourceGroupsTaggingAPI_20170126.GetResources", f.targets[0])
	assert.Equal(t, "application/x-amz-json-1.1", f.types[0])
}

func TestGetResourcesPage(t *testing.T) {
	f, svc := newFakeTagging(t)
	f.reply = `{"PaginationToken":"p2","ResourceTagMappingList":[
		{"ResourceARN":"arn:aws:lambda:us-east-1:1:function:orders-api","Tags":[{"Key":"team","Value":"orders"},{"Key":"env","Value":"dev"}]},
		{"ResourceARN":"arn:aws:s3:::logs","Tags":[]}]}`
	page, err := svc.GetResourcesPage(context.Background(),
		[]TagFilter{{Key: "team", Values: []string{"orders"}}, {Key: "env"}}, new("p1"))
	require.NoError(t, err)
	require.Len(t, page.Resources, 2)
	assert.Equal(t, "arn:aws:lambda:us-east-1:1:function:orders-api", page.Resources[0].ARN)
	assert.Equal(t, map[string]string{"team": "orders", "env": "dev"}, page.Resources[0].Tags)
	assert.Equal(t, "p2", *page.Next)

	body := f.bodies[0]
	assert.Equal(t, "p1", body["PaginationToken"])
	assert.EqualValues(t, 100, body["ResourcesPerPage"])
	assert.Equal(t, []any{
		map[string]any{"Key": "team", "Values": []any{"orders"}},
		map[string]any{"Key": "env", "Values": []any{}},
	}, body["TagFilters"], "a key alone sends an empty list, which LocalStack needs")

	// An empty token at the end is no next page.
	f.reply = `{"PaginationToken":"","ResourceTagMappingList":[]}`
	page, err = svc.GetResourcesPage(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Nil(t, page.Next)
}

func TestGetResourcesPageDenied(t *testing.T) {
	f, svc := newFakeTagging(t)
	f.status = http.StatusBadRequest
	f.reply = `{"__type":"AccessDeniedException","message":"not allowed"}`
	_, err := svc.GetResourcesPage(context.Background(), nil, nil)
	require.ErrorIs(t, err, ErrTagSearchDenied)
	assert.Equal(t, "tag:GetResources is not allowed for this identity", ErrorText(err), "the message names the action")

	f.reply = `{"__type":"ThrottledException","message":"slow down"}`
	_, err = svc.GetResourcesPage(context.Background(), nil, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrTagSearchDenied)
}

func TestTaggedResourceParts(t *testing.T) {
	for _, tc := range []struct {
		arn, service, region, typ, name string
	}{
		{"arn:aws:lambda:us-east-1:1:function:orders-api", "lambda", "us-east-1", "function", "orders-api"},
		{"arn:aws:ec2:eu-west-1:1:instance/i-0abc", "ec2", "eu-west-1", "instance", "i-0abc"},
		{"arn:aws:ecs:us-east-1:1:service/prod/web", "ecs", "us-east-1", "service", "web"},
		{"arn:aws:logs:us-east-1:1:log-group:/aws/lambda/x:*", "logs", "us-east-1", "log-group", "x"},
		{"arn:aws:s3:::logs", "s3", "", "bucket", "logs"},
		{"arn:aws:sqs:us-east-1:1:orders", "sqs", "us-east-1", "", "orders"},
		{"not an arn", "", "", "", "not an arn"},
	} {
		r := TaggedResource{ARN: tc.arn}
		assert.Equal(t, tc.service, r.Service(), tc.arn)
		assert.Equal(t, tc.region, r.Region(), tc.arn)
		assert.Equal(t, tc.typ, r.Type(), tc.arn)
		assert.Equal(t, tc.name, r.Name(), tc.arn)
	}
	assert.Equal(t, "a=1 b=2", TaggedResource{Tags: map[string]string{"b": "2", "a": "1"}}.TagText())
}
