package jump

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"trims spaces", "  i-0abc1234  ", "i-0abc1234"},
		{"removes format characters", "\u200bi-0abc\ufeff1234\u200e", "i-0abc1234"},
		{"turns NBSP into a space", "orders\u00a0api", "orders api"},
		{"trims an NBSP at the end", "orders\u00a0", "orders"},
		{"removes spaces in an ARN", "arn:aws:lambda:us-east-1:123456789012: function:orders", "arn:aws:lambda:us-east-1:123456789012:function:orders"},
		{"removes spaces in an ARN with a path", "arn:aws:ecs:us-east-1:123456789012:task/ main/ abc", "arn:aws:ecs:us-east-1:123456789012:task/main/abc"},
		{"keeps spaces in an S3 key", "arn:aws:s3::: logs/my report.csv", "arn:aws:s3:::logs/my report.csv"},
		{"keeps spaces in an s3 URL", "s3://logs/my report.csv", "s3://logs/my report.csv"},
		{"removes spaces in an S3 bucket ARN", "arn:aws:s3:::lo gs", "arn:aws:s3:::logs"},
		{"removes spaces in an S3 access point ARN", "arn:aws:s3:us-east-1:123456789012:accesspoint/my ap", "arn:aws:s3:us-east-1:123456789012:accesspoint/myap"},
		{"keeps spaces in a log stream name", "arn:aws:logs:us-east-1:123456789012: log-group:app:log-stream:web server 1", "arn:aws:logs:us-east-1:123456789012:log-group:app:log-stream:web server 1"},
		{"keeps spaces in an S3 key that holds :log-stream:", "arn:aws:s3:::logs/a b:log-stream:c", "arn:aws:s3:::logs/a b:log-stream:c"},
		{"removes spaces in a log group ARN", "arn:aws:logs:us-east-1:123456789012:log-group:/aws/ lambda/x", "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/x"},
		{"keeps spaces in a name", "web server", "web server"},
		{"empty", " \u200b ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Clean(tt.in))
		})
	}
}

func TestLooks(t *testing.T) {
	for _, s := range []string{
		"arn:aws:lambda:us-east-1:123456789012:function:orders",
		"arn:nonsense",
		"s3://logs",
		"https://sqs.us-east-1.amazonaws.com/123456789012/orders",
		"http://localhost:4566/000000000000/orders",
		"i-0abc1234",
		"i-54a50a1b561509250",
		"ami-1d92558f",
		"sg-70d9dcf9fe216d2ca",
		"vpc-82c585e4bd181e0e5",
		"subnet-378f6cf069aba7523",
		"\u200b i-0abc1234",
	} {
		assert.True(t, Looks(s), s)
	}
	for _, s := range []string{
		"", "lambda", "api", "vpc-flow-logs", "/aws/lambda/orders", "tag:team=orders",
		"123456789012:function:orders", "web:3", "i-0ABC1234", "i-0abc123", "eni-0abc1234",
	} {
		assert.False(t, Looks(s), s)
	}
}

func TestParse(t *testing.T) {
	const acct = "123456789012"
	tests := []struct {
		in   string
		want Target
	}{
		// EC2 IDs.
		{"i-0abc1234", Target{Kind: Instance, ID: "i-0abc1234"}},
		{"i-54a50a1b561509250", Target{Kind: Instance, ID: "i-54a50a1b561509250"}},
		{"ami-1d92558f", Target{Kind: AMI, ID: "ami-1d92558f"}},
		{"sg-70d9dcf9fe216d2ca", Target{Kind: SecurityGroup, ID: "sg-70d9dcf9fe216d2ca"}},
		{"vpc-82c585e4bd181e0e5", Target{Kind: VPC, ID: "vpc-82c585e4bd181e0e5"}},
		{"subnet-378f6cf069aba7523", Target{Kind: Subnet, ID: "subnet-378f6cf069aba7523"}},
		{"vpc-flow-logs", Target{Kind: Name, ID: "vpc-flow-logs"}},
		{"i-0abc12345", Target{Kind: Name, ID: "i-0abc12345"}},

		// EC2 ARNs.
		{"arn:aws:ec2:us-east-1:" + acct + ":instance/i-0abc1234",
			Target{Kind: Instance, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2", ID: "i-0abc1234"}},
		{"arn:aws:ec2:us-east-1::image/ami-1d92558f",
			Target{Kind: AMI, Partition: "aws", Region: "us-east-1", Service: "ec2", ID: "ami-1d92558f"}},
		{"arn:aws:ec2:eu-west-1:" + acct + ":security-group/sg-0abc1234",
			Target{Kind: SecurityGroup, Partition: "aws", Region: "eu-west-1", Account: acct, Service: "ec2", ID: "sg-0abc1234"}},
		{"arn:aws:ec2:us-east-1:" + acct + ":vpc/vpc-0abc1234",
			Target{Kind: VPC, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2", ID: "vpc-0abc1234"}},
		{"arn:aws:ec2:us-east-1:" + acct + ":subnet/subnet-0abc1234",
			Target{Kind: Subnet, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2", ID: "subnet-0abc1234"}},
		{"arn:aws:ec2:us-east-1:" + acct + ":network-interface/eni-0abc1234",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2"}},
		{"arn:aws:ec2:us-east-1:" + acct + ":instance/sg-0abc1234",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2"}},
		{"arn:aws:ec2:us-east-1:" + acct + ":instance/",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2"}},
		{"arn:aws:ec2:us-east-1:" + acct + ":instance",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ec2"}},

		// S3.
		{"arn:aws:s3:::logs", Target{Kind: Bucket, Partition: "aws", Service: "s3", ID: "logs"}},
		{"arn:aws:s3:::logs/2026/app.log",
			Target{Kind: Object, Partition: "aws", Service: "s3", Parent: "logs", ID: "2026/app.log"}},
		{"arn:aws:s3:::logs/my report.csv",
			Target{Kind: Object, Partition: "aws", Service: "s3", Parent: "logs", ID: "my report.csv"}},
		{"arn:aws:s3:us-east-1:" + acct + ":accesspoint/ap",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "s3"}},
		{"arn:aws:s3:us-east-1::logs", Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Service: "s3"}},
		{"arn:aws:s3:::", Target{Kind: Invalid}},
		{"s3://my bucket", Target{Kind: Invalid}},
		{"s3://logs", Target{Kind: Bucket, ID: "logs"}},
		{"s3://logs/", Target{Kind: Bucket, ID: "logs"}},
		{"s3://logs/2026/", Target{Kind: Object, Parent: "logs", ID: "2026/"}},
		{"s3:///key", Target{Kind: Invalid}},

		// SQS.
		{"arn:aws:sqs:us-east-1:" + acct + ":orders",
			Target{Kind: Queue, Partition: "aws", Region: "us-east-1", Account: acct, Service: "sqs", ID: "orders"}},
		{"arn:aws:sqs:us-east-1:" + acct + ":a/b",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "sqs"}},
		{"https://sqs.eu-west-1.amazonaws.com/" + acct + "/orders.fifo",
			Target{Kind: Queue, Region: "eu-west-1", Account: acct, ID: "orders.fifo"}},
		{"https://sqs.cn-north-1.amazonaws.com.cn/" + acct + "/orders",
			Target{Kind: Queue, Region: "cn-north-1", Account: acct, ID: "orders"}},
		{"https://eu-west-1.queue.amazonaws.com/" + acct + "/orders",
			Target{Kind: Queue, Region: "eu-west-1", Account: acct, ID: "orders"}},
		{"https://queue.amazonaws.com/" + acct + "/orders",
			Target{Kind: Queue, Region: "us-east-1", Account: acct, ID: "orders"}},
		{"http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/orders",
			Target{Kind: Queue, Region: "us-east-1", Account: "000000000000", ID: "orders"}},
		{"http://localhost:4566/000000000000/orders",
			Target{Kind: Queue, Account: "000000000000", ID: "orders"}},
		{"https://sqs.us-east-1.amazonaws.com/" + acct + "/orders/",
			Target{Kind: Queue, Region: "us-east-1", Account: acct, ID: "orders"}},
		{"https://example.com/" + acct + "/orders", Target{Kind: Queue, Account: acct, ID: "orders"}},
		{"https://foo.queue.amazonaws.com/" + acct + "/orders", Target{Kind: Queue, Account: acct, ID: "orders"}},
		{"https://sqs.foo.amazonaws.com/" + acct + "/orders", Target{Kind: Queue, Account: acct, ID: "orders"}},
		{"https://example.com/docs", Target{Kind: Invalid}},
		{"https://sqs.us-east-1.amazonaws.com/123/orders", Target{Kind: Invalid}},
		{"https://sqs.us-east-1.amazonaws.com/12345678901a/orders", Target{Kind: Invalid}},
		{"https://sqs.us-east-1.amazonaws.com/" + acct + "/orders?x=1", Target{Kind: Invalid}},
		{"https://sqs.us-east-1.amazonaws.com/" + acct + "/orders#x", Target{Kind: Invalid}},
		{"https://", Target{Kind: Invalid}},

		// CloudWatch Logs.
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:/aws/lambda/orders",
			Target{Kind: LogGroup, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs", ID: "/aws/lambda/orders"}},
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:/aws/lambda/orders:*",
			Target{Kind: LogGroup, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs", ID: "/aws/lambda/orders"}},
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:app:log-stream:web/1/abc",
			Target{Kind: LogStream, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs", Parent: "app", ID: "web/1/abc"}},
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:/ecs/web:log-stream:web server 1",
			Target{Kind: LogStream, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs", Parent: "/ecs/web", ID: "web server 1"}},
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs"}},
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:a:b",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs"}},
		{"arn:aws:logs:us-east-1:" + acct + ":log-group:app:log-stream:",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs"}},
		{"arn:aws:logs:us-east-1:" + acct + ":destination:d",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "logs"}},
		{"/aws/lambda/orders", Target{Kind: LogGroup, ID: "/aws/lambda/orders"}},
		{"/ecs/web#1", Target{Kind: LogGroup, ID: "/ecs/web#1"}},
		{"/aws/lambda/my orders", Target{Kind: Invalid}},

		// Lambda.
		{"arn:aws:lambda:us-east-1:" + acct + ":function:orders",
			Target{Kind: Function, Partition: "aws", Region: "us-east-1", Account: acct, Service: "lambda", ID: "orders"}},
		{"arn:aws:lambda:us-east-1:" + acct + ":function:orders:7",
			Target{Kind: Function, Partition: "aws", Region: "us-east-1", Account: acct, Service: "lambda", ID: "orders", Qualifier: "7"}},
		{"arn:aws:lambda:us-east-1:" + acct + ":function:orders:$LATEST",
			Target{Kind: Function, Partition: "aws", Region: "us-east-1", Account: acct, Service: "lambda", ID: "orders", Qualifier: "$LATEST"}},
		{"arn:aws:lambda:us-east-1:" + acct + ":function:orders:live:x",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "lambda"}},
		{"arn:aws:lambda:us-east-1:" + acct + ":function:",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "lambda"}},
		{"arn:aws:lambda:us-east-1:" + acct + ":layer:deps:3",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "lambda"}},
		{acct + ":function:orders", Target{Kind: Function, Account: acct, ID: "orders"}},
		{acct + ":function:orders:live", Target{Kind: Function, Account: acct, ID: "orders", Qualifier: "live"}},
		{"123:function:orders", Target{Kind: Name, ID: "123:function:orders"}},

		// ECS.
		{"arn:aws:ecs:us-east-1:" + acct + ":cluster/main",
			Target{Kind: Cluster, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", ID: "main"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":service/main/web",
			Target{Kind: Service, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", Parent: "main", ID: "web"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":service/web",
			Target{Kind: Service, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", ID: "web"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":task/main/0abc",
			Target{Kind: Task, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", Parent: "main", ID: "0abc"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":task/0abc",
			Target{Kind: Task, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", ID: "0abc"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":task-definition/web:3",
			Target{Kind: TaskDefinition, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", ID: "web", Qualifier: "3"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":task-definition/web",
			Target{Kind: TaskDefinition, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs", ID: "web"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":task-definition/web:",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":cluster/a/b",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":service/a/b/c",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":task//0abc",
			Target{Kind: Invalid, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":container-instance/main/0abc",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs"}},
		{"arn:aws:ecs:us-east-1:" + acct + ":container/main/0abc/web",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "ecs"}},

		// Other ARNs and partitions.
		{"arn:aws:rds:us-east-1:" + acct + ":db:orders",
			Target{Kind: Unsupported, Partition: "aws", Region: "us-east-1", Account: acct, Service: "rds"}},
		{"arn:aws-cn:lambda:cn-north-1:" + acct + ":function:orders",
			Target{Kind: Function, Partition: "aws-cn", Region: "cn-north-1", Account: acct, Service: "lambda", ID: "orders"}},
		{"arn:aws:lambda", Target{Kind: Invalid}},
		{"arn:aws:::::", Target{Kind: Invalid}},
		{"arn:aws::us-east-1:" + acct + ":foo", Target{Kind: Invalid}},
		{"arn::lambda:us-east-1:" + acct + ":function:orders", Target{Kind: Invalid}},
		{"arn:aws:sqs:us-east-1:" + acct + ":", Target{Kind: Invalid}},
		{"arn:aws:sqs:us-east-1:abc:orders", Target{Kind: Invalid}},

		// Tags, names and empty text.
		{"tag:team=orders", Target{Kind: Tags, Tags: []Tag{{Key: "team", Value: "orders", HasValue: true}}}},
		{"tag:team=orders tag:env", Target{Kind: Tags, Tags: []Tag{
			{Key: "team", Value: "orders", HasValue: true}, {Key: "env"}}}},
		{"tag:team=", Target{Kind: Tags, Tags: []Tag{{Key: "team", HasValue: true}}}},
		{"tag:=orders", Target{Kind: Invalid}},
		{"tag:team=orders web", Target{Kind: Invalid}},
		{"orders-api", Target{Kind: Name, ID: "orders-api"}},
		{"web:3", Target{Kind: Name, ID: "web", Qualifier: "3"}},
		{"web:", Target{Kind: Name, ID: "web:"}},
		{"a:b:c", Target{Kind: Name, ID: "a:b:c"}},
		{"web server", Target{Kind: Name, ID: "web server"}},
		{"", Target{Kind: Empty}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			want := tt.want
			want.Raw = Clean(tt.in)
			assert.Equal(t, want, Parse(tt.in))
		})
	}
}

// A long S3 object ARN parses in full. The command input keeps 2,048 runes
// (H7).
func TestParse_LongObjectARN(t *testing.T) {
	key := strings.Repeat("a/", 900) + "file.txt"
	got := Parse("arn:aws:s3:::logs/" + key)
	assert.Equal(t, Object, got.Kind)
	assert.Equal(t, key, got.ID)
}

// Text copied from a web page parses as if it were typed.
func TestParse_CleansTheText(t *testing.T) {
	got := Parse("\u200barn:aws:lambda:us-east-1:123456789012:\u00a0function:orders\ufeff")
	assert.Equal(t, Function, got.Kind)
	assert.Equal(t, "orders", got.ID)
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:orders", got.Raw)
}

func TestKindNoun(t *testing.T) {
	assert.Equal(t, "Lambda function", Function.Noun())
	assert.Equal(t, "EC2 instance", Instance.Noun())
	for k := Instance; k <= TaskDefinition; k++ {
		assert.NotEmpty(t, k.Noun(), "kind %d", k)
	}
	for _, k := range []Kind{Empty, Invalid, Unsupported, Name, Tags} {
		assert.Empty(t, k.Noun(), "kind %d", k)
	}
}
