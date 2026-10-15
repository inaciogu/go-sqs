package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	gosqs "github.com/inaciogu/go-sqs/v2"
)

// The deterministic test scenario exercises the same handler and providers
// as the executable, without a Docker dependency or actual redelivery.
func run(parent context.Context) error {
	ctx, stop := context.WithTimeout(parent, 10*time.Second)
	defer stop()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	return runConsumer(runCtx, &demoClient{cancel: cancel}, "otel-demo", 0)
}

// Only SQS is simulated. Each delivery is settled before cancelling Run.
type demoClient struct {
	gosqs.QueueClient
	mu            sync.Mutex
	next, settled int
	cancel        context.CancelFunc
}

func (*demoClient) GetQueueUrl(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	return &sqs.GetQueueUrlOutput{QueueUrl: aws.String("http://localhost/000000000000/otel-demo")}, nil
}
func (c *demoClient) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	c.mu.Lock()
	if c.next < 3 {
		body := []string{"success", "drop", "retry"}[c.next%3]
		c.next++
		c.mu.Unlock()
		return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: aws.String(fmt.Sprintf(`{"id":"%s","action":"%s"}`, body, body)), MessageId: aws.String(body), ReceiptHandle: aws.String("example-receipt"), Attributes: map[string]string{"ApproximateReceiveCount": "1"}}}}, nil
	}
	c.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *demoClient) settle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settled++
	if c.settled == 3 {
		c.cancel()
	}
}
func (c *demoClient) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	c.settle()
	return &sqs.DeleteMessageOutput{}, nil
}
func (c *demoClient) ChangeMessageVisibility(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	c.settle()
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}
