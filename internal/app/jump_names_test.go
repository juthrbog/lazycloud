package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/jump"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Jump PR 5 (docs/design/jump.md, "Names (PR 5)"): :go NAME.

// missAll makes every read by name miss, except the ones that the test
// sets first. testify takes the first matching expectation, so a test sets
// its hits before it calls missAll.
func missAll(ec2 *awstest.MockEC2Service, s3 *awstest.MockS3Service, sqs *awstest.MockSQSService,
	logs *awstest.MockLogsService, lambda *awstest.MockLambdaService, ecs *awstest.MockECSService) {
	any := mock.Anything
	notFound := apiErr("ResourceNotFoundException", "x")
	lambda.On("GetFunction", any, any, any).Return(nil, notFound).Maybe()
	ecs.On("DescribeCluster", any, any).Return(nil, aws.ErrClusterNotFound).Maybe()
	ecs.On("DescribeTaskDefinition", any, any).Return(nil, apiErr("ClientException", "Unable to describe task definition.")).Maybe()
	sqs.On("GetQueueURL", any, any, any).Return("", aws.ErrQueueNotFound).Maybe()
	s3.On("HeadBucket", any, any, any).Return(aws.ErrBucketNotFound).Maybe()
	logs.On("GetLogGroup", any, any).Return(nil, aws.ErrLogGroupNotFound).Maybe()
	ec2.On("FindSecurityGroupsByName", any, any).Return([]aws.SecurityGroup(nil), nil).Maybe()
	ec2.On("FindInstancesByName", any, any).Return([]aws.Instance(nil), nil).Maybe()
}

func TestNameReadsFollowTheNameRules(t *testing.T) {
	services := func(text string) []string {
		var out []string
		for _, r := range nameReads(aws.Services{}, jump.Parse(text), jump.Session{Region: "us-east-1"}) {
			out = append(out, r.service)
		}
		return out
	}
	all := []string{"Lambda", "ECS", "ECS", "SQS", "S3", "CloudWatch Logs", "EC2", "EC2"}
	assert.Equal(t, all, services("orders-api"))
	assert.Equal(t, []string{"Lambda", "ECS", "ECS", "SQS", "CloudWatch Logs", "EC2", "EC2"}, services("Orders-api"), "no bucket has upper case")
	assert.Equal(t, []string{"SQS", "S3", "CloudWatch Logs", "EC2", "EC2"}, services("orders.fifo"))
	assert.Equal(t, []string{"EC2", "EC2"}, services("web server"), "only a Name tag or a group name can hold a space")
	assert.Equal(t, []string{"Lambda", "ECS", "EC2", "EC2"}, services("web:3"), "NAME:SUFFIX asks Lambda and ECS")
	assert.Equal(t, []string{"EC2", "EC2"}, services("app:v1.2"), "no Lambda qualifier has a dot")
	assert.Equal(t, []string{"Lambda", "EC2", "EC2"}, services("web:$LATEST"))
	long := strings.Repeat("q", 80)
	assert.Contains(t, services(long), "SQS", "a standard queue name has up to 80 characters")
	assert.NotContains(t, services(long+"q"), "SQS")
	assert.Contains(t, services(strings.Repeat("q", 75)+".fifo"), "SQS")
	assert.NotContains(t, services(strings.Repeat("q", 76)+".fifo"), "SQS")
	assert.Equal(t, []string{"Lambda", "EC2", "EC2"}, services("web:live"), "a revision is a number")
	assert.Equal(t, []string{"Lambda", "ECS", "ECS", "SQS", "CloudWatch Logs", "EC2", "EC2"}, services("a-x_y"),
		"an underscore is no bucket name")
	assert.Equal(t, []string{"CloudWatch Logs", "EC2", "EC2"}, services("a..b"), "dots: no bucket (two in a row), Lambda, ECS or SQS")
}

func TestJumpByNameOneMatchOpensIt(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	fnARN := "arn:aws:lambda:us-east-1:" + jumpAcct + ":function:orders-api"
	lambda.On("GetFunction", mock.Anything, "orders-api", "").
		Return(&aws.FunctionDetail{Function: aws.Function{Name: "orders-api", ARN: fnARN}}, nil)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	s := jump.Session{Region: "us-east-1", Account: jumpAcct}
	r := nameResult(context.Background(), context.Background(), svc, jump.Parse("orders-api"), s, jumpResultMsg{})
	require.NotNil(t, r.nav)
	assert.Equal(t, msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": fnARN}}, *r.nav)
	assert.Empty(t, r.cands)
	s3.AssertCalled(t, "HeadBucket", mock.Anything, "orders-api", jumpAcct)
	ec2.AssertCalled(t, "FindInstancesByName", mock.Anything, "orders-api")
}

func TestJumpByNameSeveralMatches(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	any := mock.Anything
	ecs.On("DescribeCluster", any, "web").Return(&aws.Cluster{Name: "web", ARN: "arn:cluster/web"}, nil)
	ecs.On("DescribeTaskDefinition", any, "web").Return(&aws.TaskDefinition{Family: "web", ARN: "arn:td/web:3"}, nil)
	ec2.On("FindInstancesByName", any, "web").Return([]aws.Instance{{ID: "i-1", State: "running"}, {ID: "i-2", State: "stopped"}}, nil)
	ec2.On("FindSecurityGroupsByName", any, "web").Return([]aws.SecurityGroup{{ID: "sg-1", VpcID: "vpc-1"}}, nil)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	r := nameResult(context.Background(), context.Background(), svc, jump.Parse("web"), jump.Session{Region: "us-east-1"}, jumpResultMsg{})
	assert.Nil(t, r.nav)
	assert.Equal(t, "web", r.name)
	var labels []string
	for _, c := range r.cands {
		labels = append(labels, c.label)
	}
	assert.Equal(t, []string{"ECS cluster", "task definition", "Security group sg-1 in vpc-1",
		"EC2 instance i-1 (running)", "EC2 instance i-2 (stopped)"}, labels, "in the order of the reads")
	assert.Equal(t, msg.NavigateMsg{ViewID: "ec2_list", Params: map[string]string{"focus": "i-2"}}, r.cands[4].nav)
}

func TestJumpByNameQualifier(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	any := mock.Anything
	fn := &aws.FunctionDetail{Function: aws.Function{Name: "web", ARN: "arn:fn/web"}}
	lambda.On("GetFunction", any, "web", "3").Return(fn, nil)
	ecs.On("DescribeTaskDefinition", any, "web:3").Return(&aws.TaskDefinition{Family: "web", ARN: "arn:td/web:3"}, nil)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	r := nameResult(context.Background(), context.Background(), svc, jump.Parse("web:3"), jump.Session{Region: "us-east-1"}, jumpResultMsg{})
	require.Len(t, r.cands, 2)
	assert.Equal(t, "Lambda function web:3", r.cands[0].label)
	assert.Equal(t, "lambda_versions", r.cands[0].nav.ViewID)
	assert.Equal(t, "task definition web:3", r.cands[1].label)
	ec2.AssertCalled(t, "FindInstancesByName", any, "web:3")
	ec2.AssertCalled(t, "FindSecurityGroupsByName", any, "web:3")
	ecs.AssertNotCalled(t, "DescribeCluster", any, any)
}

func TestJumpByNameMiss(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	any := mock.Anything
	// A bucket of another account is a miss: the name is taken elsewhere.
	s3.On("HeadBucket", any, "nothing", "").Return(aws.ErrBucketDenied)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	s := jump.Session{Region: "eu-west-1"}
	r := nameResult(context.Background(), context.Background(), svc, jump.Parse("nothing"), s, jumpResultMsg{})
	assert.Equal(t, "No resource named nothing in eu-west-1.", r.text)
}

// A failed read is named when nothing matched. When something matched, the
// failure goes to the event log only.
func TestJumpByNameFailedReads(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	any := mock.Anything
	lambda.On("GetFunction", any, "nothing", "").Return(nil, apiErr("AccessDeniedException", "no"))
	ec2.On("FindInstancesByName", any, "nothing").Return([]aws.Instance(nil), apiErr("UnauthorizedOperation", "no"))
	ec2.On("FindSecurityGroupsByName", any, "nothing").Return([]aws.SecurityGroup(nil), apiErr("UnauthorizedOperation", "no"))
	sqs.On("GetQueueURL", any, "nothing", "").Return("", errors.New("dial tcp: connection refused"))
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	s := jump.Session{Region: "us-east-1"}
	r := nameResult(context.Background(), context.Background(), svc, jump.Parse("nothing"), s, jumpResultMsg{})
	assert.Equal(t, "No resource named nothing in us-east-1. Some reads failed: Lambda (AccessDeniedException), SQS (error), EC2 (UnauthorizedOperation).", r.text,
		"a service is named once, and an error with no code is short")

	svc, ec2, s3, sqs, logs, lambda, ecs = jumpServices(t)
	lambda.On("GetFunction", any, "q", "").Return(nil, apiErr("AccessDeniedException", "no"))
	sqs.On("GetQueueURL", any, "q", "").Return("https://sqs.us-east-1.amazonaws.com/"+jumpAcct+"/q", nil)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	r = nameResult(context.Background(), context.Background(), svc, jump.Parse("q"), s, jumpResultMsg{})
	require.NotNil(t, r.nav)
	assert.Equal(t, "sqs_queues", r.nav.ViewID)
}

// A cancel during the reads writes no warning per read to the event log.
func TestJumpByNameCancelLogsNoFailures(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	lambda.On("GetFunction", mock.Anything, "web", "").Return(nil, context.Canceled)
	ec2.On("FindInstancesByName", mock.Anything, "web").Return([]aws.Instance(nil), context.Canceled)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	_, err := lookupNames(context.Background(), svc, jump.Parse("web"), jump.Session{Region: "us-east-1"})
	assert.NoError(t, err, "a cancel is no failed read")
}

// A timeout of one read and a denied other read is no timeout message.
func TestJumpByNameMixedFailures(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	lambda.On("GetFunction", mock.Anything, "web", "").Return(nil, context.DeadlineExceeded)
	ecs.On("DescribeCluster", mock.Anything, "web").Return(nil, apiErr("AccessDeniedException", "no"))
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	r := nameResult(context.Background(), context.Background(), svc, jump.Parse("web"), jump.Session{Region: "us-east-1"}, jumpResultMsg{})
	assert.Equal(t, "No resource named web in us-east-1. Some reads failed: Lambda (timeout), ECS (AccessDeniedException).", r.text)
}

// A cancelled read shows nothing, and a timeout says so.
func TestJumpByNameCancelAndTimeout(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := nameResult(ctx, ctx, svc, jump.Parse("web"), jump.Session{Region: "us-east-1"}, jumpResultMsg{})
	assert.Equal(t, jumpResultMsg{}, r)

	svc, ec2, s3, sqs, logs, lambda, ecs = jumpServices(t)
	lambda.On("GetFunction", mock.Anything, "web", "").Return(nil, context.DeadlineExceeded)
	missAll(ec2, s3, sqs, logs, lambda, ecs)
	r = nameResult(context.Background(), context.Background(), svc, jump.Parse("web"), jump.Session{Region: "us-east-1"}, jumpResultMsg{})
	assert.Equal(t, "Finding resources named web took too long. Try again.", r.text)
}

// Several matches open a picker. A pick opens the resource; a pick after a
// session switch does nothing.
func TestJumpByNamePicker(t *testing.T) {
	m := jumpModel(t)
	cands := []nameCandidate{
		{label: "ECS cluster", nav: msg.NavigateMsg{ViewID: "ecs_clusters", Params: map[string]string{"focus": "arn:cluster/web"}}},
		{label: "EC2 instance i-1 (running)", nav: msg.NavigateMsg{ViewID: "ec2_list", Params: map[string]string{"focus": "i-1"}}},
	}
	r := jumpResultMsg{session: m.sessionLabel(), gen: m.sessionGen, name: "web", cands: cands}
	m, _ = update(t, m, r)
	require.True(t, m.picker.Visible())
	out := rendered(m)
	assert.Contains(t, out, "Several resources are named web")
	assert.Contains(t, out, "EC2 instance i-1 (running)")

	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, cmd := update(t, m, keyEnter)
	require.NotNil(t, cmd)
	m, _ = update(t, m, cmd())
	assert.Equal(t, "ec2_list", m.nav.Current().ID())

	m = jumpModel(t)
	m, _ = update(t, m, jumpResultMsg{session: m.sessionLabel(), gen: m.sessionGen, name: "web", cands: cands})
	m.sessionGen++ // a switch while the picker is open
	m, cmd = update(t, m, keyEnter)
	m, _ = update(t, m, cmd())
	assert.Equal(t, 1, m.nav.Depth())
	assert.Contains(t, rendered(m), "The session changed, so the jump did not run.")
}

// :go NAME runs the reads as one operation with its own label.
func TestJumpByNameCommand(t *testing.T) {
	m := jumpModel(t)
	missAll(m.svc.EC2.(*awstest.MockEC2Service), m.svc.S3.(*awstest.MockS3Service), m.svc.SQS.(*awstest.MockSQSService),
		m.svc.Logs.(*awstest.MockLogsService), m.svc.Lambda.(*awstest.MockLambdaService), m.svc.ECS.(*awstest.MockECSService))
	m, cmd := m.executeCommand("go web server")
	assert.Equal(t, 1, m.ops.Count())
	assert.Contains(t, rendered(m), "Find resources named web server")
	r := findOf[jumpResultMsg](collectMsgs(cmd))
	require.NotNil(t, r)
	m, _ = update(t, m, r)
	assert.Contains(t, rendered(m), "No resource named web server in us-east-1.")
}
