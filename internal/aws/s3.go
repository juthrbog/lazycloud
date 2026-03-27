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

// S3Service defines all S3 operations that views depend on.
type S3Service interface {
	ListBuckets(ctx context.Context) ([]Bucket, error)
	ListObjectsPage(ctx context.Context, bucket, prefix string, token *string) (*ObjectPage, error)
	HeadObject(ctx context.Context, bucket, key string) (*ObjectMeta, error)
	GetObjectContent(ctx context.Context, bucket, key string, maxBytes int64) (string, error)
	PresignGetObject(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	ListAllKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	DownloadObject(ctx context.Context, bucket, key, destPath string) error
	ListObjectVersions(ctx context.Context, bucket, key string) ([]ObjectVersion, error)
	GetBucketProperties(ctx context.Context, bucket string) (*BucketProperties, error)
	GetBucketRegion(ctx context.Context, bucket string) (string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	DeleteObjects(ctx context.Context, bucket string, keys []string) error
	DeleteBucket(ctx context.Context, bucket string) error
	EmptyAndDeleteBucket(ctx context.Context, bucket string) error
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
	MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
	CreateBucket(ctx context.Context, name, region string) error
}

// S3ServiceImpl is the real AWS-backed implementation of S3Service.
type S3ServiceImpl struct {
	client *Client
}

// NewS3Service creates a real S3 service backed by the given AWS client.
func NewS3Service(client *Client) *S3ServiceImpl {
	return &S3ServiceImpl{client: client}
}

var _ S3Service = (*S3ServiceImpl)(nil)

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
func (svc *S3ServiceImpl) GetBucketRegion(ctx context.Context, bucket string) (string, error) {
	// LocalStack doesn't support GetBucketLocation properly
	if svc.client.Endpoint != "" {
		if svc.client.Region != "" {
			return svc.client.Region, nil
		}
		return "us-east-1", nil
	}

	s3c := svc.client.S3Client()
	output, err := s3c.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
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
func (svc *S3ServiceImpl) s3ClientForBucket(ctx context.Context, bucket string) (*s3.Client, error) {
	region, err := svc.GetBucketRegion(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("resolving bucket region: %w", err)
	}
	return svc.client.S3ClientForRegion(region), nil
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
func (svc *S3ServiceImpl) ListBuckets(ctx context.Context) ([]Bucket, error) {
	s3c := svc.client.S3Client()
	output, err := s3c.ListBuckets(ctx, &s3.ListBucketsInput{})
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
func (svc *S3ServiceImpl) ListObjectsPage(ctx context.Context, bucket, prefix string, token *string) (*ObjectPage, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
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

	output, err := s3c.ListObjectsV2(ctx, input)
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
func (svc *S3ServiceImpl) HeadObject(ctx context.Context, bucket, key string) (*ObjectMeta, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	output, err := s3c.HeadObject(ctx, &s3.HeadObjectInput{
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
func (svc *S3ServiceImpl) GetObjectContent(ctx context.Context, bucket, key string, maxBytes int64) (string, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return "", err
	}
	rangeHeader := fmt.Sprintf("bytes=0-%d", maxBytes-1)
	output, err := s3c.GetObject(ctx, &s3.GetObjectInput{
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
func (svc *S3ServiceImpl) PresignGetObject(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return "", err
	}
	presigner := s3.NewPresignClient(s3c)
	req, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// ListAllKeys returns every object key under prefix (no delimiter, recursive).
// Used to resolve a "folder" into concrete keys for deletion.
func (svc *S3ServiceImpl) ListAllKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}

	var keys []string
	var token *string
	for {
		input := &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String(prefix),
		}
		if token != nil {
			input.ContinuationToken = token
		}
		output, err := s3c.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, obj := range output.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		token = output.NextContinuationToken
	}
	return keys, nil
}

// DeleteObject deletes a single S3 object.
func (svc *S3ServiceImpl) DeleteObject(ctx context.Context, bucket, key string) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	_, err = s3c.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err
}

// DeleteObjects deletes multiple S3 objects in batches of up to 1000.
func (svc *S3ServiceImpl) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
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

		_, err := s3c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
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
func (svc *S3ServiceImpl) DeleteBucket(ctx context.Context, bucket string) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	_, err = s3c.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	return err
}

// EmptyAndDeleteBucket deletes all objects in a bucket, then deletes the bucket itself.
func (svc *S3ServiceImpl) EmptyAndDeleteBucket(ctx context.Context, bucket string) error {
	// List and delete all objects
	for {
		page, err := svc.ListObjectsPage(ctx, bucket, "", nil)
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
		if err := svc.DeleteObjects(ctx, bucket, keys); err != nil {
			return fmt.Errorf("deleting objects: %w", err)
		}
	}
	return svc.DeleteBucket(ctx, bucket)
}

// DownloadObject downloads an S3 object to a local file.
func (svc *S3ServiceImpl) DownloadObject(ctx context.Context, bucket, key, destPath string) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	output, err := s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return err
	}
	defer output.Body.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil { //nolint:gosec // download dir needs user+group access
		return fmt.Errorf("creating directory: %w", err)
	}

	f, err := os.Create(destPath) //nolint:gosec // destPath is user-chosen download destination
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
func (svc *S3ServiceImpl) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	s3c, err := svc.s3ClientForBucket(ctx, dstBucket)
	if err != nil {
		return err
	}

	copySource := srcBucket + "/" + srcKey
	_, err = s3c.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(copySource),
	})
	return err
}

// MoveObject copies an object to a new location then deletes the source.
func (svc *S3ServiceImpl) MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	if err := svc.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := svc.DeleteObject(ctx, srcBucket, srcKey); err != nil {
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
func (svc *S3ServiceImpl) ListObjectVersions(ctx context.Context, bucket, key string) ([]ObjectVersion, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}

	output, err := s3c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
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
func (svc *S3ServiceImpl) GetBucketProperties(ctx context.Context, bucket string) (*BucketProperties, error) {
	region, err := svc.GetBucketRegion(ctx, bucket)
	if err != nil {
		return nil, err
	}

	s3c := svc.client.S3ClientForRegion(region)
	props := &BucketProperties{
		Name:   bucket,
		Region: region,
	}

	// Versioning
	if vOut, err := s3c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	}); err == nil {
		props.Versioning = string(vOut.Status)
	}

	// Encryption
	if eOut, err := s3c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{
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
	if pOut, err := s3c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{
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
func (svc *S3ServiceImpl) CreateBucket(ctx context.Context, name, region string) error {
	s3c := svc.client.S3ClientForRegion(region)

	input := &s3.CreateBucketInput{
		Bucket: aws.String(name),
	}

	// us-east-1 doesn't use LocationConstraint (legacy behavior)
	if region != "us-east-1" {
		input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}

	_, err := s3c.CreateBucket(ctx, input)
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
