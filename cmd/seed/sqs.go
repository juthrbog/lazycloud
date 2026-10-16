package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	awslib "github.com/juthrbog/lazycloud/internal/aws"
)

type sqsSeeder struct {
	client *awslib.Client
	config SQSSeedConfig
}

func newSQSSeeder(client *awslib.Client, cfg SQSSeedConfig) *sqsSeeder {
	return &sqsSeeder{client: client, config: cfg}
}

func (s *sqsSeeder) Name() string { return "sqs" }

func (s *sqsSeeder) Seed(ctx context.Context) error {
	sqsc := s.client.SQSClient()

	fmt.Printf("  SQS queues...")
	// Track created queue URLs for DLQ wiring and message seeding.
	queueURLs := make(map[string]string) // name → URL

	// Create base queues.
	for _, q := range s.config.Queues {
		url, err := s.ensureQueue(ctx, sqsc, q)
		if err != nil {
			return err
		}
		queueURLs[q.Name] = url
	}

	// Create extra generated queues.
	for i := range s.config.ExtraQueues {
		name := fmt.Sprintf("generated-queue-%03d", i+1)
		url, err := s.ensureQueue(ctx, sqsc, queueDef{Name: name})
		if err != nil {
			return err
		}
		queueURLs[name] = url
	}
	fmt.Println(" done")

	// Wire DLQ redrive policies.
	fmt.Printf("  SQS redrive policies...")
	for _, q := range s.config.Queues {
		if q.DLQRef == "" {
			continue
		}
		dlqURL, ok := queueURLs[q.DLQRef]
		if !ok {
			return fmt.Errorf("DLQ ref %q not found for queue %q", q.DLQRef, q.Name)
		}
		srcURL := queueURLs[q.Name]

		// Get DLQ ARN.
		dlqAttrs, err := sqsc.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(dlqURL),
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
		})
		if err != nil {
			return fmt.Errorf("getting DLQ ARN for %s: %w", q.DLQRef, err)
		}
		dlqArn := dlqAttrs.Attributes["QueueArn"]

		maxReceive := q.MaxReceiveCount
		if maxReceive == 0 {
			maxReceive = 5
		}

		redrivePolicy := fmt.Sprintf(`{"deadLetterTargetArn":"%s","maxReceiveCount":%d}`, dlqArn, maxReceive)
		_, err = sqsc.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
			QueueUrl: aws.String(srcURL),
			Attributes: map[string]string{
				"RedrivePolicy": redrivePolicy,
			},
		})
		if err != nil {
			return fmt.Errorf("setting redrive policy on %s: %w", q.Name, err)
		}
	}
	fmt.Println(" done")

	// Seed messages.
	fmt.Printf("  SQS messages...")
	skipped := 0
	for _, q := range s.config.Queues {
		if q.SeedMessages == 0 {
			continue
		}
		sent, err := seedMessages(ctx, sqsc, q, queueURLs[q.Name])
		if err != nil {
			return err
		}
		if !sent {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Printf(" done (%d queue(s) already had messages)\n", skipped)
	} else {
		fmt.Println(" done")
	}

	return nil
}

// sqsMessageAPI is the part of the SQS client that seedMessages uses.
type sqsMessageAPI interface {
	GetQueueAttributes(context.Context, *sqs.GetQueueAttributesInput, ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// seedMessages sends the seed messages of q to the queue at url, but only
// when the queue holds no messages: none visible, in flight, or delayed.
// A second run of the seed tool then does not double the counts, and a run
// after a purge sends the messages again. It reports whether it sent them.
func seedMessages(ctx context.Context, api sqsMessageAPI, q queueDef, url string) (bool, error) {
	counts := []sqstypes.QueueAttributeName{
		sqstypes.QueueAttributeNameApproximateNumberOfMessages,
		sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		sqstypes.QueueAttributeNameApproximateNumberOfMessagesDelayed,
	}
	out, err := api.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(url),
		AttributeNames: counts,
	})
	if err != nil {
		return false, fmt.Errorf("reading the message counts of %s: %w", q.Name, err)
	}
	for _, name := range counts {
		if n, _ := strconv.Atoi(out.Attributes[string(name)]); n > 0 {
			return false, nil
		}
	}

	for i := range q.SeedMessages {
		body := fmt.Sprintf(`{"order_id": "ORD-%04d", "timestamp": "2026-04-06T10:%02d:00Z", "amount": %d.%02d}`,
			i+1, i%60, (i+1)*10, (i*7)%100)
		input := &sqs.SendMessageInput{
			QueueUrl:    aws.String(url),
			MessageBody: aws.String(body),
		}
		if q.FIFO {
			input.MessageGroupId = aws.String("default")
			input.MessageDeduplicationId = aws.String("msg-" + strconv.Itoa(i))
		}
		if _, err := api.SendMessage(ctx, input); err != nil {
			return false, fmt.Errorf("sending message to %s: %w", q.Name, err)
		}
	}
	return true, nil
}

func (s *sqsSeeder) ensureQueue(ctx context.Context, sqsc *sqs.Client, q queueDef) (string, error) {
	// Keep the default visibility timeout (30s) so local queues behave like
	// real ones. LazyCloud resets visibility itself after a peek.
	attrs := map[string]string{}
	name := q.Name
	if q.FIFO {
		attrs["FifoQueue"] = "true"
		attrs["ContentBasedDeduplication"] = "true"
	}

	out, err := sqsc.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(name),
		Attributes: attrs,
	})
	if err != nil {
		return "", fmt.Errorf("creating queue %s: %w", name, err)
	}
	return aws.ToString(out.QueueUrl), nil
}
