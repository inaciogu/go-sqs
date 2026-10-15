package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	gosqs "github.com/inaciogu/go-sqs/v2"
)

func main() {
	raw := flag.Bool("raw", false, "consume the raw-delivery queue using SQS format")
	queue := flag.String("queue", "", "queue override; default depends on -raw")
	flag.Parse()
	if *queue == "" {
		*queue = "example-sns"
		if *raw {
			*queue = "example-sns-raw"
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *queue, *raw); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, queue string, raw bool) error {
	format := gosqs.MessageFormatSNS
	if raw {
		format = gosqs.MessageFormatSQS
	}
	// Explicit local credentials bypass AWS profiles. For AWS, use the SDK's
	// default configuration chain instead of these development credentials.
	endpoint := cmp.Or(os.Getenv("SQS_TEST_ENDPOINT"), "http://localhost:4566")
	client := sqs.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
	initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	consumer, err := gosqs.NewConsumer(initCtx, handleOrder, gosqs.ConsumerOptions{
		QueueName: queue, Client: client, MessageFormat: format,
		WaitTime:          gosqs.Duration(time.Second),
		VisibilityTimeout: gosqs.Duration(5 * time.Second),
		ShutdownTimeout:   gosqs.Duration(5 * time.Second),
		OnError: func(ctx context.Context, err error) {
			slog.WarnContext(ctx, "processing or acknowledgement failed", "error", err)
		},
	})
	cancel()
	if err != nil {
		return err
	}
	fmt.Printf("Consuming %s at %s; publish messages in another terminal. Ctrl+C drains and stops.\n", queue, endpoint)
	err = consumer.Run(ctx)
	if errors.Is(err, gosqs.ErrShutdownTimeout) || !errors.Is(err, context.Canceled) {
		return err
	}
	fmt.Println("Consumer drained; resources remain available for the next run.")
	return nil
}

func handleOrder(ctx context.Context, message *gosqs.Message) error {
	var order struct {
		ID string `json:"id"`
	}
	if err := message.Unmarshal(&order); err != nil {
		return err
	}
	if order.ID == "" {
		return errors.New("order id is required")
	}
	slog.InfoContext(ctx, "order processed", "id", order.ID, "message_id", message.Metadata.MessageID)
	return nil
}
