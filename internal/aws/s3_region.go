package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// An S3 call must go to the region of its bucket. The service keeps the
// region of each bucket that it knows, so a call needs no extra request to
// find it. ListBuckets fills the cache with the region of every bucket, and
// a bucket that is not in it is looked up once with HeadBucket.
//
// The cache belongs to the service, so it lives as long as the session of
// the service: a profile or region switch starts with an empty cache.

// GetBucketRegion returns the region of a bucket.
func (svc *S3ServiceImpl) GetBucketRegion(ctx context.Context, bucket string) (string, error) {
	// The endpoint override (LocalStack) has one region for all buckets.
	if svc.client.Endpoint != "" {
		if svc.client.Region != "" {
			return svc.client.Region, nil
		}
		return "us-east-1", nil
	}

	if region, ok := svc.cachedRegion(bucket); ok {
		return region, nil
	}
	region, err := svc.lookupBucketRegion(ctx, bucket)
	if err != nil {
		return "", err
	}
	svc.rememberRegion(bucket, region)
	return region, nil
}

// lookupBucketRegion asks S3 for the region of a bucket. HeadBucket gives
// the region in its result, and also in the x-amz-bucket-region header of
// its error when the bucket is in another region or the call is denied.
// GetBucketLocation is the last resort: AWS no longer recommends it, it
// fails across accounts outside the home region of the bucket, and it
// needs a permission of its own.
func (svc *S3ServiceImpl) lookupBucketRegion(ctx context.Context, bucket string) (string, error) {
	s3c := svc.client.S3Client()
	out, err := s3c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil && aws.ToString(out.BucketRegion) != "" {
		return aws.ToString(out.BucketRegion), nil
	}
	if region := regionHeader(err); region != "" {
		return region, nil
	}
	if ctx.Err() != nil {
		return "", err
	}

	loc, lerr := s3c.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if lerr != nil {
		// The error of GetBucketLocation has a code and a message, such as
		// NoSuchBucket. HeadBucket has no body, so its error has neither.
		return "", lerr
	}
	return locationRegion(loc.LocationConstraint), nil
}

// regionHeader returns the x-amz-bucket-region header of a failed S3 call,
// or "" when the error has no such header.
func regionHeader(err error) string {
	re, ok := errors.AsType[*awshttp.ResponseError](err)
	if !ok || re.Response == nil {
		return ""
	}
	return re.Response.Header.Get("X-Amz-Bucket-Region")
}

// locationRegion turns the LocationConstraint of GetBucketLocation into a
// region. S3 returns "" for us-east-1 and "EU" for old buckets in eu-west-1.
func locationRegion(c s3types.BucketLocationConstraint) string {
	if c == "" {
		return "us-east-1"
	}
	if c == s3types.BucketLocationConstraintEu {
		return "eu-west-1"
	}
	return string(c)
}

func (svc *S3ServiceImpl) cachedRegion(bucket string) (string, bool) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	region, ok := svc.regions[bucket]
	return region, ok
}

func (svc *S3ServiceImpl) rememberRegion(bucket, region string) {
	if region == "" {
		return
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.regions == nil {
		svc.regions = map[string]string{}
	}
	svc.regions[bucket] = region
}

func (svc *S3ServiceImpl) forgetRegion(bucket string) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	delete(svc.regions, bucket)
}

// s3ClientForBucket returns a client for the region of a bucket. It keeps
// one client for each region.
func (svc *S3ServiceImpl) s3ClientForBucket(ctx context.Context, bucket string) (*s3.Client, error) {
	region, err := svc.GetBucketRegion(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("resolving bucket region: %w", err)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if c, ok := svc.clients[region]; ok {
		return c, nil
	}
	if svc.clients == nil {
		svc.clients = map[string]*s3.Client{}
	}
	c := svc.client.S3ClientForRegion(region)
	svc.clients[region] = c
	return c, nil
}
