// Setup provisions stable resources for manual examples. It is safe to repeat.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
	"github.com/inaciogu/go-sqs/v2/examples/internal/playground"
	"github.com/inaciogu/go-sqs/v2/internal/localdev"
)

func main() {
	cleanup := flag.Bool("cleanup", false, "delete only the named example resources")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := setup(ctx, *cleanup); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func setup(ctx context.Context, cleanup bool) error {
	clients := localdev.NewClients()
	if err := clients.WaitReady(ctx); err != nil {
		return err
	}
	names := []string{playground.BasicQueue, playground.SNSQueue, playground.SNSRawQueue, playground.TelemetryQueue}
	if cleanup {
		return remove(ctx, clients, names)
	}
	queues := make(map[string]string)
	for _, name := range names {
		out, err := clients.SQS.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name)})
		if err != nil {
			return err
		}
		queues[name] = aws.ToString(out.QueueUrl)
		fmt.Printf("Queue %s: %s\n", name, queues[name])
	}
	topic, err := clients.SNS.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(playground.TopicName)})
	if err != nil {
		return err
	}
	for _, name := range []string{playground.SNSQueue, playground.SNSRawQueue} {
		attrs, err := clients.SQS.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: aws.String(queues[name]), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn}})
		if err != nil {
			return err
		}
		sub, err := clients.SNS.Subscribe(ctx, &sns.SubscribeInput{
			TopicArn: topic.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(attrs.Attributes["QueueArn"]), ReturnSubscriptionArn: true,
		})
		if err != nil {
			return err
		}
		// Updating the attribute also makes repeated setup repair delivery mode.
		_, err = clients.SNS.SetSubscriptionAttributes(ctx, &sns.SetSubscriptionAttributesInput{
			SubscriptionArn: sub.SubscriptionArn, AttributeName: aws.String("RawMessageDelivery"), AttributeValue: aws.String(fmt.Sprint(name == playground.SNSRawQueue)),
		})
		if err != nil {
			return err
		}
	}
	fmt.Printf("Topic %s: %s\nReady. Start a consumer, then run examples/publish.\n", playground.TopicName, aws.ToString(topic.TopicArn))
	return nil
}

func remove(ctx context.Context, clients *localdev.Clients, names []string) error {
	var failures []error
	topics := sns.NewListTopicsPaginator(clients.SNS, &sns.ListTopicsInput{})
	for topics.HasMorePages() {
		page, err := topics.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, topic := range page.Topics {
			if strings.HasSuffix(aws.ToString(topic.TopicArn), ":"+playground.TopicName) {
				_, err := clients.SNS.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: topic.TopicArn})
				failures = append(failures, err)
			}
		}
	}
	for _, name := range names {
		queue, err := clients.SQS.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
		if err != nil {
			var api smithy.APIError
			if errors.As(err, &api) && (api.ErrorCode() == "QueueDoesNotExist" || api.ErrorCode() == "AWS.SimpleQueueService.NonExistentQueue") {
				continue
			}
			failures = append(failures, err)
			continue
		}
		_, err = clients.SQS.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: queue.QueueUrl})
		failures = append(failures, err)
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	fmt.Println("Example resources removed. Integration-test resources were not touched.")
	return nil
}
