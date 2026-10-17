package aws

import (
	"context"
	"errors"
	"fmt"
)

// ErrReadOnly is returned by a guarded service when a call would change AWS
// resources while the app is in ReadOnly mode.
var ErrReadOnly = errors.New("blocked in ReadOnly mode")

// The guards below wrap the services and refuse every call that changes AWS
// resources while readOnly returns true. They check the mode when the call
// runs, not when the user pressed the key, so a prompt, a picker, or a form
// that was opened in ReadWrite mode cannot act after a switch to ReadOnly.
// Calls that only read pass through. readOnly is called from command
// goroutines, so it must be safe for concurrent use.
//
// A new mutating method must get a guard here. readonly_test.go fails when
// an interface method is in neither its list of reads nor its list of guards.

func readOnlyErr(op string) error {
	return fmt.Errorf("%s: %w", op, ErrReadOnly)
}

// GuardS3 returns svc with a ReadOnly guard on every mutating method.
func GuardS3(svc S3Service, readOnly func() bool) S3Service {
	return guardedS3{S3Service: svc, readOnly: readOnly}
}

type guardedS3 struct {
	S3Service
	readOnly func() bool
}

func (g guardedS3) DeleteObject(ctx context.Context, bucket, key string) error {
	if g.readOnly() {
		return readOnlyErr("DeleteObject")
	}
	return g.S3Service.DeleteObject(ctx, bucket, key)
}

func (g guardedS3) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	if g.readOnly() {
		return readOnlyErr("DeleteObjects")
	}
	return g.S3Service.DeleteObjects(ctx, bucket, keys)
}

func (g guardedS3) DeleteObjectVersions(ctx context.Context, bucket string, versions []ObjectVersion) error {
	if g.readOnly() {
		return readOnlyErr("DeleteObjectVersions")
	}
	return g.S3Service.DeleteObjectVersions(ctx, bucket, versions)
}

func (g guardedS3) DeleteBucket(ctx context.Context, bucket string) error {
	if g.readOnly() {
		return readOnlyErr("DeleteBucket")
	}
	return g.S3Service.DeleteBucket(ctx, bucket)
}

func (g guardedS3) EmptyAndDeleteBucket(ctx context.Context, bucket string) error {
	if g.readOnly() {
		return readOnlyErr("EmptyAndDeleteBucket")
	}
	return g.S3Service.EmptyAndDeleteBucket(ctx, bucket)
}

func (g guardedS3) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, overwrite bool) error {
	if g.readOnly() {
		return readOnlyErr("CopyObject")
	}
	return g.S3Service.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey, overwrite)
}

func (g guardedS3) MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, overwrite bool) error {
	if g.readOnly() {
		return readOnlyErr("MoveObject")
	}
	return g.S3Service.MoveObject(ctx, srcBucket, srcKey, dstBucket, dstKey, overwrite)
}

func (g guardedS3) CreateBucket(ctx context.Context, name, region string) error {
	if g.readOnly() {
		return readOnlyErr("CreateBucket")
	}
	return g.S3Service.CreateBucket(ctx, name, region)
}

// GuardEC2 returns svc with a ReadOnly guard on every mutating method.
func GuardEC2(svc EC2Service, readOnly func() bool) EC2Service {
	return guardedEC2{EC2Service: svc, readOnly: readOnly}
}

type guardedEC2 struct {
	EC2Service
	readOnly func() bool
}

func (g guardedEC2) StartInstance(ctx context.Context, instanceID string) error {
	if g.readOnly() {
		return readOnlyErr("StartInstance")
	}
	return g.EC2Service.StartInstance(ctx, instanceID)
}

func (g guardedEC2) StopInstance(ctx context.Context, instanceID string) error {
	if g.readOnly() {
		return readOnlyErr("StopInstance")
	}
	return g.EC2Service.StopInstance(ctx, instanceID)
}

func (g guardedEC2) RebootInstance(ctx context.Context, instanceID string) error {
	if g.readOnly() {
		return readOnlyErr("RebootInstance")
	}
	return g.EC2Service.RebootInstance(ctx, instanceID)
}

func (g guardedEC2) TerminateInstance(ctx context.Context, instanceID string) error {
	if g.readOnly() {
		return readOnlyErr("TerminateInstance")
	}
	return g.EC2Service.TerminateInstance(ctx, instanceID)
}

func (g guardedEC2) StartInstances(ctx context.Context, ids []string) error {
	if g.readOnly() {
		return readOnlyErr("StartInstances")
	}
	return g.EC2Service.StartInstances(ctx, ids)
}

func (g guardedEC2) StopInstances(ctx context.Context, ids []string) error {
	if g.readOnly() {
		return readOnlyErr("StopInstances")
	}
	return g.EC2Service.StopInstances(ctx, ids)
}

func (g guardedEC2) RebootInstances(ctx context.Context, ids []string) error {
	if g.readOnly() {
		return readOnlyErr("RebootInstances")
	}
	return g.EC2Service.RebootInstances(ctx, ids)
}

func (g guardedEC2) TerminateInstances(ctx context.Context, ids []string) error {
	if g.readOnly() {
		return readOnlyErr("TerminateInstances")
	}
	return g.EC2Service.TerminateInstances(ctx, ids)
}

// GuardSQS returns svc with a ReadOnly guard on every mutating method.
// ReceiveMessages is not guarded: the messages view decides when a peek is
// allowed in ReadOnly mode (only on queues without a dead-letter queue).
func GuardSQS(svc SQSService, readOnly func() bool) SQSService {
	return guardedSQS{SQSService: svc, readOnly: readOnly}
}

type guardedSQS struct {
	SQSService
	readOnly func() bool
}

func (g guardedSQS) SendMessage(ctx context.Context, queueURL, body string, delaySeconds int32, groupID, dedupID string) error {
	if g.readOnly() {
		return readOnlyErr("SendMessage")
	}
	return g.SQSService.SendMessage(ctx, queueURL, body, delaySeconds, groupID, dedupID)
}

func (g guardedSQS) DeleteMessage(ctx context.Context, queueURL, receiptHandle string) error {
	if g.readOnly() {
		return readOnlyErr("DeleteMessage")
	}
	return g.SQSService.DeleteMessage(ctx, queueURL, receiptHandle)
}

func (g guardedSQS) DeleteMessageBatch(ctx context.Context, queueURL string, receiptHandles []string) error {
	if g.readOnly() {
		return readOnlyErr("DeleteMessageBatch")
	}
	return g.SQSService.DeleteMessageBatch(ctx, queueURL, receiptHandles)
}

func (g guardedSQS) PurgeQueue(ctx context.Context, queueURL string) error {
	if g.readOnly() {
		return readOnlyErr("PurgeQueue")
	}
	return g.SQSService.PurgeQueue(ctx, queueURL)
}

func (g guardedSQS) DeleteQueue(ctx context.Context, queueURL string) error {
	if g.readOnly() {
		return readOnlyErr("DeleteQueue")
	}
	return g.SQSService.DeleteQueue(ctx, queueURL)
}

func (g guardedSQS) StartMessageMoveTask(ctx context.Context, sourceArn, destArn string) (string, error) {
	if g.readOnly() {
		return "", readOnlyErr("StartMessageMoveTask")
	}
	return g.SQSService.StartMessageMoveTask(ctx, sourceArn, destArn)
}
