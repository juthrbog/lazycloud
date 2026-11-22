package app

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/jump"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Jump PR 3 (docs/design/jump.md): :go and the bare form, the checks, the
// lookup and the routes.

const jumpAcct = "111122223333"

// jumpServices returns mocks for the reads of a jump.
func jumpServices(t *testing.T) (aws.Services, *awstest.MockEC2Service, *awstest.MockS3Service, *awstest.MockSQSService,
	*awstest.MockLogsService, *awstest.MockLambdaService, *awstest.MockECSService) {
	t.Helper()
	ec2, s3, sqs := new(awstest.MockEC2Service), new(awstest.MockS3Service), new(awstest.MockSQSService)
	logs, lambda, ecs := new(awstest.MockLogsService), new(awstest.MockLambdaService), new(awstest.MockECSService)
	for _, m := range []interface{ Test(mock.TestingT) }{ec2, s3, sqs, logs, lambda, ecs} {
		m.Test(t)
	}
	return aws.Services{EC2: ec2, S3: s3, SQS: sqs, Logs: logs, Lambda: lambda, ECS: ecs}, ec2, s3, sqs, logs, lambda, ecs
}

func apiErr(code, message string) error {
	return &smithy.GenericAPIError{Code: code, Message: message}
}

func TestJumpLookupRoutes(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	s := jump.Session{Region: "us-east-1", Account: jumpAcct}
	any := mock.Anything

	ec2.On("GetInstanceDetail", any, "i-0abc1234").Return(&aws.InstanceDetail{}, nil)
	ec2.On("RefreshAMIs", any, []string{"ami-0abc1234"}).Return([]aws.AMI{{ID: "ami-0abc1234"}}, nil)
	ec2.On("GetSecurityGroup", any, "sg-0abc1234").Return(&aws.SecurityGroup{ID: "sg-0abc1234"}, nil)
	ec2.On("GetVPC", any, "vpc-0abc1234").Return(&aws.VPC{ID: "vpc-0abc1234"}, nil)
	ec2.On("GetSubnet", any, "subnet-0abc1234").Return(&aws.Subnet{ID: "subnet-0abc1234"}, nil)
	s3.On("HeadBucket", any, "logs", jumpAcct).Return(nil)
	sqs.On("GetQueueURL", any, "orders", jumpAcct).Return("https://sqs.us-east-1.amazonaws.com/"+jumpAcct+"/orders", nil)
	group := aws.LogGroup{Name: "/aws/lambda/orders", ARN: "arn:aws:logs:us-east-1:" + jumpAcct + ":log-group:/aws/lambda/orders:*", Class: "STANDARD"}
	logs.On("GetLogGroup", any, "/aws/lambda/orders").Return(&group, nil)
	fnARN := "arn:aws:lambda:us-east-1:" + jumpAcct + ":function:orders"
	fn := &aws.FunctionDetail{Function: aws.Function{Name: "orders", ARN: fnARN}}
	lambda.On("GetFunction", any, "orders", "").Return(fn, nil)
	lambda.On("GetFunction", any, "orders", "3").Return(fn, nil)
	lambda.On("GetFunction", any, "orders", "$LATEST").Return(fn, nil)
	lambda.On("GetFunction", any, "orders", "live").Return(fn, nil)
	clusterARN := "arn:aws:ecs:us-east-1:" + jumpAcct + ":cluster/main"
	ecs.On("DescribeCluster", any, "main").Return(&aws.Cluster{Name: "main", ARN: clusterARN}, nil)
	svcARN := "arn:aws:ecs:us-east-1:" + jumpAcct + ":service/main/web"
	ecs.On("DescribeService", any, "main", "web").Return(&aws.ClusterService{Name: "web", ARN: svcARN, ClusterARN: clusterARN}, nil)
	defaultARN := "arn:aws:ecs:us-east-1:" + jumpAcct + ":cluster/default"
	ecs.On("DescribeService", any, "default", "api").Return(&aws.ClusterService{Name: "api", ARN: "arn:svc/api", ClusterARN: defaultARN}, nil)
	taskARN := "arn:aws:ecs:us-east-1:" + jumpAcct + ":task/main/0abc"
	ecs.On("DescribeTask", any, "main", "0abc").Return(&aws.Task{ARN: taskARN, ID: "0abc", ClusterARN: clusterARN}, nil)
	tdARN := "arn:aws:ecs:us-east-1:" + jumpAcct + ":task-definition/web:3"
	ecs.On("DescribeTaskDefinition", any, "web:3").Return(&aws.TaskDefinition{ARN: tdARN, Family: "web"}, nil)

	child := map[string]string{"function": "orders", "log_group": "/aws/lambda/orders"}
	with := func(p map[string]string, k, v string) map[string]string {
		out := maps.Clone(p)
		out[k] = v
		return out
	}
	tests := []struct {
		in   string
		want msg.NavigateMsg
	}{
		{"i-0abc1234", msg.NavigateMsg{ViewID: "ec2_list", Params: map[string]string{"focus": "i-0abc1234"}}},
		{"ami-0abc1234", msg.NavigateMsg{ViewID: "ami_list", Params: map[string]string{"focus": "ami-0abc1234"}}},
		{"sg-0abc1234", msg.NavigateMsg{ViewID: "sg_list", Params: map[string]string{"focus": "sg-0abc1234"}}},
		{"vpc-0abc1234", msg.NavigateMsg{ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-0abc1234"}}},
		{"subnet-0abc1234", msg.NavigateMsg{ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-0abc1234"}}},
		{"s3://logs", msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "logs", "prefix": ""}}},
		{"arn:aws:s3:::logs/2026/11/app.log", msg.NavigateMsg{ViewID: "s3_objects",
			Params: map[string]string{"bucket": "logs", "prefix": "2026/11/", "focus": "2026/11/app.log"}}},
		{"s3://logs/top.txt", msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "logs", "prefix": "", "focus": "top.txt"}}},
		{"s3://logs/2026/11/", msg.NavigateMsg{ViewID: "s3_objects", Params: map[string]string{"bucket": "logs", "prefix": "2026/", "focus": "2026/11/"}}},
		{"https://sqs.us-east-1.amazonaws.com/" + jumpAcct + "/orders", msg.NavigateMsg{ViewID: "sqs_queues",
			Params: map[string]string{"focus": "https://sqs.us-east-1.amazonaws.com/" + jumpAcct + "/orders"}}},
		{"/aws/lambda/orders", msg.NavigateMsg{ViewID: "logs_events", Params: map[string]string{
			"log_group": "/aws/lambda/orders", "log_group_arn": group.ARN, "log_class": "STANDARD", "log_retention": "0"}}},
		{"arn:aws:logs:us-east-1:" + jumpAcct + ":log-group:/aws/lambda/orders:log-stream:2026/11/22/[$LATEST]abc",
			msg.NavigateMsg{ViewID: "logs_events", Params: map[string]string{
				"log_group": "/aws/lambda/orders", "log_group_arn": group.ARN, "log_class": "STANDARD", "log_retention": "0",
				"log_stream": "2026/11/22/[$LATEST]abc"}}},
		{fnARN, msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": fnARN}}},
		{fnARN + ":3", msg.NavigateMsg{ViewID: "lambda_versions", Params: with(child, "focus", "3")}},
		{fnARN + ":$LATEST", msg.NavigateMsg{ViewID: "lambda_versions", Params: with(child, "focus", "$LATEST")}},
		{fnARN + ":live", msg.NavigateMsg{ViewID: "lambda_aliases", Params: with(child, "focus", "live")}},
		{jumpAcct + ":function:orders", msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": fnARN}}},
		{clusterARN, msg.NavigateMsg{ViewID: "ecs_clusters", Params: map[string]string{"focus": clusterARN}}},
		{svcARN, msg.NavigateMsg{ViewID: "ecs_services", Params: map[string]string{"cluster": clusterARN, "focus": svcARN}}},
		{"arn:aws:ecs:us-east-1:" + jumpAcct + ":service/api", msg.NavigateMsg{ViewID: "ecs_services",
			Params: map[string]string{"cluster": defaultARN, "focus": "arn:svc/api"}}},
		{taskARN, msg.NavigateMsg{ViewID: "ecs_tasks", Params: map[string]string{"cluster": clusterARN, "focus": taskARN}}},
		{tdARN, msg.NavigateMsg{ViewID: "ecs_task_definitions", Params: map[string]string{"family": "web", "focus": tdARN}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := lookup(context.Background(), svc, jump.Parse(tt.in), s)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			// Each route builds.
			_, ok := routes[got.ViewID]
			assert.True(t, ok, got.ViewID)
		})
	}
}

func TestJumpLookupMisses(t *testing.T) {
	svc, ec2, s3, sqs, logs, lambda, ecs := jumpServices(t)
	s := jump.Session{Region: "eu-west-1"}
	any := mock.Anything
	ec2.On("GetInstanceDetail", any, "i-0abc1234").Return(nil, apiErr("InvalidInstanceID.NotFound", "x"))
	ec2.On("GetInstanceDetail", any, "i-0abc5678").Return(nil, nil)
	ec2.On("RefreshAMIs", any, []string{"ami-0abc1234"}).Return([]aws.AMI{}, nil)
	ec2.On("GetSecurityGroup", any, "sg-0abc1234").Return(nil, apiErr("InvalidGroup.NotFound", "x"))
	ec2.On("GetVPC", any, "vpc-0abc1234").Return(nil, apiErr("InvalidVpcID.NotFound", "x"))
	ec2.On("GetSubnet", any, "subnet-0abc1234").Return(nil, apiErr("InvalidSubnetID.NotFound", "x"))
	s3.On("HeadBucket", any, "gone", "").Return(aws.ErrBucketNotFound)
	s3.On("HeadBucket", any, "theirs", "").Return(aws.ErrBucketDenied)
	sqs.On("GetQueueURL", any, "orders", "").Return("", aws.ErrQueueNotFound)
	logs.On("GetLogGroup", any, "/nope").Return(nil, aws.ErrLogGroupNotFound)
	lambda.On("GetFunction", any, "orders", "").Return(nil, apiErr("ResourceNotFoundException", "Function not found"))
	lambda.On("GetFunction", any, "denied", "").Return(nil, apiErr("AccessDeniedException", "not allowed"))
	ecs.On("DescribeCluster", any, "main").Return(nil, aws.ErrClusterNotFound)
	// ECS answers a cluster that does not exist with ClusterNotFoundException.
	ecs.On("DescribeService", any, "default", "api").Return(nil, apiErr("ClusterNotFoundException", "Cluster not found."))
	ecs.On("DescribeService", any, "gone", "web").Return(nil, apiErr("ClusterNotFoundException", "Cluster not found."))
	ecs.On("DescribeTask", any, "default", "0def").Return(nil, apiErr("ClusterNotFoundException", "Cluster not found."))
	ecs.On("DescribeService", any, "main", "web").Return(nil, aws.ErrServiceNotFound)
	ecs.On("DescribeTask", any, "main", "0abc").Return(nil, aws.ErrTaskNotFound)
	ecs.On("DescribeTaskDefinition", any, "web:9").Return(nil, apiErr("ClientException", "Unable to describe task definition."))

	tests := []struct{ in, want string }{
		{"i-0abc1234", "No EC2 instance i-0abc1234 in eu-west-1."},
		{"i-0abc5678", "No EC2 instance i-0abc5678 in eu-west-1."},
		{"ami-0abc1234", "No AMI ami-0abc1234 in eu-west-1."},
		{"sg-0abc1234", "No security group sg-0abc1234 in eu-west-1."},
		{"vpc-0abc1234", "No VPC vpc-0abc1234 in eu-west-1."},
		{"subnet-0abc1234", "No subnet subnet-0abc1234 in eu-west-1."},
		{"s3://gone", "No bucket named gone."},
		{"s3://gone/a/b.txt", "No bucket named gone."},
		{"s3://theirs", "Bucket theirs is not in this account, or access is denied."},
		{"arn:aws:sqs:eu-west-1:" + jumpAcct + ":orders", ""},
		{"/nope", "No log group named /nope in eu-west-1."},
		{"orders-not-a-name", ""},
		{"arn:aws:lambda:eu-west-1:" + jumpAcct + ":function:orders", "No Lambda function named orders in eu-west-1."},
		{"arn:aws:lambda:eu-west-1:" + jumpAcct + ":function:denied", "access denied (AccessDeniedException). The credentials of this profile do not allow this call"},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":cluster/main", "No ECS cluster named main in eu-west-1."},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":service/main/web", "No ECS service named web in eu-west-1."},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":service/api",
			"The ARN has no cluster, and the default cluster has no ECS service api in eu-west-1. Use the long ARN, with the cluster."},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":service/gone/web", "No ECS service named web in eu-west-1."},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":task/0def",
			"The ARN has no cluster, and the default cluster has no ECS task 0def in eu-west-1. Use the long ARN, with the cluster."},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":task/main/0abc", "No ECS task 0abc in eu-west-1."},
		{"arn:aws:ecs:eu-west-1:" + jumpAcct + ":task-definition/web:9", "No task definition named web:9 in eu-west-1."},
	}
	for _, tt := range tests {
		if tt.want == "" {
			continue
		}
		t.Run(tt.in, func(t *testing.T) {
			target := jump.Parse(tt.in)
			_, err := lookup(context.Background(), svc, target, s)
			require.Error(t, err)
			assert.Equal(t, tt.want, lookupError(target, s, err))
		})
	}
	// A queue ARN asks for the queue of its account.
	sqs.On("GetQueueURL", any, "orders", jumpAcct).Return("", aws.ErrQueueNotFound)
	target := jump.Parse("arn:aws:sqs:eu-west-1:" + jumpAcct + ":orders")
	_, err := lookup(context.Background(), svc, target, s)
	assert.Equal(t, "No SQS queue named orders in eu-west-1.", lookupError(target, s, err))
	// A timeout says so.
	assert.Equal(t, "Finding ECS cluster main took too long. Try again.",
		lookupError(jump.Parse("arn:aws:ecs:eu-west-1:"+jumpAcct+":cluster/main"), s, context.DeadlineExceeded))
}

func TestTargetName(t *testing.T) {
	assert.Equal(t, "s3://logs/a b.txt", targetName(jump.Parse("s3://logs/a b.txt")))
	assert.Equal(t, "web/1 in /ecs/web", targetName(jump.Parse("arn:aws:logs:us-east-1:"+jumpAcct+":log-group:/ecs/web:log-stream:web/1")))
	assert.Equal(t, "orders:3", targetName(jump.Parse("arn:aws:lambda:us-east-1:"+jumpAcct+":function:orders:3")))
	assert.Equal(t, "i-0abc1234", targetName(jump.Parse("i-0abc1234")))
}

func TestObjectFolder(t *testing.T) {
	for key, want := range map[string]string{
		"a.txt": "", "a/b.txt": "a/", "a/b/c.txt": "a/b/", "a/": "", "a/b/": "a/", "my dir/my file": "my dir/",
		// S3 does not clean keys, so the folder keeps "//", "." and "..".
		"/x": "/", "a//c": "a//", "a/./c": "a/./", "a/../c": "a/../",
	} {
		assert.Equal(t, want, objectFolder(key), key)
	}
}

func TestProfilesByAccount(t *testing.T) {
	got := profilesByAccount([]aws.ProfileInfo{
		{Name: "prod-read", Account: "222"}, {Name: "dev"}, {Name: "prod-admin", Account: "222"}, {Name: "audit", Account: "333"},
	})
	assert.Equal(t, map[string][]string{"222": {"prod-admin", "prod-read"}, "333": {"audit"}}, got)
}

// jumpModel is a test model on AWS (no endpoint) with a known account.
func jumpModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(200, 40)
	mockAllServices(&m)
	m.config.AWS.Endpoint = ""
	m.ident.state = ui.AccountKnown
	m.ident.id.Account = jumpAcct
	return m
}

func TestJumpCommandOpensTheResource(t *testing.T) {
	for _, input := range []string{"go i-0abc1234", "jump i-0abc1234", "i-0abc1234", "  i-0abc1234  "} {
		t.Run(input, func(t *testing.T) {
			m := jumpModel(t)
			ec2 := m.svc.EC2.(*awstest.MockEC2Service)
			ec2.On("GetInstanceDetail", mock.Anything, "i-0abc1234").Return(&aws.InstanceDetail{}, nil)
			ec2.On("ListInstancesPage", mock.Anything, mock.Anything).Return(&aws.InstancePage{}, nil).Maybe()
			m, cmd := m.executeCommand(input)
			assert.Equal(t, 1, m.ops.Count(), "the read runs as an operation")
			assert.Contains(t, rendered(m), "Find EC2 instance i-0abc1234")
			r := findOf[jumpResultMsg](collectMsgs(cmd))
			require.NotNil(t, r)
			assert.Equal(t, 0, m.ops.Count())
			m, cmd = update(t, m, r)
			assert.False(t, m.opsShown, "the operations line goes with the result")
			assert.Equal(t, "ec2_list", m.nav.Current().ID())
			assert.Equal(t, []any{msg.FocusResourceMsg{ResourceID: "i-0abc1234"}}, anyOf(findOf[msg.FocusResourceMsg](collectMsgs(cmd))))
		})
	}
}

func anyOf(m any) []any {
	if m == nil {
		return nil
	}
	return []any{m}
}

func TestJumpCommandRefusesBeforeAnyCall(t *testing.T) {
	tests := []struct{ in, want string }{
		{"go", "Type what to jump to. " + jump.Forms},
		{"arn:aws:rds:us-east-1:" + jumpAcct + ":db:orders", "LazyCloud has no view for rds resources: arn:aws:rds:us-east-1:" + jumpAcct + ":db:orders"},
		{"arn:aws-cn:lambda:cn-north-1:" + jumpAcct + ":function:orders", "The resource is in the aws-cn partition. The session uses aws."},
		{"arn:aws:lambda:us-east-1:444455556666:function:orders", "The resource is in account 444455556666. The session uses account " + jumpAcct + "."},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			awsConfigEnv(t)
			m := jumpModel(t)
			m, cmd := m.executeCommand(tt.in)
			assert.Zero(t, m.ops.Count(), "no read")
			assert.Nil(t, findOf[jumpResultMsg](collectMsgs(cmd)))
			assert.Contains(t, rendered(m), tt.want)
		})
	}
}

// A bare word is a command, not a jump (H8): only :go takes a name.
func TestJumpBareWordIsNotAJump(t *testing.T) {
	m := jumpModel(t)
	m, _ = m.executeCommand("orders-api")
	assert.Contains(t, rendered(m), "Unknown command: orders-api")
}

func TestJumpWithoutASession(t *testing.T) {
	m := jumpModel(t)
	m.svcErr = errors.New("no credentials")
	m, _ = m.executeCommand("i-0abc1234")
	assert.Zero(t, m.ops.Count())
	assert.Contains(t, rendered(m), "No AWS session")
}

func TestJumpResult(t *testing.T) {
	m := jumpModel(t)
	session, gen := m.sessionLabel(), m.sessionGen

	// A miss gives its toast and opens nothing.
	m, _ = update(t, m, jumpResultMsg{session: session, gen: gen, text: "No VPC vpc-1 in us-east-1."})
	assert.Contains(t, rendered(m), "No VPC vpc-1 in us-east-1.")
	assert.Equal(t, 1, m.nav.Depth())

	// A cancelled read shows nothing.
	before := rendered(m)
	m, cmd := update(t, m, jumpResultMsg{session: session, gen: gen})
	assert.Nil(t, cmd)
	assert.Equal(t, before, rendered(m))

	// A result of an older session is dropped, also when the user switched
	// back to the same profile and Region, so the label is the same.
	nav := msg.NavigateMsg{ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-1"}}
	m, cmd = update(t, m, jumpResultMsg{session: session, gen: gen - 1, nav: &nav})
	assert.Nil(t, cmd)
	assert.Equal(t, 1, m.nav.Depth())

	m, _ = update(t, m, jumpResultMsg{session: session, gen: gen, nav: &nav})
	assert.Equal(t, "vpc_list", m.nav.Current().ID())
}

// ctrl+x cancels the read: the context of the read ends, and the result
// then shows nothing.
func TestJumpCancel(t *testing.T) {
	m := jumpModel(t)
	started := make(chan struct{})
	m.svc.EC2.(*awstest.MockEC2Service).On("GetInstanceDetail", mock.Anything, "i-0abc1234").
		Run(func(args mock.Arguments) {
			close(started)
			<-args.Get(0).(context.Context).Done() // only a cancel ends the read
		}).Return(nil, context.Canceled)
	m, cmd := m.executeCommand("i-0abc1234")
	running := m.ops.Running()
	require.Len(t, running, 1)
	// The command is a batch of the read and the tick of the operations
	// line. The read runs in its own goroutine, as the runtime runs it.
	b, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	result := make(chan tea.Msg, 1)
	go func() { result <- b[0]() }()
	<-started
	require.True(t, m.ops.Cancel(running[0].ID))
	r := <-result
	assert.Equal(t, jumpResultMsg{session: m.sessionLabel(), gen: m.sessionGen}, r)
}

// A refusal for another account names the profiles of that account from the
// config. With an endpoint (LocalStack), the account is not checked and the
// read runs.
func TestJumpAccountRefusalNamesTheProfile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfgFile, []byte("[default]\nregion = us-east-1\n\n"+
		"[profile prod]\nsso_session = corp\nsso_account_id = 444455556666\nsso_role_name = ReadOnly\nregion = us-east-1\n\n"+
		"[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = us-east-1\n"), 0o600))
	t.Setenv("AWS_CONFIG_FILE", cfgFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")

	m := jumpModel(t)
	m, _ = m.executeCommand("arn:aws:lambda:us-east-1:444455556666:function:orders")
	assert.Zero(t, m.ops.Count())
	assert.Contains(t, rendered(m), "The resource is in account 444455556666, the profile prod. Press P to switch.")

	m = jumpModel(t)
	m.config.AWS.Endpoint = "http://localhost:4566"
	m, _ = m.executeCommand("arn:aws:lambda:us-east-1:444455556666:function:orders")
	assert.Equal(t, 1, m.ops.Count(), "LocalStack skips the account check")
}
