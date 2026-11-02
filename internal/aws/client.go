package aws

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// FallbackRegion is used only when neither the caller, the environment, nor the
// shared config profile specifies a region.
const FallbackRegion = "us-east-1"

// Network timeouts of every AWS call. They are variables so that tests can
// make them short.
//
// There is no limit on the whole request, because that would also stop a
// long download that is still receiving data. readTimeout is a limit on
// silence instead: a read that gets no byte in that time fails, so a
// stalled connection cannot keep a spinner running forever. Each call also
// gets a context deadline from the view that makes it.
var (
	dialTimeout = 10 * time.Second
	readTimeout = 60 * time.Second
	// copyReadTimeout replaces readTimeout for S3 CopyObject, UploadPartCopy
	// and CompleteMultipartUpload. S3 can work for minutes on a large copy
	// before it answers, and AWS documents keep-alive bytes only for
	// CompleteMultipartUpload.
	copyReadTimeout = 10 * time.Minute
	// abortTimeout limits the clean-up call after a failed multipart copy.
	abortTimeout = 30 * time.Second
	// visibilityResetTimeout limits the call that makes peeked SQS messages
	// visible again. It runs even when the peek's own context has ended.
	visibilityResetTimeout = 10 * time.Second
)

// newHTTPClient returns an HTTP client for AWS calls with the given read
// timeout. A connection stays idle in the pool for at most half the read
// timeout, because the read deadline of a pooled connection starts while it
// is idle: a connection that was idle for longer would leave a new request
// less time than the read timeout for its first byte.
func newHTTPClient(read time.Duration) *awshttp.BuildableClient {
	return awshttp.NewBuildableClient().
		WithDialerOptions(func(d *net.Dialer) { d.Timeout = dialTimeout }).
		WithTransportOptions(func(t *http.Transport) { t.IdleConnTimeout = read / 2 }).
		WithReadTimeout(read)
}

// Client holds shared AWS configuration.
type Client struct {
	Config  aws.Config
	Profile string
	// Region is the effective region after SDK resolution, never empty.
	Region   string
	Endpoint string // empty = real AWS, set = LocalStack

	copyHTTP *awshttp.BuildableClient // see copyReadTimeout
}

// NewClient creates a client using the specified profile, region, and optional endpoint override.
// An empty region lets the SDK resolve it (env vars, then the profile in ~/.aws/config).
func NewClient(profile, region, endpoint string) (*Client, error) {
	// The retry count stays at the SDK default (3 attempts) unless the user
	// sets max_attempts in the profile or AWS_MAX_ATTEMPTS. Setting it here
	// would override the user's choice.
	opts := []func(*config.LoadOptions) error{
		config.WithHTTPClient(newHTTPClient(readTimeout)),
	}

	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	// For LocalStack, use dummy credentials
	if endpoint != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		cfg.Region = FallbackRegion
	}

	return &Client{
		Config:   cfg,
		Profile:  profile,
		Region:   cfg.Region,
		Endpoint: endpoint,
		copyHTTP: newHTTPClient(copyReadTimeout),
	}, nil
}

// SQSClient returns an SQS service client configured for the current profile/region/endpoint.
func (c *Client) SQSClient() *sqs.Client {
	return sqs.NewFromConfig(c.Config, func(o *sqs.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
}

// ServiceEndpoint returns the endpoint override for service client constructors.
// Returns nil when targeting real AWS.
func (c *Client) ServiceEndpoint() *string {
	if c.Endpoint == "" {
		return nil
	}
	return aws.String(c.Endpoint)
}
