package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// Client holds shared AWS configuration.
type Client struct {
	Config   aws.Config
	Profile  string
	Region   string
	Endpoint string // empty = real AWS, set = LocalStack
}

// NewClient creates a client using the specified profile, region, and optional endpoint override.
func NewClient(profile, region, endpoint string) (*Client, error) {
	opts := []func(*config.LoadOptions) error{}

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
		if region == "" {
			opts = append(opts, config.WithRegion("us-east-1"))
		}
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}

	return &Client{
		Config:   cfg,
		Profile:  profile,
		Region:   region,
		Endpoint: endpoint,
	}, nil
}

// ServiceEndpoint returns the endpoint override for service client constructors.
// Returns nil when targeting real AWS.
func (c *Client) ServiceEndpoint() *string {
	if c.Endpoint == "" {
		return nil
	}
	return aws.String(c.Endpoint)
}
