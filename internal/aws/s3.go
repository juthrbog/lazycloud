package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Service defines all S3 operations that views depend on.
type S3Service interface {
	ListBuckets(ctx context.Context) ([]Bucket, error)
	ListObjectsPage(ctx context.Context, bucket, prefix string, token *string) (*ObjectPage, error)
	HeadObject(ctx context.Context, bucket, key string) (*ObjectMeta, error)
	GetObjectContent(ctx context.Context, bucket, key string, maxBytes int64) (string, error)
	PresignGetObject(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	ListAllKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	DownloadObject(ctx context.Context, bucket, key, destPath string) (string, error)
	ListObjectVersions(ctx context.Context, bucket, key string) ([]ObjectVersion, error)
	GetBucketProperties(ctx context.Context, bucket string) (*BucketProperties, error)
	GetBucketRegion(ctx context.Context, bucket string) (string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	DeleteObjects(ctx context.Context, bucket string, keys []string) error
	DeleteObjectVersions(ctx context.Context, bucket string, versions []ObjectVersion) error
	DeleteBucket(ctx context.Context, bucket string) error
	EmptyAndDeleteBucket(ctx context.Context, bucket string) error
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, overwrite bool) error
	MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, overwrite bool) error
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

// KeyFailure is one object key that S3 did not delete.
type KeyFailure struct {
	Key     string
	Code    string // for example "AccessDenied"
	Message string
}

// DeleteObjectsError lists the keys that DeleteObjects did not delete. S3
// deleted the other keys.
type DeleteObjectsError struct {
	Failed []KeyFailure
}

func (e *DeleteObjectsError) Error() string {
	return fmt.Sprintf("DeleteObjects: %d key(s) not deleted", len(e.Failed))
}

// DeleteObjects deletes multiple S3 objects in batches of up to 1000. When
// some keys are not deleted, it returns a *DeleteObjectsError that lists
// them. A batch call that fails does not stop the other batches.
func (svc *S3ServiceImpl) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	var failed []KeyFailure
	const batchSize = 1000
	for i := 0; i < len(keys); i += batchSize {
		batch := keys[i:min(i+batchSize, len(keys))]

		objects := make([]s3types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objects[j] = s3types.ObjectIdentifier{Key: aws.String(key)}
		}

		// Quiet mode returns only the keys that failed.
		out, err := s3c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			for _, key := range batch {
				failed = append(failed, KeyFailure{Key: key, Code: ErrorCode(err), Message: err.Error()})
			}
			continue
		}
		for _, e := range out.Errors {
			failed = append(failed, KeyFailure{
				Key:     aws.ToString(e.Key),
				Code:    aws.ToString(e.Code),
				Message: aws.ToString(e.Message),
			})
		}
	}
	if len(failed) > 0 {
		return &DeleteObjectsError{Failed: failed}
	}
	return nil
}

// DeleteObjectVersions deletes specific object versions and delete markers in batches of up to 1000.
func (svc *S3ServiceImpl) DeleteObjectVersions(ctx context.Context, bucket string, versions []ObjectVersion) error {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	const batchSize = 1000
	for i := 0; i < len(versions); i += batchSize {
		end := i + batchSize
		if end > len(versions) {
			end = len(versions)
		}
		batch := versions[i:end]

		objects := make([]s3types.ObjectIdentifier, len(batch))
		for j, v := range batch {
			objects[j] = s3types.ObjectIdentifier{
				Key:       aws.String(v.Key),
				VersionId: aws.String(v.VersionID),
			}
		}

		out, err := s3c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
		if len(out.Errors) > 0 {
			e := out.Errors[0]
			return fmt.Errorf("deleting version %s of %s: %s",
				aws.ToString(e.VersionId), aws.ToString(e.Key), aws.ToString(e.Message))
		}
	}
	return nil
}

// firstVersionPage returns the first page (up to 1,000 entries) of object
// versions and delete markers in the bucket.
func (svc *S3ServiceImpl) firstVersionPage(ctx context.Context, bucket string) ([]ObjectVersion, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	output, err := s3c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return nil, err
	}

	page := make([]ObjectVersion, 0, len(output.Versions)+len(output.DeleteMarkers))
	for _, v := range output.Versions {
		page = append(page, ObjectVersion{
			Key:       aws.ToString(v.Key),
			VersionID: aws.ToString(v.VersionId),
		})
	}
	for _, dm := range output.DeleteMarkers {
		page = append(page, ObjectVersion{
			Key:            aws.ToString(dm.Key),
			VersionID:      aws.ToString(dm.VersionId),
			IsDeleteMarker: true,
		})
	}
	return page, nil
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

// EmptyAndDeleteBucket permanently deletes all object versions and delete
// markers, then deletes the bucket. It works for versioned and unversioned
// buckets. It lists and deletes one page at a time, so a large bucket does
// not have to fit in memory. A deleted page is gone from the listing, so each
// round lists from the start.
func (svc *S3ServiceImpl) EmptyAndDeleteBucket(ctx context.Context, bucket string) error {
	for {
		page, err := svc.firstVersionPage(ctx, bucket)
		if err != nil {
			return fmt.Errorf("listing versions: %w", err)
		}
		if len(page) == 0 {
			break
		}
		if err := svc.DeleteObjectVersions(ctx, bucket, page); err != nil {
			return fmt.Errorf("deleting versions: %w", err)
		}
	}
	return svc.DeleteBucket(ctx, bucket)
}

// DownloadObject downloads an S3 object to a local file and returns the
// path that it wrote. It never overwrites a file: when destPath exists, it
// writes to "name (1).ext", "name (2).ext", and so on. Only the user can read
// the file (mode 0600). After an error, no partial file stays.
func (svc *S3ServiceImpl) DownloadObject(ctx context.Context, bucket, key, destPath string) (string, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return "", err
	}

	output, err := s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	defer output.Body.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil { //nolint:gosec // download dir needs user+group access
		return "", fmt.Errorf("creating directory: %w", err)
	}

	f, path, err := createFreeFile(destPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, output.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("writing file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("writing file: %w", err)
	}
	return path, nil
}

// maxNameTries limits the search for a free download name.
const maxNameTries = 1000

// createFreeFile creates path, or the first free "name (n).ext" variant of
// it. O_EXCL makes sure that it never opens a file that exists, also when
// another program creates one at the same moment.
func createFreeFile(path string) (*os.File, string, error) {
	ext := filepath.Ext(path)
	if ext == filepath.Base(path) {
		ext = "" // a dotfile such as ".env" becomes ".env (1)"
	}
	base := strings.TrimSuffix(path, ext)
	for n := range maxNameTries {
		p := path
		if n > 0 {
			p = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // p is the download path that DefaultDownloadPath builds
		if err == nil {
			return f, p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", fmt.Errorf("creating file: %w", err)
		}
	}
	return nil, "", fmt.Errorf("creating file: %s and %d other names exist", path, maxNameTries-1)
}

// DefaultDownloadPath returns ~/Downloads/<filename> for an S3 key. A key
// whose last element is "." or ".." gets the name "download", because
// filepath.Join would otherwise point outside ~/Downloads.
func DefaultDownloadPath(key string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	name := filepath.Base(key)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		name = "download"
	}
	return filepath.Join(home, "Downloads", name)
}

// ErrDestinationExists is returned by CopyObject and MoveObject when the
// destination object exists and overwrite is false. Nothing was copied.
var ErrDestinationExists = errors.New("the destination object exists")

// multipartCopyThreshold is the largest object that one CopyObject call can
// copy. A larger object needs a multipart copy. Tests make it smaller.
var multipartCopyThreshold int64 = 5 << 30

// copyPartSize is the smallest part of a multipart copy. A multipart upload
// has at most 10,000 parts, so a very large object gets larger parts.
var copyPartSize int64 = 512 << 20

// CopyObject copies an S3 object to a new location. Unless overwrite is
// true, it does not replace an existing destination: it returns
// ErrDestinationExists. The copy keeps the storage class, the metadata, and
// the content headers of the source. An object over 5 GB is copied in parts.
func (svc *S3ServiceImpl) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, overwrite bool) error {
	srcClient, err := svc.s3ClientForBucket(ctx, srcBucket)
	if err != nil {
		return err
	}
	head, err := srcClient.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(srcBucket),
		Key:    aws.String(srcKey),
	})
	if err != nil {
		return fmt.Errorf("reading the source: %w", err)
	}
	dstClient, err := svc.s3ClientForBucket(ctx, dstBucket)
	if err != nil {
		return err
	}

	// IfNoneMatch makes S3 refuse to replace an object that exists. Some
	// S3-compatible servers ignore it, so look for the destination first as
	// well.
	var ifNoneMatch *string
	if !overwrite {
		if _, err := dstClient.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(dstBucket),
			Key:    aws.String(dstKey),
		}); err == nil {
			return ErrDestinationExists
		}
		ifNoneMatch = aws.String("*")
	}

	if aws.ToInt64(head.ContentLength) > multipartCopyThreshold {
		err = multipartCopy(ctx, dstClient, srcBucket, srcKey, dstBucket, dstKey, head, ifNoneMatch)
	} else {
		_, err = dstClient.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:       aws.String(dstBucket),
			Key:          aws.String(dstKey),
			CopySource:   aws.String(copySource(srcBucket, srcKey)),
			StorageClass: head.StorageClass,
			IfNoneMatch:  ifNoneMatch,
		})
	}
	if isPreconditionFailed(err) {
		return ErrDestinationExists
	}
	return err
}

// MoveObject copies an object to a new location then deletes the source.
// Like CopyObject, it does not replace an existing destination unless
// overwrite is true.
func (svc *S3ServiceImpl) MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, overwrite bool) error {
	if err := svc.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey, overwrite); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := svc.DeleteObject(ctx, srcBucket, srcKey); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	return nil
}

// multipartCopy copies an object that is too large for one CopyObject call.
// It copies byte ranges with UploadPartCopy. A multipart upload does not copy
// the metadata, so it sets the metadata and content headers of the source.
// After an error, it aborts the upload, so no incomplete parts stay.
func multipartCopy(ctx context.Context, c *s3.Client, srcBucket, srcKey, dstBucket, dstKey string,
	head *s3.HeadObjectOutput, ifNoneMatch *string) error {
	created, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:             aws.String(dstBucket),
		Key:                aws.String(dstKey),
		StorageClass:       head.StorageClass,
		Metadata:           head.Metadata,
		ContentType:        head.ContentType,
		CacheControl:       head.CacheControl,
		ContentDisposition: head.ContentDisposition,
		ContentEncoding:    head.ContentEncoding,
		ContentLanguage:    head.ContentLanguage,
	})
	if err != nil {
		return fmt.Errorf("starting the multipart copy: %w", err)
	}
	abort := func(cause error) error {
		// A new context: the abort runs also when ctx was canceled.
		_, _ = c.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(dstBucket),
			Key:      aws.String(dstKey),
			UploadId: created.UploadId,
		})
		return cause
	}

	size := aws.ToInt64(head.ContentLength)
	partSize := max(copyPartSize, (size+9999)/10000)
	var parts []s3types.CompletedPart
	for start, n := int64(0), int32(1); start < size; start, n = start+partSize, n+1 {
		end := min(start+partSize, size) - 1
		out, err := c.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
			Bucket:          aws.String(dstBucket),
			Key:             aws.String(dstKey),
			UploadId:        created.UploadId,
			PartNumber:      aws.Int32(n),
			CopySource:      aws.String(copySource(srcBucket, srcKey)),
			CopySourceRange: aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
		})
		if err != nil {
			return abort(fmt.Errorf("copying part %d: %w", n, err))
		}
		var etag *string
		if out.CopyPartResult != nil {
			etag = out.CopyPartResult.ETag
		}
		parts = append(parts, s3types.CompletedPart{ETag: etag, PartNumber: aws.Int32(n)})
	}

	_, err = c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(dstBucket),
		Key:             aws.String(dstKey),
		UploadId:        created.UploadId,
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: parts},
		IfNoneMatch:     ifNoneMatch,
	})
	if err != nil {
		return abort(err)
	}
	return nil
}

// copySource returns the CopySource value for an object. S3 URL-decodes it,
// so every byte outside the unreserved set is percent-encoded. A key with
// "%", "?", "#", "+", or a space then names the right object. The slashes
// stay, because S3 splits the bucket from the key at the first one.
func copySource(bucket, key string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(bucket + "/" + key) {
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// isPreconditionFailed reports whether S3 refused a write because of
// IfNoneMatch: the destination exists.
func isPreconditionFailed(err error) bool {
	var apiErr smithy.APIError
	return err != nil && errors.As(err, &apiErr) && apiErr.ErrorCode() == "PreconditionFailed"
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

// ListObjectVersions returns all versions and delete markers of one object.
//
// The listing filters by prefix, so it also returns the versions of sibling
// keys such as "key.bak", and it returns them in pages of 1,000. S3 sorts
// the listing by key, and key sorts before every sibling. So the function
// reads pages until a page holds a sibling key or no page is left. A key
// with more than 1,000 versions then shows them all.
func (svc *S3ServiceImpl) ListObjectVersions(ctx context.Context, bucket, key string) ([]ObjectVersion, error) {
	s3c, err := svc.s3ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}

	var versions []ObjectVersion
	p := s3.NewListObjectVersionsPaginator(s3c, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucket),
		Prefix: aws.String(key),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		sibling := false
		for _, v := range page.Versions {
			if aws.ToString(v.Key) != key {
				sibling = true
				continue
			}
			ov := ObjectVersion{
				Key:       key,
				VersionID: aws.ToString(v.VersionId),
				Size:      aws.ToInt64(v.Size),
				IsLatest:  aws.ToBool(v.IsLatest),
			}
			if v.LastModified != nil {
				ov.LastModified = *v.LastModified
			}
			versions = append(versions, ov)
		}
		for _, dm := range page.DeleteMarkers {
			if aws.ToString(dm.Key) != key {
				sibling = true
				continue
			}
			ov := ObjectVersion{
				Key:            key,
				VersionID:      aws.ToString(dm.VersionId),
				IsLatest:       aws.ToBool(dm.IsLatest),
				IsDeleteMarker: true,
			}
			if dm.LastModified != nil {
				ov.LastModified = *dm.LastModified
			}
			versions = append(versions, ov)
		}
		if sibling {
			break // every version of key came before the first sibling
		}
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
