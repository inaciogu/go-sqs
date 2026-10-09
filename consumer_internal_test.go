package gosqs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/require"
)

type visibilityClient struct {
	QueueClient
	input *sqs.ChangeMessageVisibilityInput
}

func (c *visibilityClient) ChangeMessageVisibility(_ context.Context, input *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	c.input = input
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}

func TestRetryRespectsRemainingVisibilityBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		expired bool
	}{
		{name: "capped exponential", elapsed: time.Minute},
		{name: "expired", elapsed: 12 * time.Hour, expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &visibilityClient{}
			var event error
			c := &Consumer{client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), config: consumerConfig{backoffMultiplier: math.MaxFloat64, onError: func(_ context.Context, err error) { event = err }}}
			c.retryMessage(t.Context(), &Message{Metadata: MessageMetadata{QueueURL: "queue", MessageID: "id", SystemAttributes: map[string]string{"ApproximateReceiveCount": "100"}}}, time.Now().Add(-tc.elapsed))
			if tc.expired {
				require.Nil(t, client.input)
				var operation *OperationError
				require.True(t, errors.As(event, &operation))
				require.Equal(t, "change_visibility", operation.Operation)
			} else {
				require.NotNil(t, client.input)
				require.Positive(t, client.input.VisibilityTimeout)
				require.LessOrEqual(t, client.input.VisibilityTimeout, int32((12*time.Hour-tc.elapsed-time.Second)/time.Second))
			}
		})
	}
}

func TestDefaultLoggerFiltersByLevel(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelInfo, slog.LevelError, slog.Level(100)} {
		c, err := NewConsumer(t.Context(), func(context.Context, *Message) error { return nil }, ConsumerOptions{QueueName: "queue", Client: &visibilityClient{}, LogLevel: level})
		require.NoError(t, err)
		require.False(t, c.logger.Enabled(t.Context(), slog.LevelDebug))
		require.Equal(t, level <= slog.LevelInfo, c.logger.Enabled(t.Context(), slog.LevelInfo))
	}
}

// This receiver isolates cancellation while a send has no available dispatcher.
type handoffClient struct {
	QueueClient
	received chan struct{}
}

func (c *handoffClient) ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	close(c.received)
	return &sqs.ReceiveMessageOutput{Messages: []types.Message{{}}}, nil
}

func TestPollCancellationWithoutChannelConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &handoffClient{received: make(chan struct{})}
	c := &Consumer{client: client, config: consumerConfig{maxNumberOfMessages: 10}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	messages := make(chan delivery)
	done := make(chan error, 1)
	go func() { done <- c.poll(ctx, "queue", messages) }()
	select {
	case <-client.received:
	case <-time.After(time.Second):
		t.Fatal("receiver did not return its batch")
	}
	select {
	case <-done:
		t.Fatal("poll returned without channel handoff or cancellation")
	default:
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt channel handoff")
	}
}
