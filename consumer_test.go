package gosqs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sqs"
	gosqs "github.com/inaciogu/go-sqs"
	"github.com/inaciogu/go-sqs/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type noopLogger struct{}

func (noopLogger) Log(string, ...interface{}) {}

func newTestConsumer(t *testing.T, sqsService *mocks.QueueClient, handler gosqs.MessageHandler, options gosqs.ConsumerOptions) *gosqs.Consumer {
	t.Helper()

	client, err := gosqs.NewConsumer(handler, options)
	assert.NoError(t, err)
	assert.NotNil(t, client)

	client.Client = sqsService
	client.Logger = noopLogger{}

	return client
}

type runAllConsumer struct {
	started chan struct{}
}

func (r *runAllConsumer) Run(ctx context.Context) error {
	r.started <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func TestNewConsumer(t *testing.T) {
	client, err := gosqs.NewConsumer(func(ctx context.Context, msg *gosqs.Message) error {
		return nil
	}, gosqs.ConsumerOptions{
		QueueName: "queue-name",
	})

	assert.NoError(t, err)
	assert.NotNil(t, client)
}

func TestQueueURL(t *testing.T) {
	mockSQS := new(mocks.QueueClient)
	mockSQS.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://fake-queue-url"),
	}, nil)

	client := newTestConsumer(t, mockSQS, func(ctx context.Context, msg *gosqs.Message) error {
		return nil
	}, gosqs.ConsumerOptions{
		QueueName: "fake-queue-name",
	})

	queueURL, err := client.QueueURL()

	assert.NoError(t, err)
	assert.Equal(t, "https://fake-queue-url", queueURL)
	mockSQS.AssertCalled(t, "GetQueueUrl", &sqs.GetQueueUrlInput{
		QueueName: aws.String("fake-queue-name"),
	})
}

func TestQueueURLs(t *testing.T) {
	mockSQS := new(mocks.QueueClient)
	mockSQS.On("ListQueues", mock.Anything).Return(&sqs.ListQueuesOutput{
		QueueUrls: []*string{
			aws.String("https://fake-queue-url"),
			aws.String("https://fake-queue-url-2"),
		},
	}, nil)

	client := newTestConsumer(t, mockSQS, func(ctx context.Context, msg *gosqs.Message) error {
		return nil
	}, gosqs.ConsumerOptions{
		QueueName:   "fake-queue-name",
		PrefixBased: true,
	})

	queueURLs, err := client.QueueURLs("fake-queue-name")

	assert.NoError(t, err)
	assert.Equal(t, []string{"https://fake-queue-url", "https://fake-queue-url-2"}, queueURLs)
	mockSQS.AssertCalled(t, "ListQueues", &sqs.ListQueuesInput{
		QueueNamePrefix: aws.String("fake-queue-name"),
	})
}

func TestRunDeletesMessage(t *testing.T) {
	mockSQS := new(mocks.QueueClient)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockSQS.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://example.com/queue"),
	}, nil)

	mockSQS.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{
		Messages: []*sqs.Message{
			{
				Body:          aws.String(`{"name":"alice"}`),
				ReceiptHandle: aws.String("receipt-handle"),
				MessageId:     aws.String("message-id"),
				MessageAttributes: map[string]*sqs.MessageAttributeValue{
					"ApproximateReceiveCount": {
						DataType:    aws.String("Number"),
						StringValue: aws.String("1"),
					},
				},
			},
		},
	}, nil).Times(2)
	mockSQS.On("DeleteMessage", mock.Anything).Return(&sqs.DeleteMessageOutput{}, nil)

	client := newTestConsumer(t, mockSQS, func(ctx context.Context, msg *gosqs.Message) error {
		cancel()
		return nil
	}, gosqs.ConsumerOptions{
		QueueName: "queue-name",
	})

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not stop")
	}

	mockSQS.AssertCalled(t, "DeleteMessage", &sqs.DeleteMessageInput{
		QueueUrl:      aws.String("https://example.com/queue"),
		ReceiptHandle: aws.String("receipt-handle"),
	})
}

func TestRunDropsMessage(t *testing.T) {
	mockSQS := new(mocks.QueueClient)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockSQS.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://example.com/queue"),
	}, nil)

	mockSQS.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{
		Messages: []*sqs.Message{
			{
				Body:          aws.String(`{"name":"alice"}`),
				ReceiptHandle: aws.String("receipt-handle"),
				MessageId:     aws.String("message-id"),
				MessageAttributes: map[string]*sqs.MessageAttributeValue{
					"ApproximateReceiveCount": {
						DataType:    aws.String("Number"),
						StringValue: aws.String("1"),
					},
				},
			},
		},
	}, nil).Times(2)
	mockSQS.On("DeleteMessage", mock.Anything).Return(&sqs.DeleteMessageOutput{}, nil)

	client := newTestConsumer(t, mockSQS, func(ctx context.Context, msg *gosqs.Message) error {
		cancel()
		return errors.Join(gosqs.ErrDrop, errors.New("permanent validation failure"))
	}, gosqs.ConsumerOptions{
		QueueName: "queue-name",
	})

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not stop")
	}

	mockSQS.AssertCalled(t, "DeleteMessage", &sqs.DeleteMessageInput{
		QueueUrl:      aws.String("https://example.com/queue"),
		ReceiptHandle: aws.String("receipt-handle"),
	})
}

func TestRunRetriesWithDefaultBackoff(t *testing.T) {
	mockSQS := new(mocks.QueueClient)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockSQS.On("GetQueueUrl", mock.Anything).Return(&sqs.GetQueueUrlOutput{
		QueueUrl: aws.String("https://example.com/queue"),
	}, nil)

	mockSQS.On("ReceiveMessage", mock.Anything).Return(&sqs.ReceiveMessageOutput{
		Messages: []*sqs.Message{
			{
				Body:          aws.String(`{"name":"alice"}`),
				ReceiptHandle: aws.String("receipt-handle"),
				MessageId:     aws.String("message-id"),
			},
		},
	}, nil).Times(2)
	mockSQS.On("ChangeMessageVisibility", mock.Anything).Return(&sqs.ChangeMessageVisibilityOutput{}, nil)

	client := newTestConsumer(t, mockSQS, func(ctx context.Context, msg *gosqs.Message) error {
		cancel()
		return errors.New("boom")
	}, gosqs.ConsumerOptions{
		QueueName: "queue-name",
	})

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not stop")
	}

	mockSQS.AssertCalled(t, "ChangeMessageVisibility", &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String("https://example.com/queue"),
		ReceiptHandle:     aws.String("receipt-handle"),
		VisibilityTimeout: aws.Int64(2),
	})
}

func TestRunAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	consumer1 := &runAllConsumer{started: make(chan struct{}, 1)}
	consumer2 := &runAllConsumer{started: make(chan struct{}, 1)}
	done := make(chan error, 1)

	go func() {
		done <- gosqs.RunAll(ctx, consumer1, consumer2)
	}()

	<-consumer1.started
	<-consumer2.started
	cancel()

	assert.ErrorIs(t, <-done, context.Canceled)
}
