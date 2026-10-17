package aws_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
)

// guardCase lists every method of one service interface as a read or as a
// mutation. A new interface method must go in one of the two lists.
type guardCase struct {
	name      string
	iface     reflect.Type
	reads     []string
	mutations []string
	// guard wraps a fresh mock. It returns the guarded service and the mock.
	guard func(readOnly func() bool) (any, *mock.Mock)
}

var guardCases = []guardCase{
	{
		name:  "S3",
		iface: reflect.TypeFor[aws.S3Service](),
		reads: []string{
			"ListBuckets", "ListObjectsPage", "HeadObject", "GetObjectContent",
			"PresignGetObject", "ListAllKeys", "DownloadObject", "ListObjectVersions",
			"GetBucketProperties", "GetBucketRegion",
		},
		mutations: []string{
			"DeleteObject", "DeleteObjects", "DeleteObjectVersions", "DeleteBucket",
			"EmptyAndDeleteBucket", "CopyObject", "MoveObject", "CreateBucket",
		},
		guard: func(readOnly func() bool) (any, *mock.Mock) {
			m := new(awstest.MockS3Service)
			return aws.GuardS3(m, readOnly), &m.Mock
		},
	},
	{
		name:  "EC2",
		iface: reflect.TypeFor[aws.EC2Service](),
		reads: []string{
			"ListInstances", "ListInstancesPage", "GetInstanceDetail", "RefreshInstances",
			"ListOwnedAMIs", "SearchAMIs", "RefreshAMIs", "ListSecurityGroups",
			"GetSecurityGroup", "ListVPCsPage", "GetVPC", "ListSubnetsPage", "GetSubnet",
		},
		mutations: []string{
			"StartInstance", "StopInstance", "RebootInstance", "TerminateInstance",
			"StartInstances", "StopInstances", "RebootInstances", "TerminateInstances",
		},
		guard: func(readOnly func() bool) (any, *mock.Mock) {
			m := new(awstest.MockEC2Service)
			return aws.GuardEC2(m, readOnly), &m.Mock
		},
	},
	{
		name:  "SQS",
		iface: reflect.TypeFor[aws.SQSService](),
		// ReceiveMessages changes the receive count, but the messages view
		// owns the ReadOnly rule for a peek.
		reads: []string{
			"ListQueuesPage", "GetQueueAttributes", "FetchAllQueueAttributes",
			"ReceiveMessages", "ListDeadLetterSourceQueues", "ListMessageMoveTasks",
		},
		mutations: []string{
			"SendMessage", "DeleteMessage", "DeleteMessageBatch", "PurgeQueue",
			"DeleteQueue", "StartMessageMoveTask",
		},
		guard: func(readOnly func() bool) (any, *mock.Mock) {
			m := new(awstest.MockSQSService)
			return aws.GuardSQS(m, readOnly), &m.Mock
		},
	},
}

// Every interface method is listed, so a new mutating method cannot pass
// through the guard without a decision.
func TestGuardListsEveryMethod(t *testing.T) {
	for _, tc := range guardCases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.iface.NumMethod() {
				name := tc.iface.Method(i).Name
				listed := slices.Contains(tc.reads, name) || slices.Contains(tc.mutations, name)
				assert.True(t, listed, "%s.%s is in neither reads nor mutations", tc.name, name)
			}
			assert.Equal(t, tc.iface.NumMethod(), len(tc.reads)+len(tc.mutations), "a method is listed twice or does not exist")
		})
	}
}

func TestGuardBlocksMutationsInReadOnly(t *testing.T) {
	for _, tc := range guardCases {
		for _, name := range tc.mutations {
			t.Run(tc.name+"."+name, func(t *testing.T) {
				svc, m := tc.guard(func() bool { return true })
				expect(m, tc.iface, name)
				out := call(svc, name)
				err, _ := out[len(out)-1].Interface().(error)
				require.ErrorIs(t, err, aws.ErrReadOnly)
				assert.Contains(t, err.Error(), name)
				m.AssertNotCalled(t, name, anyArgs(tc.iface, name)...)
			})
		}
	}
}

func TestGuardPassesMutationsInReadWrite(t *testing.T) {
	for _, tc := range guardCases {
		for _, name := range tc.mutations {
			t.Run(tc.name+"."+name, func(t *testing.T) {
				svc, m := tc.guard(func() bool { return false })
				expect(m, tc.iface, name)
				out := call(svc, name)
				assert.Nil(t, out[len(out)-1].Interface())
				m.AssertNumberOfCalls(t, name, 1)
			})
		}
	}
}

func TestGuardPassesReadsInReadOnly(t *testing.T) {
	for _, tc := range guardCases {
		for _, name := range tc.reads {
			t.Run(tc.name+"."+name, func(t *testing.T) {
				svc, m := tc.guard(func() bool { return true })
				expect(m, tc.iface, name)
				call(svc, name)
				m.AssertNumberOfCalls(t, name, 1)
			})
		}
	}
}

func TestGuardErrorWrapsErrReadOnly(t *testing.T) {
	svc := aws.GuardS3(new(awstest.MockS3Service), func() bool { return true })
	err := svc.CopyObject(context.Background(), "a", "k", "b", "k", false)
	assert.True(t, errors.Is(err, aws.ErrReadOnly))
	assert.Equal(t, "CopyObject: blocked in ReadOnly mode", err.Error())
}

// expect makes the mock accept one call of the method with any arguments
// and return zero values.
func expect(m *mock.Mock, iface reflect.Type, name string) {
	mt, _ := iface.MethodByName(name)
	ret := make([]any, mt.Type.NumOut())
	for i := range ret {
		ret[i] = reflect.Zero(mt.Type.Out(i)).Interface()
	}
	m.On(name, anyArgs(iface, name)...).Return(ret...)
}

func anyArgs(iface reflect.Type, name string) []any {
	mt, _ := iface.MethodByName(name)
	args := make([]any, mt.Type.NumIn())
	for i := range args {
		args[i] = mock.Anything
	}
	return args
}

// call runs the method on svc with a background context and zero values for
// the other arguments.
func call(svc any, name string) []reflect.Value {
	method := reflect.ValueOf(svc).MethodByName(name)
	in := make([]reflect.Value, method.Type().NumIn())
	ctxType := reflect.TypeFor[context.Context]()
	for i := range in {
		if t := method.Type().In(i); t == ctxType {
			in[i] = reflect.ValueOf(context.Background())
		} else {
			in[i] = reflect.Zero(t)
		}
	}
	return method.Call(in)
}
