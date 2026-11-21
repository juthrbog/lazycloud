package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

// The Lambda seeder makes functions, versions, aliases and SQS triggers.
// It never invokes a function: Invoke pulls a runtime image and starts
// containers. The logs seeder writes the log lines of the functions,
// because the seeders run in parallel (docs/design/lambda.md).

// lambdaAPI is the part of the Lambda client that the seeder uses, so a
// test can give a fake.
type lambdaAPI interface {
	CreateFunction(ctx context.Context, in *lambda.CreateFunctionInput, opts ...func(*lambda.Options)) (*lambda.CreateFunctionOutput, error)
	GetFunction(ctx context.Context, in *lambda.GetFunctionInput, opts ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
	UpdateFunctionConfiguration(ctx context.Context, in *lambda.UpdateFunctionConfigurationInput, opts ...func(*lambda.Options)) (*lambda.UpdateFunctionConfigurationOutput, error)
	ListVersionsByFunction(ctx context.Context, in *lambda.ListVersionsByFunctionInput, opts ...func(*lambda.Options)) (*lambda.ListVersionsByFunctionOutput, error)
	PublishVersion(ctx context.Context, in *lambda.PublishVersionInput, opts ...func(*lambda.Options)) (*lambda.PublishVersionOutput, error)
	GetAlias(ctx context.Context, in *lambda.GetAliasInput, opts ...func(*lambda.Options)) (*lambda.GetAliasOutput, error)
	CreateAlias(ctx context.Context, in *lambda.CreateAliasInput, opts ...func(*lambda.Options)) (*lambda.CreateAliasOutput, error)
	ListEventSourceMappings(ctx context.Context, in *lambda.ListEventSourceMappingsInput, opts ...func(*lambda.Options)) (*lambda.ListEventSourceMappingsOutput, error)
	CreateEventSourceMapping(ctx context.Context, in *lambda.CreateEventSourceMappingInput, opts ...func(*lambda.Options)) (*lambda.CreateEventSourceMappingOutput, error)
}

// queueAPI is the part of the SQS client that the seeder uses for the
// queue of a trigger.
type queueAPI interface {
	CreateQueue(ctx context.Context, in *sqs.CreateQueueInput, opts ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error)
	GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, opts ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

// seedRole is the role of the seeded functions. LocalStack checks only its
// form, not that it exists.
const seedRole = "arn:aws:iam::000000000000:role/lazycloud-seed"

// lambdaWait is how long the seeder waits for a function to be ready, and
// how often it asks. Tests make them short.
var (
	lambdaWait     = time.Minute
	lambdaWaitStep = 200 * time.Millisecond
)

type lambdaSeeder struct {
	client *awslib.Client
	config LambdaSeedConfig
}

func newLambdaSeeder(client *awslib.Client, cfg LambdaSeedConfig) *lambdaSeeder {
	return &lambdaSeeder{client: client, config: cfg}
}

func (s *lambdaSeeder) Name() string { return "lambda" }

func (s *lambdaSeeder) Seed(ctx context.Context) error {
	fmt.Printf("  Lambda functions...")
	if err := seedLambda(ctx, s.client.LambdaClient(), s.client.SQSClient(), s.config); err != nil {
		return err
	}
	fmt.Println(" done")
	return nil
}

func seedLambda(ctx context.Context, api lambdaAPI, queues queueAPI, cfg LambdaSeedConfig) error {
	code, err := handlerZip()
	if err != nil {
		return err
	}
	for _, f := range cfg.Functions {
		if err := seedFunction(ctx, api, queues, f, code); err != nil {
			return err
		}
	}
	for i := range cfg.ExtraFunctions {
		f := functionDef{Name: fmt.Sprintf("generated-fn-%04d", i+1), Runtime: "python3.12"}
		if _, err := ensureFunction(ctx, api, f, code); err != nil {
			return err
		}
	}
	return nil
}

// seedFunction makes one function and its versions, alias and trigger. A
// second run adds nothing.
func seedFunction(ctx context.Context, api lambdaAPI, queues queueAPI, f functionDef, code []byte) error {
	created, err := ensureFunction(ctx, api, f, code)
	if err != nil {
		return err
	}
	if created || f.Versions > 0 || f.Alias != nil {
		if err := waitReady(ctx, api, f.Name); err != nil {
			return err
		}
	}
	if err := ensureVersions(ctx, api, f); err != nil {
		return err
	}
	if f.Alias != nil {
		if err := ensureAlias(ctx, api, f.Name, *f.Alias); err != nil {
			return err
		}
	}
	if f.SQSTrigger != "" {
		if err := ensureSQSTrigger(ctx, api, queues, f.Name, f.SQSTrigger); err != nil {
			return err
		}
	}
	return nil
}

// ensureFunction makes the function when it does not exist. It reports
// whether it made it.
func ensureFunction(ctx context.Context, api lambdaAPI, f functionDef, code []byte) (bool, error) {
	_, err := api.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(f.Name)})
	if err == nil {
		return false, nil
	}
	if !lambdaNotFound(err) {
		return false, fmt.Errorf("reading function %s: %w", f.Name, err)
	}
	in := &lambda.CreateFunctionInput{
		FunctionName:  aws.String(f.Name),
		Role:          aws.String(seedRole),
		Description:   aws.String(f.Description),
		Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
		Tags:          f.Tags,
	}
	if f.ImageURI != "" {
		in.PackageType = lambdatypes.PackageTypeImage
		in.Code = &lambdatypes.FunctionCode{ImageUri: aws.String(f.ImageURI)}
	} else {
		in.PackageType = lambdatypes.PackageTypeZip
		in.Runtime = lambdatypes.Runtime(f.Runtime)
		in.Handler = aws.String("index.handler")
		in.Code = &lambdatypes.FunctionCode{ZipFile: code}
	}
	if f.MemoryMB > 0 {
		in.MemorySize = aws.Int32(f.MemoryMB)
	}
	if f.TimeoutSec > 0 {
		in.Timeout = aws.Int32(f.TimeoutSec)
	}
	if len(f.Env) > 0 {
		in.Environment = &lambdatypes.Environment{Variables: f.Env}
	}
	if f.LogFormat != "" || f.LogGroup != "" || f.SystemLogLevel != "" {
		lc := &lambdatypes.LoggingConfig{LogFormat: lambdatypes.LogFormat(f.LogFormat)}
		if f.LogGroup != "" {
			lc.LogGroup = aws.String(f.LogGroup)
		}
		if f.SystemLogLevel != "" {
			lc.SystemLogLevel = lambdatypes.SystemLogLevel(f.SystemLogLevel)
		}
		in.LoggingConfig = lc
	}
	if _, err := api.CreateFunction(ctx, in); err != nil {
		return false, fmt.Errorf("creating function %s: %w", f.Name, err)
	}
	return true, nil
}

// waitReady waits until the function is Active and no update runs. A new
// function is Pending for a short time, and AWS refuses a publish then.
func waitReady(ctx context.Context, api lambdaAPI, name string) error {
	deadline := time.Now().Add(lambdaWait)
	for {
		out, err := api.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(name)})
		if err != nil {
			return fmt.Errorf("reading function %s: %w", name, err)
		}
		c := out.Configuration
		if c != nil && c.State == lambdatypes.StateFailed {
			return fmt.Errorf("function %s failed: %s", name, aws.ToString(c.StateReason))
		}
		if c != nil && c.State == lambdatypes.StateActive && c.LastUpdateStatus != lambdatypes.LastUpdateStatusInProgress {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("function %s is not ready after %s", name, lambdaWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lambdaWaitStep):
		}
	}
}

// ensureVersions publishes versions until the function has f.Versions.
// PublishVersion returns the last version when nothing changed, so each
// new version first changes the description.
func ensureVersions(ctx context.Context, api lambdaAPI, f functionDef) error {
	if f.Versions == 0 {
		return nil
	}
	out, err := api.ListVersionsByFunction(ctx, &lambda.ListVersionsByFunctionInput{
		FunctionName: aws.String(f.Name), MaxItems: aws.Int32(50),
	})
	if err != nil {
		return fmt.Errorf("listing versions of %s: %w", f.Name, err)
	}
	have := 0
	for _, v := range out.Versions {
		if n, err := strconv.Atoi(aws.ToString(v.Version)); err == nil && n > have {
			have = n
		}
	}
	for n := have + 1; n <= f.Versions; n++ {
		desc := fmt.Sprintf("%s (version %d)", f.Description, n)
		if _, err := api.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(f.Name), Description: aws.String(desc),
		}); err != nil {
			return fmt.Errorf("updating %s: %w", f.Name, err)
		}
		if err := waitReady(ctx, api, f.Name); err != nil {
			return err
		}
		if _, err := api.PublishVersion(ctx, &lambda.PublishVersionInput{
			FunctionName: aws.String(f.Name), Description: aws.String(desc),
		}); err != nil {
			return fmt.Errorf("publishing version %d of %s: %w", n, f.Name, err)
		}
	}
	return nil
}

func ensureAlias(ctx context.Context, api lambdaAPI, fn string, a aliasDef) error {
	_, err := api.GetAlias(ctx, &lambda.GetAliasInput{FunctionName: aws.String(fn), Name: aws.String(a.Name)})
	if err == nil {
		return nil
	}
	if !lambdaNotFound(err) {
		return fmt.Errorf("reading alias %s of %s: %w", a.Name, fn, err)
	}
	in := &lambda.CreateAliasInput{
		FunctionName:    aws.String(fn),
		Name:            aws.String(a.Name),
		FunctionVersion: aws.String(a.Version),
		Description:     aws.String(a.Description),
	}
	if a.WeightVersion != "" {
		in.RoutingConfig = &lambdatypes.AliasRoutingConfiguration{
			AdditionalVersionWeights: map[string]float64{a.WeightVersion: a.Weight},
		}
	}
	if _, err := api.CreateAlias(ctx, in); err != nil {
		return fmt.Errorf("creating alias %s of %s: %w", a.Name, fn, err)
	}
	return nil
}

// ensureSQSTrigger makes the queue and a mapping from it to the function.
// The seeder never disables a mapping: on LocalStack, a disabled mapping
// that is deleted stays in the list for good.
func ensureSQSTrigger(ctx context.Context, api lambdaAPI, queues queueAPI, fn, queue string) error {
	q, err := queues.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(queue)})
	if err != nil {
		return fmt.Errorf("creating queue %s: %w", queue, err)
	}
	attrs, err := queues.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       q.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return fmt.Errorf("reading queue %s: %w", queue, err)
	}
	arn := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	out, err := api.ListEventSourceMappings(ctx, &lambda.ListEventSourceMappingsInput{
		FunctionName: aws.String(fn), EventSourceArn: aws.String(arn),
	})
	if err != nil {
		return fmt.Errorf("listing triggers of %s: %w", fn, err)
	}
	for _, m := range out.EventSourceMappings {
		if aws.ToString(m.State) != "Deleting" {
			return nil
		}
	}
	if _, err := api.CreateEventSourceMapping(ctx, &lambda.CreateEventSourceMappingInput{
		FunctionName: aws.String(fn), EventSourceArn: aws.String(arn), BatchSize: aws.Int32(10),
	}); err != nil {
		return fmt.Errorf("creating the trigger of %s: %w", fn, err)
	}
	return nil
}

// handlerZip returns the code of the seeded functions: a Python handler
// that prints a line.
func handlerZip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.py")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte("def handler(event, context):\n    print(\"processed order\")\n    return {\"ok\": True}\n")); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func lambdaNotFound(err error) bool {
	var e *lambdatypes.ResourceNotFoundException
	return errors.As(err, &e)
}
