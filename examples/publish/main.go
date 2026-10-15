// Publish sends messages to the manual examples, independently of consumers.
package main

import (
	"context"
	"encoding/json"
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
	"github.com/inaciogu/go-sqs/v2/examples/internal/playground"
	"github.com/inaciogu/go-sqs/v2/internal/localdev"
)

func main() {
	target := flag.String("target", "sqs", "sqs, sns, or otel")
	count := flag.Int("count", 3, "messages to send; zero publishes until Ctrl+C")
	interval := flag.Duration("interval", 0, "delay between messages; continuous publishing defaults to 1s")
	action := flag.String("action", "success", "OTel handler action: success, drop, retry, or cycle")
	body := flag.String("body", "", "custom body instead of generated JSON")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := publish(ctx, *target, *count, *interval, *action, *body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func publish(ctx context.Context, target string, count int, interval time.Duration, action, body string) error {
	if count < 0 || interval < 0 {
		return errors.New("count and interval must be nonnegative")
	}
	if target != "sqs" && target != "sns" && target != "otel" {
		return errors.New("target must be sqs, sns, or otel")
	}
	if action != "success" && action != "drop" && action != "retry" && action != "cycle" {
		return errors.New("action must be success, drop, retry, or cycle")
	}
	if count == 0 && interval == 0 {
		interval = time.Second
	}
	clients := localdev.NewClients()
	var url, arn string
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if target == "sns" {
		if err := clients.WaitReady(initCtx); err != nil {
			return err
		}
		paginator := sns.NewListTopicsPaginator(clients.SNS, &sns.ListTopicsInput{})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(initCtx)
			if err != nil {
				return err
			}
			for _, topic := range page.Topics {
				if strings.HasSuffix(aws.ToString(topic.TopicArn), ":"+playground.TopicName) {
					arn = aws.ToString(topic.TopicArn)
				}
			}
		}
		if arn == "" {
			return errors.New("topic missing; run go run ./examples/setup")
		}
	} else {
		if err := clients.WaitSQS(initCtx); err != nil {
			return err
		}
		queue := playground.BasicQueue
		if target == "otel" {
			queue = playground.TelemetryQueue
		}
		out, err := clients.SQS.GetQueueUrl(initCtx, &sqs.GetQueueUrlInput{QueueName: aws.String(queue)})
		if err != nil {
			return fmt.Errorf("run go run ./examples/setup first: %w", err)
		}
		url = aws.ToString(out.QueueUrl)
	}
	cancel()
	for i := 0; count == 0 || i < count; i++ {
		if ctx.Err() != nil {
			return nil
		}
		message := body
		if message == "" {
			selected := action
			if action == "cycle" {
				selected = []string{"success", "drop", "retry"}[i%3]
			}
			order := struct {
				ID     string `json:"id"`
				Action string `json:"action,omitempty"`
			}{ID: fmt.Sprintf("order-%d-%d", time.Now().UnixNano(), i+1)}
			if target == "otel" {
				order.Action = selected
			}
			data, err := json.Marshal(order)
			if err != nil {
				return err
			}
			message = string(data)
		}
		request, stop := context.WithTimeout(ctx, 5*time.Second)
		var err error
		if target == "sns" {
			_, err = clients.SNS.Publish(request, &sns.PublishInput{TopicArn: aws.String(arn), Message: aws.String(message)})
		} else {
			_, err = clients.SQS.SendMessage(request, &sqs.SendMessageInput{QueueUrl: aws.String(url), MessageBody: aws.String(message)})
		}
		stop()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		fmt.Printf("Published to %s: %s\n", target, message)
		if interval > 0 && (count == 0 || i+1 < count) {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
	return nil
}
