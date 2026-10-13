//go:build integration

package gosqs_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	gosqs "github.com/inaciogu/go-sqs/v2"
	"github.com/inaciogu/go-sqs/v2/internal/localdev"
	"github.com/stretchr/testify/require"
)

type integrationSettlement struct {
	queueURL   string
	visibility int32
	err        error
}

// observedSQS delegates every call to the real SDK; it never fabricates responses.
type observedSQS struct {
	*sqs.Client
	deleted     chan integrationSettlement
	changed     chan integrationSettlement
	polling     chan struct{}
	markPolling func()
}

func observeSQS(client *sqs.Client) *observedSQS {
	c := &observedSQS{Client: client, deleted: make(chan integrationSettlement, 16), changed: make(chan integrationSettlement, 16), polling: make(chan struct{})}
	c.markPolling = sync.OnceFunc(func() { close(c.polling) })
	return c
}

func (c *observedSQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, options ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	c.markPolling()
	return c.Client.ReceiveMessage(ctx, in, options...)
}

func (c *observedSQS) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, options ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	out, err := c.Client.DeleteMessage(ctx, in, options...)
	c.deleted <- integrationSettlement{queueURL: aws.ToString(in.QueueUrl), err: err}
	return out, err
}

func (c *observedSQS) ChangeMessageVisibility(ctx context.Context, in *sqs.ChangeMessageVisibilityInput, options ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	out, err := c.Client.ChangeMessageVisibility(ctx, in, options...)
	c.changed <- integrationSettlement{queueURL: aws.ToString(in.QueueUrl), visibility: in.VisibilityTimeout, err: err}
	return out, err
}

func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func integrationQueue(t *testing.T, ctx context.Context, clients *localdev.Clients, name string) *localdev.Resources {
	t.Helper()
	r := &localdev.Resources{Clients: clients}
	t.Cleanup(func() { require.NoError(t, r.Cleanup(), "clean up test resources") })
	require.NoError(t, r.CreateQueue(ctx, name))
	return r
}

func integrationSend(t *testing.T, ctx context.Context, r *localdev.Resources, body string, attrs map[string]types.MessageAttributeValue) {
	t.Helper()
	_, err := r.Clients.SQS.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(r.QueueURL), MessageBody: aws.String(body), MessageAttributes: attrs})
	require.NoError(t, err)
}

func integrationAwait[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatalf("integration operation did not complete: %v", ctx.Err())
		var zero T
		return zero
	}
}

type integrationExecution struct {
	cancel context.CancelFunc
	done   <-chan error
}

func integrationStart(t *testing.T, ctx context.Context, client *observedSQS, handler gosqs.MessageHandler, options gosqs.ConsumerOptions) integrationExecution {
	t.Helper()
	options.Client = client
	options.Logger = quietLogger()
	options.WaitTime = gosqs.Duration(time.Second)
	options.VisibilityTimeout = gosqs.Duration(time.Second)
	options.ShutdownTimeout = gosqs.Duration(5 * time.Second)
	options.BackoffMultiplier = 1
	consumer, err := gosqs.NewConsumer(ctx, handler, options)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() { defer close(exited); done <- consumer.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(6 * time.Second):
			t.Error("consumer did not exit during cleanup")
		}
	})
	return integrationExecution{cancel: cancel, done: done}
}

func integrationStop(t *testing.T, ctx context.Context, execution integrationExecution) {
	t.Helper()
	execution.cancel()
	err := integrationAwait(t, ctx, execution.done)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, gosqs.ErrShutdownTimeout)
}

// A two-second long poll crosses the consumer's one-second visibility window.
// An undeleted message would become visible and be returned during that window.
func integrationAbsent(t *testing.T, ctx context.Context, r *localdev.Resources) {
	t.Helper()
	result, err := r.Clients.SQS.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: aws.String(r.QueueURL), WaitTimeSeconds: 2, MaxNumberOfMessages: 1})
	require.NoError(t, err)
	require.Empty(t, result.Messages, "acknowledged message became visible again")
}

func TestMiniStack(t *testing.T) {
	clients := localdev.NewClients()
	require.NoError(t, clients.WaitReady(t.Context()))

	for _, outcome := range []struct {
		name string
		err  error
	}{
		{name: "success"}, {name: "wrapped drop", err: fmt.Errorf("discard: %w", gosqs.ErrDrop)},
	} {
		t.Run(outcome.name, func(t *testing.T) {
			ctx := integrationContext(t)
			r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-outcome-"))
			integrationSend(t, ctx, r, `{"id":"order"}`, nil)
			client := observeSQS(clients.SQS)
			messages := make(chan *gosqs.Message, 1)
			execution := integrationStart(t, ctx, client, func(_ context.Context, m *gosqs.Message) error { messages <- m; return outcome.err }, gosqs.ConsumerOptions{QueueName: r.QueueName})
			require.Equal(t, `{"id":"order"}`, integrationAwait(t, ctx, messages).Content)
			settled := integrationAwait(t, ctx, client.deleted)
			require.NoError(t, settled.err)
			require.Equal(t, r.QueueURL, settled.queueURL)
			integrationStop(t, ctx, execution)
			integrationAbsent(t, ctx, r)
		})
	}

	t.Run("retry", func(t *testing.T) {
		ctx := integrationContext(t)
		r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-retry-"))
		integrationSend(t, ctx, r, "retry me", nil)
		client := observeSQS(clients.SQS)
		counts := make(chan string, 4)
		var attempts atomic.Int32
		execution := integrationStart(t, ctx, client, func(_ context.Context, m *gosqs.Message) error {
			counts <- m.Metadata.SystemAttributes["ApproximateReceiveCount"]
			if attempts.Add(1) == 1 {
				return errors.New("transient failure")
			}
			return nil
		}, gosqs.ConsumerOptions{QueueName: r.QueueName})
		first, err := strconv.Atoi(integrationAwait(t, ctx, counts))
		require.NoError(t, err)
		require.Equal(t, 1, first)
		changed := integrationAwait(t, ctx, client.changed)
		require.NoError(t, changed.err)
		require.Equal(t, int32(1), changed.visibility)
		second, err := strconv.Atoi(integrationAwait(t, ctx, counts))
		require.NoError(t, err)
		require.Greater(t, second, first)
		require.NoError(t, integrationAwait(t, ctx, client.deleted).err)
		integrationStop(t, ctx, execution)
		integrationAbsent(t, ctx, r)
	})

	t.Run("SQS attributes", func(t *testing.T) {
		ctx := integrationContext(t)
		r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-attrs-"))
		integrationSend(t, ctx, r, "attributes", map[string]types.MessageAttributeValue{
			"text":   {DataType: aws.String("String"), StringValue: aws.String("hello")},
			"number": {DataType: aws.String("Number"), StringValue: aws.String("42")},
			"binary": {DataType: aws.String("Binary"), BinaryValue: []byte{0, 1, 255}},
		})
		client := observeSQS(clients.SQS)
		messages := make(chan *gosqs.Message, 1)
		execution := integrationStart(t, ctx, client, func(_ context.Context, m *gosqs.Message) error { messages <- m; return nil }, gosqs.ConsumerOptions{QueueName: r.QueueName})
		message := integrationAwait(t, ctx, messages)
		require.Equal(t, "attributes", message.Content)
		require.Equal(t, "String", message.Metadata.MessageAttributes["text"].DataType)
		require.Equal(t, "hello", aws.ToString(message.Metadata.MessageAttributes["text"].StringValue))
		require.Equal(t, "Number", message.Metadata.MessageAttributes["number"].DataType)
		require.Equal(t, "42", aws.ToString(message.Metadata.MessageAttributes["number"].StringValue))
		require.Equal(t, "Binary", message.Metadata.MessageAttributes["binary"].DataType)
		require.Equal(t, []byte{0, 1, 255}, message.Metadata.MessageAttributes["binary"].BinaryValue)
		require.NoError(t, integrationAwait(t, ctx, client.deleted).err)
		integrationStop(t, ctx, execution)
	})

	t.Run("prefix", func(t *testing.T) {
		ctx := integrationContext(t)
		prefix := localdev.UniqueName("it-prefix-")
		first := integrationQueue(t, ctx, clients, prefix+"-a")
		second := integrationQueue(t, ctx, clients, prefix+"-b")
		integrationSend(t, ctx, first, "a", nil)
		integrationSend(t, ctx, second, "b", nil)
		client := observeSQS(clients.SQS)
		messages := make(chan *gosqs.Message, 4)
		execution := integrationStart(t, ctx, client, func(_ context.Context, m *gosqs.Message) error { messages <- m; return nil }, gosqs.ConsumerOptions{QueuePrefix: prefix, MaxNumberOfMessages: 1})
		bodies := map[string]string{}
		for range 2 {
			m := integrationAwait(t, ctx, messages)
			bodies[m.Metadata.QueueURL] = m.Content
			require.NoError(t, integrationAwait(t, ctx, client.deleted).err)
		}
		require.Equal(t, map[string]string{first.QueueURL: "a", second.QueueURL: "b"}, bodies)
		integrationStop(t, ctx, execution)
		consumer, err := gosqs.NewConsumer(ctx, func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{QueuePrefix: localdev.UniqueName("it-missing-"), Client: clients.SQS, Logger: quietLogger()})
		require.NoError(t, err)
		require.ErrorIs(t, consumer.Run(ctx), gosqs.ErrNoQueues)
	})

	t.Run("cancel empty polling", func(t *testing.T) {
		ctx := integrationContext(t)
		r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-empty-"))
		client := observeSQS(clients.SQS)
		execution := integrationStart(t, ctx, client, func(context.Context, *gosqs.Message) error { return errors.New("unexpected message") }, gosqs.ConsumerOptions{QueueName: r.QueueName})
		integrationAwait(t, ctx, client.polling)
		stopping, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		integrationStop(t, stopping, execution)
	})

	t.Run("drain active handler", func(t *testing.T) {
		ctx := integrationContext(t)
		r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-drain-"))
		integrationSend(t, ctx, r, "drain", nil)
		client := observeSQS(clients.SQS)
		started := make(chan struct{})
		release := make(chan struct{})
		live := make(chan error, 1)
		markStarted := sync.OnceFunc(func() { close(started) })
		execution := integrationStart(t, ctx, client, func(work context.Context, _ *gosqs.Message) error {
			markStarted()
			select {
			case <-release:
			case <-work.Done():
			}
			live <- work.Err()
			return work.Err()
		}, gosqs.ConsumerOptions{QueueName: r.QueueName})
		integrationAwait(t, ctx, started)
		execution.cancel()
		close(release)
		require.NoError(t, integrationAwait(t, ctx, live))
		require.NoError(t, integrationAwait(t, ctx, client.deleted).err)
		integrationStop(t, ctx, execution)
		integrationAbsent(t, ctx, r)
	})

	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("SNS raw=%t", raw), func(t *testing.T) {
			ctx := integrationContext(t)
			r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-sns-"))
			require.NoError(t, r.Subscribe(ctx, raw))
			body := `{"id":"sns-order"}`
			_, err := clients.SNS.Publish(ctx, &sns.PublishInput{TopicArn: aws.String(r.TopicARN), Message: aws.String(body), MessageAttributes: map[string]snstypes.MessageAttributeValue{
				"text":   {DataType: aws.String("String"), StringValue: aws.String("hello")},
				"number": {DataType: aws.String("Number"), StringValue: aws.String("42")},
			}})
			require.NoError(t, err)
			format := gosqs.MessageFormatSNS
			if raw {
				format = gosqs.MessageFormatSQS
			}
			client := observeSQS(clients.SQS)
			messages := make(chan *gosqs.Message, 1)
			execution := integrationStart(t, ctx, client, func(_ context.Context, m *gosqs.Message) error { messages <- m; return nil }, gosqs.ConsumerOptions{QueueName: r.QueueName, MessageFormat: format})
			message := integrationAwait(t, ctx, messages)
			require.Equal(t, body, message.Content)
			require.Equal(t, "String", message.Metadata.MessageAttributes["text"].DataType)
			require.Equal(t, "hello", aws.ToString(message.Metadata.MessageAttributes["text"].StringValue))
			require.Equal(t, "Number", message.Metadata.MessageAttributes["number"].DataType)
			require.Equal(t, "42", aws.ToString(message.Metadata.MessageAttributes["number"].StringValue))
			require.NoError(t, integrationAwait(t, ctx, client.deleted).err)
			integrationStop(t, ctx, execution)
		})
	}

	t.Run("SNS binary envelope via SQS", func(t *testing.T) {
		ctx := integrationContext(t)
		r := integrationQueue(t, ctx, clients, localdev.UniqueName("it-sns-binary-"))
		// MiniStack 1.5.22 loses BinaryValue when building SNS envelopes. This
		// valid fixture tests our decoder without adapting it to that behavior.
		integrationSend(t, ctx, r, `{"Type":"Notification","TopicArn":"arn:aws:sns:us-east-1:000000000000:fixture","MessageId":"fixture-id","Message":"binary","MessageAttributes":{"blob":{"Type":"Binary","Value":"AAH/"}}}`, nil)
		client := observeSQS(clients.SQS)
		messages := make(chan *gosqs.Message, 1)
		execution := integrationStart(t, ctx, client, func(_ context.Context, m *gosqs.Message) error { messages <- m; return nil }, gosqs.ConsumerOptions{QueueName: r.QueueName, MessageFormat: gosqs.MessageFormatSNS})
		message := integrationAwait(t, ctx, messages)
		require.Equal(t, "binary", message.Content)
		require.Equal(t, []byte{0, 1, 255}, message.Metadata.MessageAttributes["blob"].BinaryValue)
		require.NoError(t, integrationAwait(t, ctx, client.deleted).err)
		integrationStop(t, ctx, execution)
	})
}
