// Package gosqs consumes SQS messages with bounded concurrency and graceful shutdown.
package gosqs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// QueueClient matches the AWS SDK v2 SQS client and permits dependency injection.
type QueueClient interface {
	GetQueueUrl(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	ChangeMessageVisibility(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
	DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ListQueues(context.Context, *sqs.ListQueuesInput, ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
}

// MessageHandler can be invoked concurrently. Nil acknowledges the message;
// ErrDrop discards it; any other error requests redelivery by changing visibility.
type MessageHandler func(context.Context, *Message) error

var (
	ErrDrop            = errors.New("drop message")
	ErrAlreadyRunning  = errors.New("consumer is already running")
	ErrShutdownTimeout = errors.New("consumer shutdown timed out")
	ErrNoQueues        = errors.New("no queues match the prefix")
)

// OperationError describes a processing or AWS operation failure.
// Identifying fields omit the body and receipt handle; Err is caller-provided.
type OperationError struct {
	Operation string
	QueueURL  string
	MessageID string
	Err       error
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("%s queue %s message %s: %v", e.Operation, e.QueueURL, e.MessageID, e.Err)
}
func (e *OperationError) Unwrap() error { return e.Err }

// Consumer is configured by NewConsumer. Do not copy a Consumer after use.
// Run calls are exclusive, including work that outlives a shutdown timeout.
type Consumer struct {
	client    QueueClient
	config    consumerConfig
	handler   MessageHandler
	logger    *slog.Logger
	telemetry *consumerTelemetry
	mu        sync.Mutex
	running   bool
}

func (c *Consumer) queueURLs(ctx context.Context) ([]string, error) {
	if c.config.queueName != "" {
		result, err := c.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(c.config.queueName)})
		if err != nil {
			return nil, &OperationError{Operation: "get_queue_url", Err: err}
		}
		if result == nil || aws.ToString(result.QueueUrl) == "" {
			return nil, errInvalidQueueURL
		}
		return []string{*result.QueueUrl}, nil
	}
	var urls []string
	paginator := sqs.NewListQueuesPaginator(c.client, &sqs.ListQueuesInput{QueueNamePrefix: aws.String(c.config.queuePrefix), MaxResults: aws.Int32(1000)})
	for paginator.HasMorePages() {
		result, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, &OperationError{Operation: "list_queues", Err: err}
		}
		urls = append(urls, result.QueueUrls...)
	}
	if len(urls) == 0 {
		return nil, ErrNoQueues
	}
	return urls, nil
}

// Run discovers queues, consumes messages, then drains active work on termination.
// ctx controls polling. Message contexts retain its values, but are cancelled only
// when the shutdown deadline expires. A noncooperative handler may outlive Run.
func (c *Consumer) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return ErrAlreadyRunning
	}
	c.running = true
	c.mu.Unlock()
	finish := func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }
	urls, err := c.queueURLs(ctx)
	if err != nil {
		c.logRunError(ctx, err)
		finish()
		return err
	}
	// Previous workers have exited before another Run can start.
	if c.telemetry != nil {
		c.telemetry.queues.Clear()
	}
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	capacity := make(chan struct{}, c.config.maxConcurrency)
	for i := 0; i < c.config.maxConcurrency; i++ {
		capacity <- struct{}{}
	}
	var polls, workers sync.WaitGroup
	errs := make(chan error, len(urls))
	polls.Add(len(urls))
	for _, url := range urls {
		go func(url string) { defer polls.Done(); errs <- c.poll(pollCtx, workCtx, url, capacity, &workers) }(url)
	}
	var cause error
	select {
	case <-ctx.Done():
		cause = ctx.Err()
	case cause = <-errs:
	}
	shutdownStart := c.telemetry.start()
	stopPolling()
	c.logRunError(ctx, cause)
	done := make(chan struct{})
	go func() {
		polls.Wait() // No worker can be added after polling stops.
		workers.Wait()
		cancelWork()
		finish()
		close(done)
	}()
	// Prefer completed drainage even when the configured deadline is zero.
	select {
	case <-done:
		c.telemetry.shutdown(ctx, shutdownStart, "complete")
		return cause
	default:
	}
	timer := time.NewTimer(c.config.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
		c.telemetry.shutdown(ctx, shutdownStart, "complete")
		return cause
	case <-timer.C:
		cancelWork()
		c.telemetry.shutdown(ctx, shutdownStart, "timeout")
		c.telemetry.failure(ctx, nil, "shutdown", ErrShutdownTimeout, true)
		c.logger.ErrorContext(ctx, "consumer shutdown timed out", "operation", "shutdown")
		return errors.Join(cause, ErrShutdownTimeout)
	}
}

func (c *Consumer) poll(ctx, workCtx context.Context, url string, capacity chan struct{}, workers *sync.WaitGroup) error {
	queue := c.telemetry.queue(url)
	release := func(n int) {
		c.telemetry.capacityChange(ctx, -int64(n))
		for i := 0; i < n; i++ {
			capacity <- struct{}{}
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		reserved := 0
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-capacity:
			reserved = 1
			c.telemetry.capacityChange(ctx, 1)
		}
	reserve:
		for reserved < c.config.maxNumberOfMessages {
			select {
			case <-capacity:
				reserved++
				c.telemetry.capacityChange(ctx, 1)
			default:
				break reserve
			}
		}
		if err := ctx.Err(); err != nil {
			release(reserved)
			return err
		}
		receivedAt := time.Now()
		c.logger.DebugContext(ctx, "polling messages", "operation", "receive", "queue_url", url)
		operationStart := c.telemetry.start()
		result, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(url), MaxNumberOfMessages: int32(reserved),
			VisibilityTimeout: int32(c.config.visibilityTimeout / time.Second), WaitTimeSeconds: int32(c.config.waitTime / time.Second),
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll}, MessageAttributeNames: []string{"All"},
		})
		if err == nil && (result == nil || len(result.Messages) > reserved) {
			err = errInvalidReceive
		}
		c.telemetry.operation(ctx, queue, "ReceiveMessage", "receive", operationStart, err)
		if err != nil {
			c.telemetry.failure(ctx, queue, "receive", err, ctx.Err() != nil)
			release(reserved)
			return &OperationError{Operation: "receive", QueueURL: url, Err: err}
		}
		c.telemetry.received(ctx, queue, int64(len(result.Messages)))
		release(reserved - len(result.Messages))
		for i := range result.Messages {
			if ctx.Err() != nil {
				release(len(result.Messages) - i)
				return ctx.Err()
			}
			message := result.Messages[i]
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer release(1)
				c.handleMessage(workCtx, url, &message, receivedAt)
			}()
		}
	}
}

func (c *Consumer) logRunError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	operation, url := "discover", ""
	var event *OperationError
	if errors.As(err, &event) {
		operation, url = event.Operation, event.QueueURL
	}
	// Receive failures are counted by each poller, including simultaneous failures.
	if operation != "receive" {
		c.telemetry.failure(ctx, c.telemetry.queue(url), operation, err, ctx.Err() != nil)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	c.logger.ErrorContext(ctx, "consumer operation failed", "operation", operation, "queue_url", url)
}

func (c *Consumer) report(ctx context.Context, op, url, id string, err error) {
	c.telemetry.failure(ctx, c.telemetry.queue(url), op, err, false)
	event := &OperationError{Operation: op, QueueURL: url, MessageID: id, Err: err}
	level := slog.LevelError
	if op == "handler" {
		level = slog.LevelWarn
	}
	// Error text from third-party handlers may include sensitive content. Keep logs
	// structural; the original error is available to OnError through OperationError.
	c.logger.Log(ctx, level, "message operation failed", "operation", op, "queue_url", url, "message_id", id)
	if c.config.onError != nil {
		c.config.onError(ctx, event)
	}
}

func (c *Consumer) handleMessage(ctx context.Context, url string, raw *types.Message, receivedAt time.Time) {
	queue := c.telemetry.queue(url)
	c.telemetry.workerChange(ctx, 1)
	defer c.telemetry.workerChange(ctx, -1)
	msg := NewMessage(raw)
	msg.Metadata.QueueURL = url
	// Keep receipt and retry state independent of metadata edited by a handler.
	delivery := &Message{Metadata: MessageMetadata{
		MessageID: msg.Metadata.MessageID, ReceiptHandle: msg.Metadata.ReceiptHandle, QueueURL: url,
		SystemAttributes: map[string]string{"ApproximateReceiveCount": msg.Metadata.SystemAttributes["ApproximateReceiveCount"]},
	}}
	if c.config.messageFormat == MessageFormatSNS {
		if err := unwrapSNS(msg); err != nil {
			c.report(ctx, "decode", url, delivery.Metadata.MessageID, err)
			c.retryMessage(ctx, delivery, receivedAt)
			return
		}
	}
	handlerStart := c.telemetry.start()
	err := c.handler(ctx, msg)
	c.telemetry.process(ctx, queue, handlerStart, err)
	if err == nil || errors.Is(err, ErrDrop) {
		operationStart := c.telemetry.start()
		_, err = c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: aws.String(url), ReceiptHandle: aws.String(delivery.Metadata.ReceiptHandle)})
		c.telemetry.operation(ctx, queue, "DeleteMessage", "settle", operationStart, err)
		if err != nil {
			c.report(ctx, "delete", url, delivery.Metadata.MessageID, err)
		} else {
			c.logger.DebugContext(ctx, "message acknowledged", "operation", "delete", "queue_url", url, "message_id", delivery.Metadata.MessageID)
		}
		return
	}
	c.report(ctx, "handler", url, delivery.Metadata.MessageID, err)
	c.retryMessage(ctx, delivery, receivedAt)
}

func (c *Consumer) retryMessage(ctx context.Context, msg *Message, receivedAt time.Time) {
	attempts, err := strconv.Atoi(msg.Metadata.SystemAttributes["ApproximateReceiveCount"])
	if err != nil || attempts < 1 {
		attempts = 1
	}
	// Start the 12-hour budget before ReceiveMessage and allow a safety second.
	remaining := int64((12*time.Hour - time.Since(receivedAt) - time.Second) / time.Second)
	if remaining <= 0 {
		c.report(ctx, "change_visibility", msg.Metadata.QueueURL, msg.Metadata.MessageID, errVisibilityBudget)
		return
	}
	delay := math.Min(math.Pow(c.config.backoffMultiplier, float64(attempts)), float64(remaining))
	operationStart := c.telemetry.start()
	_, err = c.client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(msg.Metadata.QueueURL), ReceiptHandle: aws.String(msg.Metadata.ReceiptHandle), VisibilityTimeout: int32(delay),
	})
	c.telemetry.operation(ctx, c.telemetry.queue(msg.Metadata.QueueURL), "ChangeMessageVisibility", "settle", operationStart, err)
	if err != nil {
		c.report(ctx, "change_visibility", msg.Metadata.QueueURL, msg.Metadata.MessageID, err)
	}
}

type runner interface{ Run(context.Context) error }

// RunAll runs independent consumers, cancels siblings on an error, and waits for
// their drainage. Shutdown failures are joined with the triggering error.
func RunAll(ctx context.Context, consumers ...runner) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if len(consumers) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(consumers))
	for _, consumer := range consumers {
		go func() { results <- consumer.Run(ctx) }()
	}
	var failures []error
	for range consumers {
		err := <-results
		if err != nil {
			// Retain the initiating error even when it is a child-specific cancellation
			// or deadline, rather than replacing it with sibling cancellation.
			initiating := ctx.Err() == nil
			if initiating || errors.Is(err, ErrShutdownTimeout) || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
				failures = append(failures, err)
			}
			cancel()
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return ctx.Err()
}
