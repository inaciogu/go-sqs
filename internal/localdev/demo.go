package localdev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	gosqs "github.com/inaciogu/go-sqs/v2"
)

// Demo sends three messages, consumes them, drains confirmations, and cleans up.
func Demo(parent context.Context, useSNS, raw bool) (result error) {
	ctx, deadline := context.WithTimeout(parent, 30*time.Second)
	defer deadline()
	clients := NewClients()
	if err := clients.WaitReady(ctx); err != nil {
		return err
	}
	resources := &Resources{Clients: clients}
	defer func() { result = errors.Join(result, resources.Cleanup()) }()
	if err := resources.CreateQueue(ctx, UniqueName("demo-queue-")); err != nil {
		return err
	}
	if useSNS {
		if err := resources.Subscribe(ctx, raw); err != nil {
			return err
		}
	}
	for i := 1; i <= 3; i++ {
		body := fmt.Sprintf(`{"id":"order-%d"}`, i)
		var err error
		if useSNS {
			_, err = clients.SNS.Publish(ctx, &sns.PublishInput{TopicArn: aws.String(resources.TopicARN), Message: aws.String(body)})
		} else {
			_, err = clients.SQS.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(resources.QueueURL), MessageBody: aws.String(body)})
		}
		if err != nil {
			return err
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	seen := make(map[string]bool)
	var failures error
	format := gosqs.MessageFormatSQS
	if useSNS && !raw {
		format = gosqs.MessageFormatSNS
	}
	consumer, err := gosqs.NewConsumer(ctx, func(_ context.Context, message *gosqs.Message) error {
		var order struct {
			ID string `json:"id"`
		}
		if err := message.Unmarshal(&order); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Printf("Processed %s\n", order.ID)
		seen[order.ID] = true
		if len(seen) == 3 {
			cancel()
		}
		return nil
	}, gosqs.ConsumerOptions{
		QueueName: resources.QueueName, Client: clients.SQS, MessageFormat: format,
		WaitTime: gosqs.Duration(time.Second), ShutdownTimeout: gosqs.Duration(5 * time.Second),
		OnError: func(_ context.Context, err error) {
			mu.Lock()
			failures = errors.Join(failures, err)
			mu.Unlock()
			fmt.Fprintln(os.Stderr, err)
			cancel()
		},
	})
	if err != nil {
		return err
	}
	err = consumer.Run(runCtx)
	mu.Lock()
	defer mu.Unlock()
	if failures != nil {
		return errors.Join(failures, err)
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	if errors.Is(err, gosqs.ErrShutdownTimeout) || !errors.Is(err, context.Canceled) {
		return err
	}
	if len(seen) != 3 {
		return fmt.Errorf("processed %d of 3 messages", len(seen))
	}
	fmt.Println("All messages processed and acknowledged; consumer drained.")
	return nil
}
