package aws

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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

// DeleteObject deletes a single S3 object.
func DeleteObject(ctx context.Context, client *Client, bucket, key string) error {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return err
	}
	_, err = svc.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err
}

// DeleteObjects deletes multiple S3 objects in batches of up to 1000.
func DeleteObjects(ctx context.Context, client *Client, bucket string, keys []string) error {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return err
	}

	const batchSize = 1000
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]

		objects := make([]s3types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objects[j] = s3types.ObjectIdentifier{Key: aws.String(key)}
		}

		_, err := svc.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteBucket deletes an S3 bucket. The bucket must be empty.
func DeleteBucket(ctx context.Context, client *Client, bucket string) error {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return err
	}
	_, err = svc.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	return err
}

// EmptyAndDeleteBucket deletes all objects in a bucket, then deletes the bucket itself.
func EmptyAndDeleteBucket(ctx context.Context, client *Client, bucket string) error {
	// List and delete all objects
	for {
		page, err := ListObjectsPage(ctx, client, bucket, "", nil)
		if err != nil {
			return fmt.Errorf("listing objects: %w", err)
		}
		if len(page.Objects) == 0 {
			break
		}
		keys := make([]string, len(page.Objects))
		for i, obj := range page.Objects {
			keys[i] = obj.Key
		}
		if err := DeleteObjects(ctx, client, bucket, keys); err != nil {
			return fmt.Errorf("deleting objects: %w", err)
		}
	}
	return DeleteBucket(ctx, client, bucket)
}

// DownloadObject downloads an S3 object to a local file.
func DownloadObject(ctx context.Context, client *Client, bucket, key, destPath string) error {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return err
	}

	output, err := svc.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return err
	}
	defer output.Body.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, output.Body); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	return nil
}

// DefaultDownloadPath returns ~/Downloads/<filename> for an S3 key.
func DefaultDownloadPath(key string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Downloads", filepath.Base(key))
}

// CopyObject copies an S3 object to a new location.
func CopyObject(ctx context.Context, client *Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	svc, err := s3ClientForBucket(ctx, client, dstBucket)
	if err != nil {
		return err
	}

	copySource := srcBucket + "/" + srcKey
	_, err = svc.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(copySource),
	})
	return err
}

// MoveObject copies an object to a new location then deletes the source.
func MoveObject(ctx context.Context, client *Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	if err := CopyObject(ctx, client, srcBucket, srcKey, dstBucket, dstKey); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := DeleteObject(ctx, client, srcBucket, srcKey); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	return nil
}

// ObjectVersion represents a version of an S3 object.
type ObjectVersion struct {
	Key            string
	VersionID      string
	Size           int64
	LastModified   time.Time
	IsLatest       bool
	IsDeleteMarker bool
}

// ListObjectVersions returns all versions of a specific object.
func ListObjectVersions(ctx context.Context, client *Client, bucket, key string) ([]ObjectVersion, error) {
	svc, err := s3ClientForBucket(ctx, client, bucket)
	if err != nil {
		return nil, err
	}

	output, err := svc.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucket),
		Prefix: aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	var versions []ObjectVersion
	for _, v := range output.Versions {
		if aws.ToString(v.Key) != key {
			continue
		}
		ov := ObjectVersion{
			Key:       aws.ToString(v.Key),
			VersionID: aws.ToString(v.VersionId),
			Size:      aws.ToInt64(v.Size),
			IsLatest:  aws.ToBool(v.IsLatest),
		}
		if v.LastModified != nil {
			ov.LastModified = *v.LastModified
		}
		versions = append(versions, ov)
	}

	for _, dm := range output.DeleteMarkers {
		if aws.ToString(dm.Key) != key {
			continue
		}
		ov := ObjectVersion{
			Key:            aws.ToString(dm.Key),
			VersionID:      aws.ToString(dm.VersionId),
			IsLatest:       aws.ToBool(dm.IsLatest),
			IsDeleteMarker: true,
		}
		if dm.LastModified != nil {
			ov.LastModified = *dm.LastModified
		}
		versions = append(versions, ov)
	}

	return versions, nil
}

// BucketProperties holds aggregated bucket configuration.
type BucketProperties struct {
	Name         string
	Region       string
	Versioning   string // "Enabled", "Suspended", or ""
	Encryption   string // "AES256", "aws:kms", or ""
	PublicAccess bool   // true if all public access is blocked
}

// GetBucketProperties aggregates bucket configuration from multiple API calls.
// Individual call failures are non-fatal (permissions may vary).
func GetBucketProperties(ctx context.Context, client *Client, bucket string) (*BucketProperties, error) {
	region, err := GetBucketRegion(ctx, client, bucket)
	if err != nil {
		return nil, err
	}

	svc := client.S3ClientForRegion(region)
	props := &BucketProperties{
		Name:   bucket,
		Region: region,
	}

	// Versioning
	if vOut, err := svc.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	}); err == nil {
		props.Versioning = string(vOut.Status)
	}

	// Encryption
	if eOut, err := svc.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{
		Bucket: aws.String(bucket),
	}); err == nil && eOut.ServerSideEncryptionConfiguration != nil {
		for _, rule := range eOut.ServerSideEncryptionConfiguration.Rules {
			if rule.ApplyServerSideEncryptionByDefault != nil {
				props.Encryption = string(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm)
				break
			}
		}
	}

	// Public access block
	if pOut, err := svc.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{
		Bucket: aws.String(bucket),
	}); err == nil && pOut.PublicAccessBlockConfiguration != nil {
		cfg := pOut.PublicAccessBlockConfiguration
		props.PublicAccess = aws.ToBool(cfg.BlockPublicAcls) &&
			aws.ToBool(cfg.BlockPublicPolicy) &&
			aws.ToBool(cfg.IgnorePublicAcls) &&
			aws.ToBool(cfg.RestrictPublicBuckets)
	}

	return props, nil
}

// CreateBucket creates a new S3 bucket in the specified region.
func CreateBucket(ctx context.Context, client *Client, name, region string) error {
	svc := client.S3ClientForRegion(region)

	input := &s3.CreateBucketInput{
		Bucket: aws.String(name),
	}

	// us-east-1 doesn't use LocationConstraint (legacy behavior)
	if region != "us-east-1" {
		input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}

	_, err := svc.CreateBucket(ctx, input)
	return err
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
