// Package localdev shares explicit local AWS clients between examples and integration tests.
// It is not used by the library's consumer implementation.
package localdev

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type Clients struct {
	Endpoint string
	SQS      *sqs.Client
	SNS      *sns.Client
}

func NewClients() *Clients {
	endpoint := cmp.Or(os.Getenv("SQS_TEST_ENDPOINT"), "http://localhost:4566")
	cfg := aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:       &http.Client{Timeout: 25 * time.Second},
		RetryMaxAttempts: 1,
	}
	return &Clients{
		Endpoint: endpoint,
		SQS:      sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) }),
		SNS:      sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(endpoint) }),
	}
}

// WaitReady verifies both AWS APIs, not merely the emulator's health endpoint.
func (c *Clients) WaitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		probe, stop := context.WithTimeout(ctx, time.Second)
		_, last = c.SQS.ListQueues(probe, &sqs.ListQueuesInput{MaxResults: aws.Int32(1)})
		stop()
		if last == nil {
			probe, stop = context.WithTimeout(ctx, time.Second)
			_, last = c.SNS.ListTopics(probe, &sns.ListTopicsInput{})
			stop()
		}
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("SQS/SNS unavailable at %s; run docker compose up -d: %w (last probe: %v)", c.Endpoint, ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func UniqueName(prefix string) string { return prefix + rand.Text() }

// Resources owns only the resources created for one example or test.
type Resources struct {
	Clients         *Clients
	QueueName       string
	QueueURL        string
	TopicARN        string
	SubscriptionARN string
}

func (r *Resources) CreateQueue(ctx context.Context, name string) error {
	queue, err := r.Clients.SQS.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		return err
	}
	r.QueueName, r.QueueURL = name, aws.ToString(queue.QueueUrl)
	return nil
}

func (r *Resources) Subscribe(ctx context.Context, raw bool) error {
	topic, err := r.Clients.SNS.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(UniqueName("demo-topic-"))})
	if err != nil {
		return err
	}
	r.TopicARN = aws.ToString(topic.TopicArn)
	attrs, err := r.Clients.SQS.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(r.QueueURL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return err
	}
	sub, err := r.Clients.SNS.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(r.TopicARN), Protocol: aws.String("sqs"),
		Endpoint:   aws.String(attrs.Attributes[string(types.QueueAttributeNameQueueArn)]),
		Attributes: map[string]string{"RawMessageDelivery": fmt.Sprint(raw)}, ReturnSubscriptionArn: true,
	})
	if err != nil {
		return err
	}
	r.SubscriptionARN = aws.ToString(sub.SubscriptionArn)
	return nil
}

// Cleanup is independent of cancelled processing contexts and never resets shared state.
func (r *Resources) Cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var errs []error
	if r.SubscriptionARN != "" {
		_, err := r.Clients.SNS.Unsubscribe(ctx, &sns.UnsubscribeInput{SubscriptionArn: aws.String(r.SubscriptionARN)})
		errs = append(errs, err)
	}
	if r.TopicARN != "" {
		_, err := r.Clients.SNS.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: aws.String(r.TopicARN)})
		errs = append(errs, err)
	}
	if r.QueueURL != "" {
		_, err := r.Clients.SQS.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(r.QueueURL)})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
