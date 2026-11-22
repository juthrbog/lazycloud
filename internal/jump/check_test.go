package jump

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestPartitionOf(t *testing.T) {
	for region, want := range map[string]string{
		"us-east-1":       "aws",
		"eu-west-1":       "aws",
		"il-central-1":    "aws",
		"cn-north-1":      "aws-cn",
		"cn-northwest-1":  "aws-cn",
		"us-gov-west-1":   "aws-us-gov",
		"us-iso-east-1":   "aws-iso",
		"us-isob-east-1":  "aws-iso-b",
		"eu-isoe-west-1":  "aws-iso-e",
		"us-isof-south-1": "aws-iso-f",
		"eusc-de-east-1":  "aws-eusc",
		"moon-base-1":     "aws",
		"":                "aws",
	} {
		assert.Equal(t, want, PartitionOf(region), region)
	}
}

func TestCheck(t *testing.T) {
	const own, other = "111111111111", "222222222222"
	s := Session{Region: "us-east-1", Account: own, Profiles: map[string][]string{
		other:          {"prod"},
		"333333333333": {"audit", "audit-admin"},
	}}
	tests := []struct {
		name string
		in   string
		s    Session
		want string
	}{
		{"own account", "arn:aws:lambda:us-east-1:" + own + ":function:orders", s, ""},
		{"no account in the target", "i-0abc1234", s, ""},
		{"image ARN with no account", "arn:aws:ec2:us-east-1::image/ami-0abc1234", s, ""},
		{"S3 ARN", "arn:aws:s3:::logs/a", s, ""},
		{"other Region is no refusal", "arn:aws:lambda:eu-west-1:" + own + ":function:orders", s, ""},
		{"other account with a profile", "arn:aws:lambda:us-east-1:" + other + ":function:orders", s,
			"The resource is in account 222222222222, the profile prod. Press P to switch."},
		{"other account with two profiles", "arn:aws:lambda:us-east-1:333333333333:function:orders", s,
			"The resource is in account 333333333333, the profiles audit, audit-admin. Press P to switch."},
		{"other account with no profile", "arn:aws:lambda:us-east-1:444444444444:function:orders", s,
			"The resource is in account 444444444444. The session uses account 111111111111."},
		{"other account from a queue URL", "https://sqs.us-east-1.amazonaws.com/" + other + "/orders", s,
			"The resource is in account 222222222222, the profile prod. Press P to switch."},
		{"account not known yet", "arn:aws:lambda:us-east-1:" + other + ":function:orders",
			Session{Region: "us-east-1"}, ""},
		{"LocalStack skips the account", "arn:aws:lambda:us-east-1:000000000000:function:orders",
			Session{Region: "us-east-1", Account: own, Endpoint: true}, ""},
		{"other partition", "arn:aws-cn:lambda:cn-north-1:" + own + ":function:orders", s,
			"The resource is in the aws-cn partition. The session uses aws."},
		{"other partition from a queue URL", "https://sqs.cn-north-1.amazonaws.com.cn/" + own + "/orders", s,
			"The resource is in the aws-cn partition. The session uses aws."},
		{"other partition with no Region", "arn:aws-cn:s3:::logs", s,
			"The resource is in the aws-cn partition. The session uses aws."},
		{"partition before account", "arn:aws-cn:lambda:cn-north-1:" + other + ":function:orders", s,
			"The resource is in the aws-cn partition. The session uses aws."},
		{"GovCloud session", "arn:aws-us-gov:lambda:us-gov-west-1:" + own + ":function:orders",
			Session{Region: "us-gov-east-1", Account: own}, ""},
		{"partition with an endpoint", "arn:aws-cn:lambda:cn-north-1:000000000000:function:orders",
			Session{Region: "us-east-1", Endpoint: true},
			"The resource is in the aws-cn partition. The session uses aws."},
		{"empty", "  ", s, "Type what to jump to. " + Forms},
		{"invalid", "arn:aws:lambda", s, `LazyCloud cannot read "arn:aws:lambda". ` + Forms},
		{"unsupported", "arn:aws:rds:us-east-1:" + own + ":db:orders", s,
			"LazyCloud has no view for rds resources: arn:aws:rds:us-east-1:111111111111:db:orders"},
		{"name", "orders-api", s, "LazyCloud cannot find a resource by name yet. " + Forms},
		{"tags", "tag:team=orders", s, "LazyCloud cannot search by tag yet. " + Forms},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Check(Parse(tt.in), tt.s))
		})
	}
}

// A message shows at most 120 runes of the text, so a long paste does not
// fill the screen.
func TestCheck_CutsALongText(t *testing.T) {
	raw := "arn:aws:rds:us-east-1:111111111111:db:" + strings.Repeat("x", 300)
	msg := Check(Parse(raw), Session{Region: "us-east-1"})
	assert.Equal(t, "LazyCloud has no view for rds resources: "+raw[:119]+"…", msg)

	// The cut counts runes, not bytes, so it never splits a character.
	raw = "arn:aws:rds:us-east-1:111111111111:db:" + strings.Repeat("é", 300)
	msg = Check(Parse(raw), Session{Region: "us-east-1"})
	want := "arn:aws:rds:us-east-1:111111111111:db:" + strings.Repeat("é", 119-38) + "…"
	assert.Equal(t, "LazyCloud has no view for rds resources: "+want, msg)
	assert.True(t, utf8.ValidString(msg))
}

func TestOtherRegion(t *testing.T) {
	s := Session{Region: "us-east-1"}
	for in, want := range map[string]string{
		"arn:aws:lambda:eu-west-1:111111111111:function:orders":   "eu-west-1",
		"https://sqs.eu-west-1.amazonaws.com/111111111111/orders": "eu-west-1",
		"arn:aws:lambda:us-east-1:111111111111:function:orders":   "",
		"i-0abc1234": "",
		"http://localhost:4566/000000000000/orders": "",
		"arn:aws:s3:::logs":                         "",
		"s3://logs/a":                               "",
	} {
		assert.Equal(t, want, OtherRegion(Parse(in), s), in)
	}
	// An S3 target never asks for a switch, also when it has a Region.
	assert.Empty(t, OtherRegion(Target{Kind: Object, Region: "eu-west-1"}, s))
	assert.Empty(t, OtherRegion(Target{Kind: Bucket, Region: "eu-west-1"}, s))
}
