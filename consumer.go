package gosqs

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/inaciogu/go-sqs/logger"
)

type QueueClient interface {
	GetQueueUrl(input *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error)
	ReceiveMessage(input *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
	ChangeMessageVisibility(input *sqs.ChangeMessageVisibilityInput) (*sqs.ChangeMessageVisibilityOutput, error)
	DeleteMessage(input *sqs.DeleteMessageInput) (*sqs.DeleteMessageOutput, error)
	ListQueues(input *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error)
}

type Logger interface {
	Log(message string, v ...interface{})
}

type MessageHandler func(ctx context.Context, message *Message) error

type ConsumerOptions struct {
	QueueName string
	Region    string
	Endpoint  string

	PrefixBased bool

	MaxNumberOfMessages int64
	VisibilityTimeout   int64
	WaitTimeSeconds     int64
	LogLevel            string
	BackoffMultiplier   float64
}

type Consumer struct {
	Client        QueueClient
	ClientOptions ConsumerOptions
	Handler       MessageHandler
	Logger        Logger
}

const (
	DefaultMaxNumberOfMessages = 10
	DefaultVisibilityTimeout   = 30
	DefaultWaitTimeSeconds     = 20
	DefaultRegion              = "us-east-1"
)

var ErrDrop = errors.New("drop message")

type runner interface {
	Run(context.Context) error
}

func NewConsumer(handler MessageHandler, options ConsumerOptions) (*Consumer, error) {
	if options.QueueName == "" {
		return nil, errors.New("QueueName is required")
	}

	if handler == nil {
		return nil, errors.New("Handler is required")
	}

	setConsumerDefaultOptions(&options)

	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
		Config: aws.Config{
			Region:   aws.String(options.Region),
			Endpoint: aws.String(options.Endpoint),
		},
	}))
	sqsService := sqs.New(sess)

	log := logger.New(logger.DefaultLoggerConfig{LogLevel: options.LogLevel})

	return &Consumer{
		Client:        sqsService,
		ClientOptions: options,
		Handler:       handler,
		Logger:        log,
	}, nil
}

func setConsumerDefaultOptions(options *ConsumerOptions) {
	if options.MaxNumberOfMessages == 0 {
		options.MaxNumberOfMessages = DefaultMaxNumberOfMessages
	}

	if options.VisibilityTimeout == 0 {
		options.VisibilityTimeout = DefaultVisibilityTimeout
	}

	if options.WaitTimeSeconds == 0 {
		options.WaitTimeSeconds = DefaultWaitTimeSeconds
	}

	if options.Region == "" {
		options.Region = DefaultRegion
	}

	if options.LogLevel == "" {
		options.LogLevel = "info"
	}

	if options.BackoffMultiplier == 0 {
		options.BackoffMultiplier = 2
	}
}

func (c *Consumer) QueueURL() (string, error) {
	urlResult, err := c.Client.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(c.ClientOptions.QueueName),
	})
	if err != nil {
		return "", err
	}

	return aws.StringValue(urlResult.QueueUrl), nil
}

func (c *Consumer) QueueURLs(prefix string) ([]string, error) {
	result, err := c.Client.ListQueues(&sqs.ListQueuesInput{
		QueueNamePrefix: aws.String(prefix),
	})
	if err != nil {
		return nil, err
	}

	queueURLs := make([]string, 0, len(result.QueueUrls))
	for _, queueURL := range result.QueueUrls {
		queueURLs = append(queueURLs, aws.StringValue(queueURL))
	}

	return queueURLs, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if c.ClientOptions.PrefixBased {
		queueURLs, err := c.QueueURLs(c.ClientOptions.QueueName)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		var wg sync.WaitGroup
		errCh := make(chan error, len(queueURLs))

		for _, queueURL := range queueURLs {
			wg.Add(1)
			go func(queueURL string) {
				defer wg.Done()
				errCh <- c.consumeQueue(ctx, queueURL)
			}(queueURL)
		}

		var firstErr error
		for i := 0; i < len(queueURLs); i++ {
			if err := <-errCh; err != nil && firstErr == nil {
				firstErr = err
				cancel()
			}
		}

		wg.Wait()

		if firstErr != nil {
			return firstErr
		}

		return ctx.Err()
	}

	queueURL, err := c.QueueURL()
	if err != nil {
		return err
	}

	return c.consumeQueue(ctx, queueURL)
}

// RunAll starts multiple consumers in parallel and waits until the context is cancelled
// or one of the consumers returns an error.
func RunAll(ctx context.Context, consumers ...runner) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if len(consumers) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(consumers))

	var wg sync.WaitGroup
	for _, consumer := range consumers {
		wg.Add(1)
		go func(consumer runner) {
			defer wg.Done()
			errCh <- consumer.Run(ctx)
		}(consumer)
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	var firstErr error
	for err := range errCh {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	if firstErr != nil {
		return firstErr
	}

	return ctx.Err()
}

func (c *Consumer) consumeQueue(ctx context.Context, queueURL string) error {
	ch := make(chan *sqs.Message)
	errCh := make(chan error, 1)

	go func() {
		errCh <- c.receiveMessages(ctx, queueURL, ch)
	}()

	for {
		select {
		case <-ctx.Done():
			<-errCh
			return ctx.Err()
		case sqsMessage, ok := <-ch:
			if !ok {
				return <-errCh
			}
			go c.handleMessage(ctx, queueURL, sqsMessage)
		}
	}
}

func (c *Consumer) receiveMessages(ctx context.Context, queueURL string, ch chan *sqs.Message) error {
	defer close(ch)

	queueName := queueNameFromURL(queueURL)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		c.Logger.Log("polling messages from queue %s", queueName)

		result, err := c.Client.ReceiveMessage(&sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: aws.Int64(c.ClientOptions.MaxNumberOfMessages),
			WaitTimeSeconds:     aws.Int64(c.ClientOptions.WaitTimeSeconds),
			VisibilityTimeout:   aws.Int64(c.ClientOptions.VisibilityTimeout),
			AttributeNames:      []*string{aws.String("All")},
		})
		if err != nil {
			c.Logger.Log("error receiving messages from queue %s: %v", queueName, err)
			return err
		}

		c.Logger.Log("received %d messages from queue %s", len(result.Messages), queueName)

		for _, sqsMessage := range result.Messages {
			select {
			case ch <- sqsMessage:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (c *Consumer) handleMessage(ctx context.Context, queueURL string, sqsMessage *sqs.Message) {
	if c.Handler == nil {
		c.Logger.Log("no message handler configured")
		return
	}

	msg := NewMessage(sqsMessage)
	resultErr := c.Handler(ctx, msg)

	if resultErr == nil {
		c.deleteMessage(queueURL, msg)
		return
	}

	if errors.Is(resultErr, ErrDrop) {
		c.Logger.Log("dropping message with ID: %s", msg.Metadata.MessageId)
		c.deleteMessage(queueURL, msg)
		return
	}

	c.Logger.Log("handler returned error for message ID %s: %v", msg.Metadata.MessageId, resultErr)
	c.retryMessage(queueURL, msg)
}

func (c *Consumer) deleteMessage(queueURL string, msg *Message) {
	_, err := c.Client.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: aws.String(msg.Metadata.ReceiptHandle),
	})
	if err != nil {
		c.Logger.Log("error deleting message with ID %s: %v", msg.Metadata.MessageId, err)
		return
	}

	c.Logger.Log("message handled ID: %s", msg.Metadata.MessageId)
}

func (c *Consumer) retryMessage(queueURL string, msg *Message) {
	attempts := receiveAttempts(msg.Metadata.MessageAttributes["ApproximateReceiveCount"])
	backoff := int64(math.Pow(c.ClientOptions.BackoffMultiplier, float64(attempts)))

	_, err := c.Client.ChangeMessageVisibility(&sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(queueURL),
		ReceiptHandle:     aws.String(msg.Metadata.ReceiptHandle),
		VisibilityTimeout: aws.Int64(backoff),
	})
	if err != nil {
		c.Logger.Log("error updating visibility for message ID %s: %v", msg.Metadata.MessageId, err)
		return
	}

	c.Logger.Log("failed to handle message with ID: %s", msg.Metadata.MessageId)
}

func queueNameFromURL(queueURL string) string {
	parts := strings.Split(queueURL, "/")
	return parts[len(parts)-1]
}

func receiveAttempts(raw string) int {
	attempts, err := strconv.Atoi(raw)
	if err != nil || attempts < 1 {
		return 1
	}

	return attempts
}
