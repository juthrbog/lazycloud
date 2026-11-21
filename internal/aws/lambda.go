package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// LambdaService defines the Lambda calls that views depend on. Every call
// reads. See docs/design/lambda.md.
type LambdaService interface {
	ListFunctionsPage(ctx context.Context, token *string) (*FunctionPage, error)
	// GetFunction reads one function, or one version or alias of it when
	// qualifier is not empty.
	GetFunction(ctx context.Context, name, qualifier string) (*FunctionDetail, error)
	// GetFunctionURL returns nil and no error when the function has no URL.
	GetFunctionURL(ctx context.Context, name string) (*FunctionURL, error)
	ListAliasesPage(ctx context.Context, name string, token *string) (*AliasPage, error)
	ListVersionsPage(ctx context.Context, name string, token *string) (*FunctionPage, error)
	ListTriggersPage(ctx context.Context, name string, token *string) (*TriggerPage, error)
}

// LambdaServiceImpl is the real AWS-backed implementation of LambdaService.
type LambdaServiceImpl struct {
	client *Client
}

// NewLambdaService creates a real Lambda service backed by client.
func NewLambdaService(client *Client) *LambdaServiceImpl {
	return &LambdaServiceImpl{client: client}
}

var _ LambdaService = (*LambdaServiceImpl)(nil)

// ─── Domain types ───────────────────────────────────────────────────────────

// Function is a Lambda function, or one version of it, as ListFunctions
// and ListVersionsByFunction return it. These calls also return the values
// of the environment variables, which often hold secrets. Function does
// not keep them; only GetFunction gives them (FunctionDetail).
type Function struct {
	Name          string
	ARN           string
	Version       string // "$LATEST", or a version number
	Description   string
	Runtime       string // empty for an image function
	Handler       string
	PackageType   string // Zip or Image
	Architectures []string
	MemoryMB      int32
	TimeoutSec    int32
	CodeSize      int64 // bytes
	LastModified  time.Time
	// From LoggingConfig. LogGroup is empty when AWS leaves it out; use
	// LogGroupName for the group that Lambda writes to.
	LogGroup       string
	LogFormat      string // Text or JSON
	AppLogLevel    string
	SystemLogLevel string
}

// LogGroupName is the log group of the function: LoggingConfig.LogGroup,
// or "/aws/lambda/<name>" when it is empty.
func (f Function) LogGroupName() string {
	if f.LogGroup != "" {
		return f.LogGroup
	}
	return "/aws/lambda/" + f.Name
}

// FunctionPage is one page of functions or versions.
type FunctionPage struct {
	Functions []Function
	Next      *string // nil: the last page
}

// FunctionDetail is a function as GetFunction returns it.
type FunctionDetail struct {
	Function
	State            string // Pending, Active, Inactive, Failed, ...
	StateReason      string
	StateReasonCode  string
	LastUpdateStatus string // Successful, Failed or InProgress
	LastUpdateReason string
	Role             string
	CodeSHA256       string
	ImageURI         string
	KMSKeyARN        string
	DeadLetterARN    string
	TracingMode      string // Active or PassThrough
	EphemeralMB      int32
	Env              map[string]string // values often hold secrets
	EnvError         string
	VPCID            string // empty: no VPC
	SubnetIDs        []string
	SecurityGroupIDs []string
	Layers           []Layer
	SnapStartApplyOn string
	SnapStartStatus  string
	// ReservedConcurrency is nil when the function has none.
	ReservedConcurrency *int32
	Tags                map[string]string
	TagsError           string // why Tags is empty, for example a missing lambda:ListTags
}

// Layer is a layer of a function. AWS gives only its ARN.
type Layer struct {
	ARN      string
	CodeSize int64
}

// Name returns the name of the layer, from its ARN
// (arn:aws:lambda:<region>:<account>:layer:<name>:<version>).
func (l Layer) Name() string {
	parts := strings.Split(l.ARN, ":")
	if len(parts) >= 8 && parts[5] == "layer" {
		return parts[6]
	}
	return l.ARN
}

// Version returns the version of the layer, from its ARN, or "".
func (l Layer) Version() string {
	parts := strings.Split(l.ARN, ":")
	if len(parts) >= 8 && parts[5] == "layer" {
		return parts[7]
	}
	return ""
}

// FunctionURL is the function URL of a function.
type FunctionURL struct {
	URL        string
	AuthType   string // NONE or AWS_IAM
	InvokeMode string // BUFFERED or RESPONSE_STREAM; empty means BUFFERED
	Created    time.Time
	Modified   time.Time
}

// Alias is an alias of a function.
type Alias struct {
	Name        string
	ARN         string
	Version     string
	Description string
	// Weights holds the versions that get a part of the calls, for example
	// {"2": 0.1}. The rest goes to Version.
	Weights map[string]float64
}

// AliasPage is one page of aliases.
type AliasPage struct {
	Aliases []Alias
	Next    *string
}

// Trigger is an event source mapping of a function.
type Trigger struct {
	UUID           string
	SourceARN      string
	FunctionARN    string
	State          string // Creating, Enabling, Enabled, Disabling, Disabled, Updating or Deleting
	StateReason    string
	LastResult     string
	BatchSize      int32
	BatchWindowSec int32
	StartPosition  string
	LastModified   time.Time
	Filters        []string // the filter patterns; empty when encrypted
}

// SourceType names the kind of source from its ARN: SQS, Kinesis,
// DynamoDB, Kafka, MQ or DocumentDB, or "Kafka" for a self-managed
// cluster, which has no ARN.
func (t Trigger) SourceType() string {
	parts := strings.Split(t.SourceARN, ":")
	if len(parts) < 3 {
		if t.SourceARN == "" {
			return "Kafka"
		}
		return "unknown"
	}
	switch parts[2] {
	case "sqs":
		return "SQS"
	case "kinesis":
		return "Kinesis"
	case "dynamodb":
		return "DynamoDB"
	case "kafka":
		return "Kafka"
	case "mq":
		return "MQ"
	case "rds", "docdb":
		return "DocumentDB"
	}
	return parts[2]
}

// TriggerPage is one page of triggers.
type TriggerPage struct {
	Triggers []Trigger
	Next     *string
}

// ─── Calls ──────────────────────────────────────────────────────────────────

// ListFunctionsPage returns one page of functions, up to 50.
func (svc *LambdaServiceImpl) ListFunctionsPage(ctx context.Context, token *string) (*FunctionPage, error) {
	out, err := svc.client.LambdaClient().ListFunctions(ctx, &lambda.ListFunctionsInput{
		Marker:   token,
		MaxItems: aws.Int32(50),
	})
	if err != nil {
		return nil, fmt.Errorf("ListFunctions: %w", err)
	}
	page := &FunctionPage{Next: nonEmpty(out.NextMarker)}
	for i := range out.Functions {
		page.Functions = append(page.Functions, functionFrom(&out.Functions[i]))
	}
	return page, nil
}

// ListVersionsPage returns one page of the versions of a function, up to
// 50, with $LATEST.
func (svc *LambdaServiceImpl) ListVersionsPage(ctx context.Context, name string, token *string) (*FunctionPage, error) {
	out, err := svc.client.LambdaClient().ListVersionsByFunction(ctx, &lambda.ListVersionsByFunctionInput{
		FunctionName: aws.String(name),
		Marker:       token,
		MaxItems:     aws.Int32(50),
	})
	if err != nil {
		return nil, fmt.Errorf("ListVersionsByFunction: %w", err)
	}
	page := &FunctionPage{Next: nonEmpty(out.NextMarker)}
	for i := range out.Versions {
		page.Functions = append(page.Functions, functionFrom(&out.Versions[i]))
	}
	return page, nil
}

// functionFrom keeps the fields of a list result. It leaves out the
// environment values.
func functionFrom(c *lambdatypes.FunctionConfiguration) Function {
	f := Function{
		Name:         aws.ToString(c.FunctionName),
		ARN:          aws.ToString(c.FunctionArn),
		Version:      aws.ToString(c.Version),
		Description:  aws.ToString(c.Description),
		Runtime:      string(c.Runtime),
		Handler:      aws.ToString(c.Handler),
		PackageType:  string(c.PackageType),
		MemoryMB:     aws.ToInt32(c.MemorySize),
		TimeoutSec:   aws.ToInt32(c.Timeout),
		CodeSize:     c.CodeSize,
		LastModified: parseLambdaTime(aws.ToString(c.LastModified)),
	}
	for _, a := range c.Architectures {
		f.Architectures = append(f.Architectures, string(a))
	}
	if l := c.LoggingConfig; l != nil {
		f.LogGroup = aws.ToString(l.LogGroup)
		f.LogFormat = string(l.LogFormat)
		f.AppLogLevel = string(l.ApplicationLogLevel)
		f.SystemLogLevel = string(l.SystemLogLevel)
	}
	return f
}

// GetFunction reads one function with GetFunction. It does not keep the
// presigned URL of the code, which is valid for 10 minutes.
func (svc *LambdaServiceImpl) GetFunction(ctx context.Context, name, qualifier string) (*FunctionDetail, error) {
	in := &lambda.GetFunctionInput{FunctionName: aws.String(name)}
	if qualifier != "" {
		in.Qualifier = aws.String(qualifier)
	}
	out, err := svc.client.LambdaClient().GetFunction(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("GetFunction: %w", err)
	}
	d := &FunctionDetail{Tags: maps.Clone(out.Tags)}
	if c := out.Configuration; c != nil {
		d.Function = functionFrom(c)
		d.State = string(c.State)
		d.StateReason = aws.ToString(c.StateReason)
		d.StateReasonCode = string(c.StateReasonCode)
		d.LastUpdateStatus = string(c.LastUpdateStatus)
		d.LastUpdateReason = aws.ToString(c.LastUpdateStatusReason)
		d.Role = aws.ToString(c.Role)
		d.CodeSHA256 = aws.ToString(c.CodeSha256)
		d.KMSKeyARN = aws.ToString(c.KMSKeyArn)
		if c.DeadLetterConfig != nil {
			d.DeadLetterARN = aws.ToString(c.DeadLetterConfig.TargetArn)
		}
		if c.TracingConfig != nil {
			d.TracingMode = string(c.TracingConfig.Mode)
		}
		if c.EphemeralStorage != nil {
			d.EphemeralMB = aws.ToInt32(c.EphemeralStorage.Size)
		}
		if e := c.Environment; e != nil {
			d.Env = maps.Clone(e.Variables)
			if e.Error != nil {
				d.EnvError = strings.TrimSpace(aws.ToString(e.Error.ErrorCode) + " " + aws.ToString(e.Error.Message))
			}
		}
		if v := c.VpcConfig; v != nil {
			d.VPCID = aws.ToString(v.VpcId)
			d.SubnetIDs = v.SubnetIds
			d.SecurityGroupIDs = v.SecurityGroupIds
		}
		for _, l := range c.Layers {
			d.Layers = append(d.Layers, Layer{ARN: aws.ToString(l.Arn), CodeSize: l.CodeSize})
		}
		if s := c.SnapStart; s != nil {
			d.SnapStartApplyOn = string(s.ApplyOn)
			d.SnapStartStatus = string(s.OptimizationStatus)
		}
	}
	if out.Code != nil {
		d.ImageURI = aws.ToString(out.Code.ImageUri)
	}
	if out.Concurrency != nil {
		d.ReservedConcurrency = out.Concurrency.ReservedConcurrentExecutions
	}
	if out.TagsError != nil {
		d.TagsError = strings.TrimSpace(aws.ToString(out.TagsError.ErrorCode) + ": " + aws.ToString(out.TagsError.Message))
	}
	return d, nil
}

// GetFunctionURL reads the function URL of a function, or returns nil
// when it has none.
func (svc *LambdaServiceImpl) GetFunctionURL(ctx context.Context, name string) (*FunctionURL, error) {
	out, err := svc.client.LambdaClient().GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		if _, ok := errors.AsType[*lambdatypes.ResourceNotFoundException](err); ok {
			return nil, nil //nolint:nilnil // no URL is not an error
		}
		return nil, fmt.Errorf("GetFunctionUrlConfig: %w", err)
	}
	return &FunctionURL{
		URL:        aws.ToString(out.FunctionUrl),
		AuthType:   string(out.AuthType),
		InvokeMode: string(out.InvokeMode),
		Created:    parseLambdaTime(aws.ToString(out.CreationTime)),
		Modified:   parseLambdaTime(aws.ToString(out.LastModifiedTime)),
	}, nil
}

// ListAliasesPage returns one page of the aliases of a function.
func (svc *LambdaServiceImpl) ListAliasesPage(ctx context.Context, name string, token *string) (*AliasPage, error) {
	out, err := svc.client.LambdaClient().ListAliases(ctx, &lambda.ListAliasesInput{
		FunctionName: aws.String(name),
		Marker:       token,
	})
	if err != nil {
		return nil, fmt.Errorf("ListAliases: %w", err)
	}
	page := &AliasPage{Next: nonEmpty(out.NextMarker)}
	for _, a := range out.Aliases {
		al := Alias{
			Name:        aws.ToString(a.Name),
			ARN:         aws.ToString(a.AliasArn),
			Version:     aws.ToString(a.FunctionVersion),
			Description: aws.ToString(a.Description),
		}
		if a.RoutingConfig != nil && len(a.RoutingConfig.AdditionalVersionWeights) > 0 {
			al.Weights = maps.Clone(a.RoutingConfig.AdditionalVersionWeights)
		}
		page.Aliases = append(page.Aliases, al)
	}
	return page, nil
}

// ListTriggersPage returns one page of the event source mappings of a
// function, up to 100.
func (svc *LambdaServiceImpl) ListTriggersPage(ctx context.Context, name string, token *string) (*TriggerPage, error) {
	out, err := svc.client.LambdaClient().ListEventSourceMappings(ctx, &lambda.ListEventSourceMappingsInput{
		FunctionName: aws.String(name),
		Marker:       token,
		MaxItems:     aws.Int32(100),
	})
	if err != nil {
		return nil, fmt.Errorf("ListEventSourceMappings: %w", err)
	}
	page := &TriggerPage{Next: nonEmpty(out.NextMarker)}
	for _, m := range out.EventSourceMappings {
		t := Trigger{
			UUID:           aws.ToString(m.UUID),
			SourceARN:      aws.ToString(m.EventSourceArn),
			FunctionARN:    aws.ToString(m.FunctionArn),
			State:          aws.ToString(m.State),
			StateReason:    aws.ToString(m.StateTransitionReason),
			LastResult:     aws.ToString(m.LastProcessingResult),
			BatchSize:      aws.ToInt32(m.BatchSize),
			BatchWindowSec: aws.ToInt32(m.MaximumBatchingWindowInSeconds),
			StartPosition:  string(m.StartingPosition),
			LastModified:   aws.ToTime(m.LastModified),
		}
		if m.FilterCriteria != nil {
			for _, f := range m.FilterCriteria.Filters {
				t.Filters = append(t.Filters, aws.ToString(f.Pattern))
			}
		}
		page.Triggers = append(page.Triggers, t)
	}
	return page, nil
}

// lambdaTimeLayout is the form of the times of Lambda:
// "2019-09-26T20:28:40.438+0000". The zone has no colon, so time.RFC3339
// does not read it.
const lambdaTimeLayout = "2006-01-02T15:04:05.000-0700"

// parseLambdaTime reads a time of Lambda, in its own form or in RFC 3339,
// or gives the zero time.
func parseLambdaTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{lambdaTimeLayout, "2006-01-02T15:04:05-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
