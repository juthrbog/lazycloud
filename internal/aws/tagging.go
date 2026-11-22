package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/schemas"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/transport/http/protocol/awsjson"
)

// TaggingService defines the Resource Groups Tagging API calls that the tag
// search of the jump uses (docs/design/jump.md, "Tag search (PR 6)"). Every
// call reads.
type TaggingService interface {
	// GetResourcesPage returns one page of the tagged resources of the
	// region that match every filter. A filter with no values matches any
	// value of its key.
	GetResourcesPage(ctx context.Context, filters []TagFilter, token *string) (*TaggedPage, error)
}

// TagFilter is one key of a tag search, with the values that match. The
// filters of a search are ANDed, and the values of a filter are ORed.
type TagFilter struct {
	Key    string
	Values []string
}

// TaggedResource is one resource of a tag search: its ARN and its tags. The
// API gives no name, type or state.
type TaggedResource struct {
	ARN  string
	Tags map[string]string
}

// Service returns the service of the ARN, for example "lambda".
func (r TaggedResource) Service() string {
	a, err := arn.Parse(r.ARN)
	if err != nil {
		return ""
	}
	return a.Service
}

// Region returns the region of the ARN, or "" for a global resource such
// as an S3 bucket.
func (r TaggedResource) Region() string {
	a, err := arn.Parse(r.ARN)
	if err != nil {
		return ""
	}
	return a.Region
}

// Type returns the resource type of the ARN, for example "function" or
// "instance": the part of the resource before the first "/" or ":". An S3
// bucket ARN has none, so it is "bucket".
func (r TaggedResource) Type() string {
	a, err := arn.Parse(r.ARN)
	if err != nil {
		return ""
	}
	if i := strings.IndexAny(a.Resource, "/:"); i >= 0 {
		return a.Resource[:i]
	}
	if a.Service == "s3" {
		return "bucket"
	}
	return ""
}

// Name returns the last part of the ARN: the name or ID of the resource.
// A log group ARN can end with ":*", which is not part of the name.
func (r TaggedResource) Name() string {
	a, err := arn.Parse(r.ARN)
	if err != nil {
		return r.ARN
	}
	res := strings.TrimSuffix(a.Resource, ":*")
	if i := strings.LastIndexAny(res, "/:"); i >= 0 {
		return res[i+1:]
	}
	return res
}

// TagText returns the tags as "key=value" pairs in key order.
func (r TaggedResource) TagText() string {
	keys := make([]string, 0, len(r.Tags))
	for k := range r.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + r.Tags[k]
	}
	return strings.Join(parts, " ")
}

// TaggedPage is one page of a tag search.
type TaggedPage struct {
	Resources []TaggedResource
	Next      *string
}

// ErrTagSearchDenied is returned when the identity may not call
// tag:GetResources.
var ErrTagSearchDenied = errors.New("tag:GetResources is not allowed for this identity")

// taggingPage is the number of resources that a page asks for (the API
// allows 1 to 100).
const taggingPage = 100

// TaggingServiceImpl is the real AWS-backed implementation of
// TaggingService.
type TaggingServiceImpl struct {
	client *Client
}

// NewTaggingService creates a real Tagging service backed by client.
func NewTaggingService(client *Client) *TaggingServiceImpl {
	return &TaggingServiceImpl{client: client}
}

var _ TaggingService = (*TaggingServiceImpl)(nil)

// TaggingClient returns a Resource Groups Tagging API client. The module
// defaults to the Smithy RPC v2 CBOR protocol since v1.42.0, which
// LocalStack rejects. The client sets JSON 1.1, as ECS and Logs use, and a
// test pins it, because Dependabot bumps the AWS modules as a group (H9).
func (c *Client) TaggingClient() *resourcegroupstaggingapi.Client {
	return resourcegroupstaggingapi.NewFromConfig(c.Config, func(o *resourcegroupstaggingapi.Options) {
		o.Protocol = awsjson.New11(schemas.ResourceGroupsTaggingAPI_20170126)
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
}

// GetResourcesPage returns one page of the tagged resources of the region.
func (svc *TaggingServiceImpl) GetResourcesPage(ctx context.Context, filters []TagFilter, token *string) (*TaggedPage, error) {
	in := &resourcegroupstaggingapi.GetResourcesInput{
		PaginationToken:  token,
		ResourcesPerPage: aws.Int32(taggingPage),
	}
	for _, f := range filters {
		// A key with no values matches any value. An empty list, not a
		// missing one: LocalStack fails with an InternalError when Values is
		// left out, and AWS treats both as any value.
		values := f.Values
		if values == nil {
			values = []string{}
		}
		in.TagFilters = append(in.TagFilters, tagtypes.TagFilter{Key: aws.String(f.Key), Values: values})
	}
	out, err := svc.client.TaggingClient().GetResources(ctx, in)
	if err != nil {
		if apiErr, ok := errors.AsType[smithy.APIError](err); ok && strings.HasPrefix(apiErr.ErrorCode(), "AccessDenied") {
			return nil, fmt.Errorf("%w: %w", ErrTagSearchDenied, err)
		}
		return nil, fmt.Errorf("GetResources: %w", err)
	}
	page := &TaggedPage{Next: nonEmpty(out.PaginationToken)}
	for _, m := range out.ResourceTagMappingList {
		r := TaggedResource{ARN: aws.ToString(m.ResourceARN), Tags: map[string]string{}}
		for _, t := range m.Tags {
			r.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
		page.Resources = append(page.Resources, r)
	}
	return page, nil
}
