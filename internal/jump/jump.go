// Package jump reads the text of a jump: an ID, an ARN, a URL or a name that
// the user pastes to go to a resource (docs/design/jump.md). It makes no AWS
// call and has no UI. Parse tells what the text names, and Check tells if
// the session can open it.
package jump

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
)

// Kind is the kind of resource that a text names.
type Kind int

// The kinds of a Target.
const (
	Empty       Kind = iota // no text
	Invalid                 // an ARN or a URL that LazyCloud cannot read
	Unsupported             // a valid ARN of a resource with no view
	Instance
	AMI
	SecurityGroup
	VPC
	Subnet
	Bucket
	Object
	Queue
	LogGroup
	LogStream
	Function
	Cluster
	Service
	Task
	TaskDefinition
	Name // a bare name
	Tags // one or more tag:KEY=VALUE terms
)

var nouns = map[Kind]string{
	Instance:       "EC2 instance",
	AMI:            "AMI",
	SecurityGroup:  "security group",
	VPC:            "VPC",
	Subnet:         "subnet",
	Bucket:         "S3 bucket",
	Object:         "S3 object",
	Queue:          "SQS queue",
	LogGroup:       "log group",
	LogStream:      "log stream",
	Function:       "Lambda function",
	Cluster:        "ECS cluster",
	Service:        "ECS service",
	Task:           "ECS task",
	TaskDefinition: "task definition",
}

// Noun returns the name of the kind for a message, for example "Lambda
// function". It returns "" for Empty, Invalid, Unsupported, Name and Tags.
func (k Kind) Noun() string { return nouns[k] }

// Tag is one tag:KEY=VALUE term. An empty Value with HasValue false is the
// term tag:KEY, which matches any value of the key.
type Tag struct {
	Key      string
	Value    string
	HasValue bool
}

// Target is what a text names. An empty field means that the text does not
// name it.
type Target struct {
	Kind      Kind
	Partition string // from an ARN
	Region    string // from an ARN or a queue URL
	Account   string // from an ARN or a queue URL
	Service   string // the service of an ARN, for Unsupported
	ID        string // the main ID or name: i-..., the bucket, the key of an object, the queue name, the group name, ...
	Parent    string // the bucket of an object, the group of a stream, the cluster of a service or task
	Qualifier string // a Lambda version or alias, an ECS revision, the part after the ":" of a name
	Tags      []Tag  // the terms of a tag search
	Raw       string // the cleaned text
}

// ec2ID matches the IDs of the EC2 kinds. AWS and LocalStack make IDs of 8
// or 17 hex digits in lower case.
var ec2ID = regexp.MustCompile(`^(i|ami|sg|vpc|subnet)-[0-9a-f]{8}([0-9a-f]{9})?$`)

var ec2Kinds = map[string]Kind{
	"i":      Instance,
	"ami":    AMI,
	"sg":     SecurityGroup,
	"vpc":    VPC,
	"subnet": Subnet,
}

// ec2Types maps the resource type of an EC2 ARN to its kind and its ID
// prefix.
var ec2Types = map[string]string{
	"instance":       "i",
	"image":          "ami",
	"security-group": "sg",
	"vpc":            "vpc",
	"subnet":         "subnet",
}

// partialLambda matches ACCOUNT:function:NAME[:QUALIFIER], the partial ARN
// that Lambda takes as a function name.
var partialLambda = regexp.MustCompile(`^(\d{12}):function:([^:/\s]+)(?::([^:/\s]+))?$`)

// logGroupName matches a log group name: letters, digits and _ - / . #.
var logGroupName = regexp.MustCompile(`^[A-Za-z0-9_\-/.#]+$`)

// Clean returns the text with its spaces trimmed, its format characters
// (Unicode Cf, such as U+200B, U+FEFF and bidi marks) removed, and U+00A0
// turned into a space. Text copied from a web page often holds them. Clean
// removes the spaces from an ARN, also the space that a line break became in
// the command input. An S3 key and a log stream name can hold spaces, so a
// bucket ARN keeps the spaces after BUCKET/, and a log stream ARN keeps the
// spaces after :log-stream:.
func Clean(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\u00a0':
			b.WriteRune(' ')
		case unicode.Is(unicode.Cf, r):
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if !strings.HasPrefix(s, "arn:") {
		return s
	}
	if head, stream, ok := strings.Cut(s, ":log-stream:"); ok && arnService(compact(head)) == "logs" {
		return compact(head) + ":log-stream:" + stream
	}
	head, key, hasKey := strings.Cut(s, "/")
	if hasKey && isS3ARN(compact(head)) {
		return compact(head) + "/" + key
	}
	return compact(s)
}

// compact returns s with all of its spaces removed.
func compact(s string) string { return strings.Join(strings.Fields(s), "") }

// arnService returns the service section of an ARN, or "".
func arnService(s string) string {
	parts := strings.SplitN(s, ":", 6)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// isS3ARN reports whether head, the part of an ARN before the first "/",
// is an S3 bucket ARN: arn:PARTITION:s3:::BUCKET.
func isS3ARN(head string) bool {
	parts := strings.SplitN(head, ":", 6)
	return len(parts) == 6 && parts[2] == "s3" && parts[3] == "" && parts[4] == ""
}

// Looks reports whether text is an ARN, a URL or an EC2 ID, which the
// command bar takes as a jump with no command word. A name is not, because
// a bare word can be a command (docs/design/jump.md, H8).
func Looks(text string) bool {
	s := Clean(text)
	for _, p := range []string{"arn:", "s3://", "http://", "https://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return ec2ID.MatchString(s)
}

// Parse reads text and returns what it names. It makes no AWS call, so the
// resource can be missing.
func Parse(text string) Target {
	s := Clean(text)
	t := Target{Raw: s}
	switch {
	case s == "":
		t.Kind = Empty
	case strings.HasPrefix(s, "arn:"):
		parseARN(&t)
	case partialLambda.MatchString(s):
		m := partialLambda.FindStringSubmatch(s)
		t.Kind, t.Account, t.ID, t.Qualifier = Function, m[1], m[2], m[3]
	case strings.HasPrefix(s, "s3://"):
		parseS3(&t, strings.TrimPrefix(s, "s3://"))
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		parseQueueURL(&t)
	case ec2ID.MatchString(s):
		prefix, _, _ := strings.Cut(s, "-")
		t.Kind, t.ID = ec2Kinds[prefix], s
	case strings.HasPrefix(s, "/"):
		t.Kind = Invalid
		if logGroupName.MatchString(s) {
			t.Kind, t.ID = LogGroup, s
		}
	case strings.HasPrefix(s, "tag:"):
		parseTags(&t)
	default:
		t.Kind, t.ID = Name, s
		if name, q, ok := strings.Cut(s, ":"); ok && name != "" && q != "" && !strings.Contains(q, ":") {
			t.ID, t.Qualifier = name, q
		}
	}
	return t
}

// parseARN reads t.Raw as an ARN.
func parseARN(t *Target) {
	a, err := arn.Parse(t.Raw)
	// arn.Parse counts the sections but does not check them.
	if err != nil || a.Partition == "" || a.Service == "" || a.Resource == "" ||
		(a.AccountID != "" && !isAccount(a.AccountID)) {
		t.Kind = Invalid
		return
	}
	t.Partition, t.Region, t.Account, t.Service = a.Partition, a.Region, a.AccountID, a.Service
	t.Kind = Unsupported
	switch a.Service {
	case "ec2":
		parseEC2(t, a.Resource)
	case "s3":
		if a.Region == "" && a.AccountID == "" {
			parseS3(t, a.Resource)
		}
	case "sqs":
		if validName(a.Resource) {
			t.Kind, t.ID = Queue, a.Resource
		} else {
			t.Kind = Invalid
		}
	case "logs":
		parseLogs(t, a.Resource)
	case "lambda":
		parseLambda(t, a.Resource)
	case "ecs":
		parseECS(t, a.Resource)
	}
}

// validName reports whether s is a non-empty name with no ":", "/" or
// space, as the names in an ARN are.
func validName(s string) bool {
	return s != "" && !strings.ContainsAny(s, ":/ ")
}

// parseEC2 reads the resource of an EC2 ARN: TYPE/ID. A type with no view
// stays Unsupported.
func parseEC2(t *Target, res string) {
	typ, id, _ := strings.Cut(res, "/")
	prefix, known := ec2Types[typ]
	if !known {
		return
	}
	if !validName(id) || !strings.HasPrefix(id, prefix+"-") {
		t.Kind = Invalid
		return
	}
	t.Kind, t.ID = ec2Kinds[prefix], id
}

// parseS3 reads BUCKET or BUCKET/KEY, from an S3 ARN or an s3:// URL.
func parseS3(t *Target, res string) {
	bucket, key, _ := strings.Cut(res, "/")
	switch {
	case !validName(bucket):
		t.Kind = Invalid
	case key == "":
		t.Kind, t.ID = Bucket, bucket
	default:
		t.Kind, t.Parent, t.ID = Object, bucket, key
	}
}

// parseLogs reads log-group:NAME, log-group:NAME:*, and
// log-group:NAME:log-stream:STREAM. A group name has no ":".
func parseLogs(t *Target, res string) {
	rest, ok := strings.CutPrefix(res, "log-group:")
	if !ok {
		return
	}
	group, stream, isStream := strings.Cut(rest, ":log-stream:")
	if !isStream {
		group = strings.TrimSuffix(group, ":*")
	}
	switch {
	case group == "" || strings.Contains(group, ":"):
		t.Kind = Invalid
	case !isStream:
		t.Kind, t.ID = LogGroup, group
	case stream == "" || strings.ContainsAny(stream, ":*"):
		t.Kind = Invalid
	default:
		t.Kind, t.Parent, t.ID = LogStream, group, stream
	}
}

// parseLambda reads function:NAME and function:NAME:QUALIFIER. Other
// Lambda resources, such as layers, stay Unsupported.
func parseLambda(t *Target, res string) {
	rest, ok := strings.CutPrefix(res, "function:")
	if !ok {
		return
	}
	name, q, hasQ := strings.Cut(rest, ":")
	if !validName(name) || (hasQ && !validName(q)) {
		t.Kind = Invalid
		return
	}
	t.Kind, t.ID, t.Qualifier = Function, name, q
}

// parseECS reads cluster/NAME, service/[CLUSTER/]NAME, task/[CLUSTER/]ID
// and task-definition/FAMILY[:REVISION]. A short service or task ARN has no
// cluster. Container and container instance ARNs stay Unsupported.
func parseECS(t *Target, res string) {
	typ, rest, _ := strings.Cut(res, "/")
	parts := strings.Split(rest, "/")
	switch typ {
	case "cluster":
		if len(parts) == 1 && validName(parts[0]) {
			t.Kind, t.ID = Cluster, parts[0]
			return
		}
	case "service", "task":
		if len(parts) <= 2 && validName(parts[0]) && validName(parts[len(parts)-1]) {
			t.Kind, t.ID = Service, parts[len(parts)-1]
			if typ == "task" {
				t.Kind = Task
			}
			if len(parts) == 2 {
				t.Parent = parts[0]
			}
			return
		}
	case "task-definition":
		family, rev, hasRev := strings.Cut(rest, ":")
		if validName(family) && (!hasRev || validName(rev)) {
			t.Kind, t.ID, t.Qualifier = TaskDefinition, family, rev
			return
		}
	default:
		return
	}
	t.Kind = Invalid
}

// parseQueueURL reads an SQS queue URL: https://HOST/ACCOUNT/NAME. The
// Region comes from the host, when the host names one.
func parseQueueURL(t *Target) {
	t.Kind = Invalid
	u, err := url.Parse(t.Raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || !isAccount(parts[0]) || !validName(parts[1]) {
		return
	}
	t.Kind, t.Account, t.ID = Queue, parts[0], parts[1]
	t.Region = queueRegion(u.Hostname())
}

// isAccount reports whether s is an account ID: 12 digits.
func isAccount(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// queueRegion returns the Region in the host of a queue URL, or "" when the
// host names none. The hosts are sqs.REGION.amazonaws.com (and .com.cn),
// sqs.REGION.localhost.localstack.cloud, and the old forms
// REGION.queue.amazonaws.com and queue.amazonaws.com (us-east-1).
func queueRegion(host string) string {
	host = strings.ToLower(host)
	if host == "queue.amazonaws.com" {
		return "us-east-1"
	}
	if region, ok := strings.CutSuffix(host, ".queue.amazonaws.com"); ok && validRegion(region) {
		return region
	}
	for _, suffix := range []string{".amazonaws.com", ".amazonaws.com.cn", ".localhost.localstack.cloud"} {
		if rest, ok := strings.CutSuffix(host, suffix); ok {
			if region, ok := strings.CutPrefix(rest, "sqs."); ok && validRegion(region) {
				return region
			}
		}
	}
	return ""
}

// region matches the form of a Region name, such as us-east-1 or
// us-gov-west-1.
var region = regexp.MustCompile(`^[a-z]{2,4}(-[a-z]+)+-\d+$`)

func validRegion(s string) bool { return region.MatchString(s) }

// parseTags reads one or more tag:KEY=VALUE or tag:KEY terms, with spaces
// between them. A term with no key, or text that is not a term, makes the
// target Invalid.
func parseTags(t *Target) {
	for term := range strings.FieldsSeq(t.Raw) {
		kv, ok := strings.CutPrefix(term, "tag:")
		key, value, hasValue := strings.Cut(kv, "=")
		if !ok || key == "" {
			t.Kind, t.Tags = Invalid, nil
			return
		}
		t.Tags = append(t.Tags, Tag{Key: key, Value: value, HasValue: hasValue})
	}
	t.Kind = Tags
}
