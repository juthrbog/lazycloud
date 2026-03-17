package aws

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client returns an S3 service client configured for the current profile/region/endpoint.
func (c *Client) S3Client() *s3.Client {
	return s3.NewFromConfig(c.Config, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
			o.UsePathStyle = true // required for LocalStack
		}
	})
}

// S3ClientForRegion returns an S3 client pinned to a specific region.
func (c *Client) S3ClientForRegion(region string) *s3.Client {
	return s3.NewFromConfig(c.Config, func(o *s3.Options) {
		o.Region = region
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
			o.UsePathStyle = true
		}
	})
}

// GetBucketRegion resolves the region a bucket lives in.
func GetBucketRegion(ctx context.Context, client *Client, bucket string) (string, error) {
	// LocalStack doesn't support GetBucketLocation properly
	if client.Endpoint != "" {
		if client.Region != "" {
			return client.Region, nil
		}
		return "us-east-1", nil
	}

	svc := client.S3Client()
	output, err := svc.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return "", err
	}

	// GetBucketLocation returns "" for us-east-1 (legacy behavior)
	region := string(output.LocationConstraint)
	if region == "" {
		region = "us-east-1"
	}
	return region, nil
}

// s3ClientForBucket resolves the bucket's region and returns a properly configured client.
func s3ClientForBucket(ctx context.Context, client *Client, bucket string) (*s3.Client, error) {
	region, err := GetBucketRegion(ctx, client, bucket)
	if err != nil {
		return nil, fmt.Errorf("resolving bucket region: %w", err)
	}
	return client.S3ClientForRegion(region), nil
}

// Bucket represents an S3 bucket.
type Bucket struct {
	Name         string
	CreationDate time.Time
}

// S3Object represents an object in an S3 bucket.
type S3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
	StorageClass string
}

// ObjectMeta holds detailed metadata for an S3 object.
type ObjectMeta struct {
	Key          string
	Size         int64
	LastModified time.Time
	ContentType  string
	StorageClass string
	ETag         string
	Metadata     map[string]string
}

// ListBuckets returns all S3 buckets accessible to the current credentials.
func ListBuckets(ctx context.Context, client *Client) ([]Bucket, error) {
	svc := client.S3Client()
	output, err := svc.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}

	buckets := make([]Bucket, 0, len(output.Buckets))
	for _, b := range output.Buckets {
		bucket := Bucket{
			Name: aws.ToString(b.Name),
		}
		if b.CreationDate != nil {
			bucket.CreationDate = *b.CreationDate
		}
		buckets = append(buckets, bucket)
	}
	return buckets, nil
}

// ObjectPage holds the results of a single page of ListObjectsV2.
type ObjectPage struct {
	Objects      []S3Object
	Prefixes     []string
	HasMorePages bool
	Token        *string // continuation token for the next page
}

// ListObjectsPage fetches a single page of objects. Pass nil token for the first page.
// Returns the page results and a token for the next page (nil if no more pages).
func ListObjectsPage(ctx context.Context, client *Client, bucket, prefix string, token *string) (*ObjectPage, error) {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return nil, err
	}

	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	}
	if token != nil {
		input.ContinuationToken = token
	}

	output, err := svc.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, err
	}

	page := &ObjectPage{
		HasMorePages: aws.ToBool(output.IsTruncated),
		Token:        output.NextContinuationToken,
	}

	for _, cp := range output.CommonPrefixes {
		page.Prefixes = append(page.Prefixes, aws.ToString(cp.Prefix))
	}

	for _, obj := range output.Contents {
		key := aws.ToString(obj.Key)
		if key == prefix {
			continue
		}
		o := S3Object{
			Key:          key,
			Size:         aws.ToInt64(obj.Size),
			StorageClass: string(obj.StorageClass),
		}
		if obj.LastModified != nil {
			o.LastModified = *obj.LastModified
		}
		page.Objects = append(page.Objects, o)
	}

	return page, nil
}

// HeadObject returns detailed metadata for an S3 object.
func HeadObject(ctx context.Context, client *Client, bucket, key string) (*ObjectMeta, error) {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return nil, err
	}
	output, err := svc.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	meta := &ObjectMeta{
		Key:          key,
		Size:         aws.ToInt64(output.ContentLength),
		ContentType:  aws.ToString(output.ContentType),
		StorageClass: string(output.StorageClass),
		ETag:         aws.ToString(output.ETag),
		Metadata:     output.Metadata,
	}
	if output.LastModified != nil {
		meta.LastModified = *output.LastModified
	}
	return meta, nil
}

// GetObjectContent reads up to maxBytes of an S3 object and returns it as a string.
func GetObjectContent(ctx context.Context, client *Client, bucket, key string, maxBytes int64) (string, error) {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return "", err
	}
	rangeHeader := fmt.Sprintf("bytes=0-%d", maxBytes-1)
	output, err := svc.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		return "", err
	}
	defer output.Body.Close()

	data, err := io.ReadAll(io.LimitReader(output.Body, maxBytes))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// PresignGetObject generates a presigned GET URL for an S3 object.
func PresignGetObject(ctx context.Context, client *Client, bucket, key string, expiry time.Duration) (string, error) {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return "", err
	}
	presigner := s3.NewPresignClient(svc)
	req, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// FormatBytes returns a human-readable byte size string.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
