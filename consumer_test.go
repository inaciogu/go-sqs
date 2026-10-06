package gosqs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	gosqs "github.com/inaciogu/go-sqs/v2"
	"github.com/stretchr/testify/require"
)

type fakeClient struct {
	get        func(context.Context, *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error)
	list       func(context.Context, *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error)
	receive    func(context.Context, *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
	delete     func(context.Context, *sqs.DeleteMessageInput) error
	visibility func(context.Context, *sqs.ChangeMessageVisibilityInput) error
}

func (f *fakeClient) GetQueueUrl(ctx context.Context, in *sqs.GetQueueUrlInput, _ ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	if f.get != nil {
		return f.get(ctx, in)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &sqs.GetQueueUrlOutput{QueueUrl: aws.String("https://example.com/queue")}, nil
}
func (f *fakeClient) ListQueues(ctx context.Context, in *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	if f.list != nil {
		return f.list(ctx, in)
	}
	return &sqs.ListQueuesOutput{}, nil
}
func (f *fakeClient) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if f.receive != nil {
		return f.receive(ctx, in)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *fakeClient) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	if f.delete != nil {
		return nil, f.delete(ctx, in)
	}
	return &sqs.DeleteMessageOutput{}, nil
}
func (f *fakeClient) ChangeMessageVisibility(ctx context.Context, in *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	if f.visibility != nil {
		return nil, f.visibility(ctx, in)
	}
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func newConsumer(t *testing.T, f *fakeClient, h gosqs.MessageHandler, o gosqs.ConsumerOptions) *gosqs.Consumer {
	t.Helper()
	o.Client = f
	if o.QueueName == "" && o.QueuePrefix == "" {
		o.QueueName = "test"
	}
	if o.Logger == nil {
		o.Logger = quietLogger()
	}
	c, err := gosqs.NewConsumer(t.Context(), h, o)
	require.NoError(t, err)
	return c
}
func message(body string) types.Message {
	return types.Message{MessageId: aws.String("id"), ReceiptHandle: aws.String("receipt"), Body: aws.String(body), Attributes: map[string]string{"ApproximateReceiveCount": "3"}}
}
func oneMessage(raw types.Message) func(context.Context, *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
	var delivered atomic.Bool
	return func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if !delivered.Swap(true) {
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{raw}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
}
func wait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not complete")
		var zero T
		return zero
	}
}
func run(c *gosqs.Consumer, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	return done
}

func TestConsumerValidation(t *testing.T) {
	h := func(context.Context, *gosqs.Message) error { return nil }
	for _, o := range []gosqs.ConsumerOptions{
		{}, {QueueName: "q", QueuePrefix: "p"}, {QueueName: "q", MaxConcurrency: -1}, {QueueName: "q", MaxNumberOfMessages: 11},
		{QueueName: "q", VisibilityTimeout: gosqs.Duration(-time.Second)}, {QueueName: "q", VisibilityTimeout: gosqs.Duration(12*time.Hour + time.Second)},
		{QueueName: "q", WaitTime: gosqs.Duration(time.Millisecond)}, {QueueName: "q", WaitTime: gosqs.Duration(21 * time.Second)},
		{QueueName: "q", ShutdownTimeout: gosqs.Duration(-1)}, {QueueName: "q", BackoffMultiplier: .5},
		{QueueName: "q", BackoffMultiplier: math.Inf(1)}, {QueueName: "q", BackoffMultiplier: math.NaN()},
		{QueueName: "q", MessageFormat: 99}, {QueueName: "q", Region: "x"}, {QueueName: "q", Endpoint: "http://localhost"},
	} {
		o.Client = &fakeClient{}
		c, err := gosqs.NewConsumer(t.Context(), h, o)
		require.Error(t, err)
		require.Nil(t, c)
	}
	_, err := gosqs.NewConsumer(nil, h, gosqs.ConsumerOptions{QueueName: "q"})
	require.Error(t, err)
	_, err = gosqs.NewConsumer(t.Context(), nil, gosqs.ConsumerOptions{QueueName: "q"})
	require.Error(t, err)
}

func TestDurationDefaultsAndCopies(t *testing.T) {
	for _, explicitZero := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "explicit zero"}[explicitZero], func(t *testing.T) {
			sentinel := errors.New("polling failed")
			inputs := make(chan *sqs.ReceiveMessageInput, 1)
			f := &fakeClient{receive: func(_ context.Context, in *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
				inputs <- in
				return nil, sentinel
			}}
			o := gosqs.ConsumerOptions{}
			if explicitZero {
				o.VisibilityTimeout = gosqs.Duration(0)
				o.WaitTime = gosqs.Duration(0)
			}
			c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, o)
			if explicitZero {
				*o.VisibilityTimeout = time.Hour
				*o.WaitTime = time.Hour
			}
			require.ErrorIs(t, c.Run(t.Context()), sentinel)
			in := wait(t, inputs)
			require.Equal(t, int32(10), in.MaxNumberOfMessages)
			if explicitZero {
				require.Zero(t, in.VisibilityTimeout)
				require.Zero(t, in.WaitTimeSeconds)
			} else {
				require.Equal(t, int32(30), in.VisibilityTimeout)
				require.Equal(t, int32(20), in.WaitTimeSeconds)
			}
			require.Equal(t, []string{"All"}, in.MessageAttributeNames)
			require.Equal(t, []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll}, in.MessageSystemAttributeNames)
		})
	}
}

func TestDiscoveryPaginationAndNoQueues(t *testing.T) {
	var pages int
	f := &fakeClient{list: func(_ context.Context, in *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error) {
		require.Equal(t, "prefix", aws.ToString(in.QueueNamePrefix))
		require.Equal(t, int32(1000), aws.ToInt32(in.MaxResults))
		pages++
		if pages == 1 {
			return &sqs.ListQueuesOutput{QueueUrls: []string{"one"}, NextToken: aws.String("next")}, nil
		}
		require.Equal(t, "next", aws.ToString(in.NextToken))
		return &sqs.ListQueuesOutput{QueueUrls: []string{"two"}}, nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	seen := make(chan string, 2)
	f.receive = func(ctx context.Context, in *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		seen <- aws.ToString(in.QueueUrl)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{QueuePrefix: "prefix", MaxNumberOfMessages: 1, MaxConcurrency: 2})
	done := run(c, ctx)
	a, b := wait(t, seen), wait(t, seen)
	require.ElementsMatch(t, []string{"one", "two"}, []string{a, b})
	cancel()
	require.ErrorIs(t, wait(t, done), context.Canceled)
	require.Equal(t, 2, pages)
	empty := newConsumer(t, &fakeClient{}, func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{QueuePrefix: "prefix"})
	require.ErrorIs(t, empty.Run(t.Context()), gosqs.ErrNoQueues)
}

func TestDiscoveryCancellation(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		started := make(chan struct{})
		block := func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
		f := &fakeClient{get: func(ctx context.Context, _ *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error) {
			return nil, block(ctx)
		}, list: func(ctx context.Context, _ *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error) {
			return nil, block(ctx)
		}}
		o := gosqs.ConsumerOptions{QueueName: "q"}
		if prefix {
			o.QueueName = ""
			o.QueuePrefix = "p"
		}
		c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, o)
		ctx, cancel := context.WithCancel(t.Context())
		done := run(c, ctx)
		wait(t, started)
		cancel()
		require.ErrorIs(t, wait(t, done), context.Canceled)
	}
}

func TestGracefulShutdownAcknowledgesWithLiveContext(t *testing.T) {
	type key struct{}
	started, release := make(chan context.Context, 1), make(chan struct{})
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "value"))
	defer cancel()
	ack := make(chan error, 1)
	f := &fakeClient{receive: oneMessage(message("{}")), delete: func(ctx context.Context, _ *sqs.DeleteMessageInput) error { ack <- ctx.Err(); return nil }}
	c := newConsumer(t, f, func(ctx context.Context, _ *gosqs.Message) error { started <- ctx; <-release; return nil }, gosqs.ConsumerOptions{})
	done := run(c, ctx)
	workCtx := wait(t, started)
	require.Equal(t, "value", workCtx.Value(key{}))
	cancel()
	require.NoError(t, workCtx.Err())
	select {
	case <-done:
		t.Fatal("Run returned before draining handler")
	default:
	}
	close(release)
	require.ErrorIs(t, wait(t, done), context.Canceled)
	require.NoError(t, wait(t, ack))
}

func TestShutdownTimeoutAndExclusiveRun(t *testing.T) {
	started, release := make(chan context.Context, 1), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newConsumer(t, &fakeClient{receive: oneMessage(message("{}"))}, func(ctx context.Context, _ *gosqs.Message) error { started <- ctx; <-release; return nil }, gosqs.ConsumerOptions{ShutdownTimeout: gosqs.Duration(20 * time.Millisecond)})
	done := run(c, ctx)
	workCtx := wait(t, started)
	require.ErrorIs(t, c.Run(t.Context()), gosqs.ErrAlreadyRunning)
	cancel()
	err := wait(t, done)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, gosqs.ErrShutdownTimeout)
	require.ErrorIs(t, workCtx.Err(), context.Canceled)
	require.ErrorIs(t, c.Run(t.Context()), gosqs.ErrAlreadyRunning)
	close(release)
	// A new Run is allowed only after the old worker and confirmation have exited.
	retryCtx, stop := context.WithCancel(t.Context())
	stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = c.Run(retryCtx)
		if !errors.Is(err, gosqs.ErrAlreadyRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("consumer never became idle")
		}
		runtime.Gosched()
	}
	require.ErrorIs(t, err, context.Canceled)
}

func TestGlobalConcurrencyAcrossQueues(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var active, maxActive, requests atomic.Int32
	var delivered sync.Map
	ready := make(chan struct{})
	var arrivals atomic.Int32
	f := &fakeClient{list: func(context.Context, *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error) {
		return &sqs.ListQueuesOutput{QueueUrls: []string{"one", "two"}}, nil
	}}
	f.receive = func(ctx context.Context, in *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		requests.Add(1)
		if _, loaded := delivered.LoadOrStore(aws.ToString(in.QueueUrl), true); !loaded {
			if arrivals.Add(1) == 2 {
				close(ready)
			}
			select {
			case <-ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{message("{}")}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error {
		n := active.Add(1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return nil
	}, gosqs.ConsumerOptions{QueuePrefix: "p", MaxConcurrency: 2, MaxNumberOfMessages: 1})
	done := run(c, ctx)
	wait(t, started)
	wait(t, started)
	require.Equal(t, int32(2), maxActive.Load())
	require.Equal(t, int32(2), requests.Load())
	cancel()
	close(release)
	require.ErrorIs(t, wait(t, done), context.Canceled)
}

func TestEmptyAndPartialBatchesReleaseCapacity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	batchSizes := make(chan int32, 3)
	handlerStarted, release := make(chan struct{}), make(chan struct{})
	f := &fakeClient{receive: func(ctx context.Context, in *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		n := calls.Add(1)
		batchSizes <- in.MaxNumberOfMessages
		switch n {
		case 1:
			return &sqs.ReceiveMessageOutput{}, nil
		case 2:
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{message("{}")}}, nil
		default:
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { close(handlerStarted); <-release; return nil }, gosqs.ConsumerOptions{MaxConcurrency: 3})
	done := run(c, ctx)
	wait(t, handlerStarted)
	require.Equal(t, int32(3), wait(t, batchSizes))
	require.Equal(t, int32(3), wait(t, batchSizes))
	require.Equal(t, int32(2), wait(t, batchSizes))
	cancel()
	close(release)
	require.ErrorIs(t, wait(t, done), context.Canceled)
}

func TestHandlerOutcomesAndOperationalErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name                 string
		handlerErr, errorAWS error
		operation            string
	}{
		{name: "success"}, {name: "wrapped drop", handlerErr: errors.Join(gosqs.ErrDrop, boom)},
		{name: "handler retry", handlerErr: boom}, {name: "delete failure", errorAWS: boom, operation: "delete"},
		{name: "visibility failure", handlerErr: boom, errorAWS: boom, operation: "change_visibility"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			events := make(chan error, 3)
			raw := message("{}")
			raw.MessageAttributes = map[string]types.MessageAttributeValue{"ApproximateReceiveCount": {DataType: aws.String("Number"), StringValue: aws.String("20")}}
			f := &fakeClient{receive: oneMessage(raw)}
			f.delete = func(ctx context.Context, in *sqs.DeleteMessageInput) error {
				require.NoError(t, ctx.Err())
				require.Equal(t, "receipt", aws.ToString(in.ReceiptHandle))
				cancel()
				return tc.errorAWS
			}
			f.visibility = func(ctx context.Context, in *sqs.ChangeMessageVisibilityInput) error {
				require.NoError(t, ctx.Err())
				require.Equal(t, int32(8), in.VisibilityTimeout)
				cancel()
				return tc.errorAWS
			}
			c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return tc.handlerErr }, gosqs.ConsumerOptions{OnError: func(ctx context.Context, err error) { require.NoError(t, ctx.Err()); events <- err }})
			require.ErrorIs(t, c.Run(ctx), context.Canceled)
			if tc.handlerErr != nil && !errors.Is(tc.handlerErr, gosqs.ErrDrop) {
				event := wait(t, events)
				var op *gosqs.OperationError
				require.ErrorAs(t, event, &op)
				require.Equal(t, "handler", op.Operation)
				require.ErrorIs(t, event, boom)
			}
			if tc.operation != "" {
				event := wait(t, events)
				var op *gosqs.OperationError
				require.ErrorAs(t, event, &op)
				require.Equal(t, tc.operation, op.Operation)
				require.Equal(t, "id", op.MessageID)
				require.Equal(t, "https://example.com/queue", op.QueueURL)
				require.ErrorIs(t, event, boom)
			}
			require.Empty(t, events)
		})
	}
}

func TestOperationalFailureDoesNotStopConsumption(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var receiveCalls, ackCalls atomic.Int32
	f := &fakeClient{receive: func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if receiveCalls.Add(1) <= 2 {
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{message("{}")}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	f.delete = func(context.Context, *sqs.DeleteMessageInput) error {
		if ackCalls.Add(1) == 1 {
			return errors.New("ack failed")
		}
		cancel()
		return nil
	}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{MaxConcurrency: 1})
	require.ErrorIs(t, c.Run(ctx), context.Canceled)
	require.Equal(t, int32(2), ackCalls.Load())
}

func TestLoggingLevelsAndNoSensitiveFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &fakeClient{receive: oneMessage(message("secret-payload")), visibility: func(context.Context, *sqs.ChangeMessageVisibilityInput) error {
		cancel()
		return errors.New("secret-receipt")
	}}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return errors.New("secret-payload") }, gosqs.ConsumerOptions{Logger: logger, LogLevel: slog.LevelError})
	require.ErrorIs(t, c.Run(ctx), context.Canceled)
	logs := output.String()
	require.Contains(t, logs, `"level":"WARN"`)
	require.Contains(t, logs, `"level":"ERROR"`)
	require.NotContains(t, logs, "DEBUG")
	require.NotContains(t, logs, "secret-payload")
	require.NotContains(t, logs, "secret-receipt")
	require.Contains(t, logs, `"message_id":"id"`)
}

type runnerFunc func(context.Context) error

func (f runnerFunc) Run(ctx context.Context) error { return f(ctx) }
func TestRunAllPreservesFailureAndShutdownErrors(t *testing.T) {
	boom := errors.New("poll failed")
	err := gosqs.RunAll(t.Context(), runnerFunc(func(context.Context) error { return boom }), runnerFunc(func(ctx context.Context) error { <-ctx.Done(); return errors.Join(ctx.Err(), gosqs.ErrShutdownTimeout) }))
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, gosqs.ErrShutdownTimeout)
	require.NoError(t, gosqs.RunAll(t.Context()))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, gosqs.RunAll(ctx, runnerFunc(func(ctx context.Context) error { return ctx.Err() })), context.Canceled)
}

func TestExplicitSNSAndDecodeFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body, content string
		invalid             bool
	}{
		{name: "valid", body: `{"Type":"Notification","TopicArn":"arn:aws:sns:us-east-1:123:topic","MessageId":"sns-id","Message":"hello","MessageAttributes":{"blob":{"Type":"Binary","Value":"aGk="}}}`, content: "hello"},
		{name: "empty", body: `{"Type":"Notification","TopicArn":"topic","MessageId":"id","Message":""}`},
		{name: "invalid", body: `{"Message":"hello"}`, invalid: true},
		{name: "invalid binary", body: `{"Type":"Notification","TopicArn":"topic","MessageId":"id","Message":"hello","MessageAttributes":{"blob":{"Type":"Binary","Value":"!"}}}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var handled atomic.Bool
			events := make(chan error, 2)
			f := &fakeClient{receive: oneMessage(message(tc.body)), delete: func(context.Context, *sqs.DeleteMessageInput) error { cancel(); return nil }, visibility: func(context.Context, *sqs.ChangeMessageVisibilityInput) error { cancel(); return nil }}
			c := newConsumer(t, f, func(_ context.Context, m *gosqs.Message) error {
				handled.Store(true)
				require.Equal(t, tc.content, m.Content)
				if tc.name == "valid" {
					require.Equal(t, []byte("hi"), m.Metadata.MessageAttributes["blob"].BinaryValue)
				}
				return nil
			}, gosqs.ConsumerOptions{MessageFormat: gosqs.MessageFormatSNS, OnError: func(_ context.Context, err error) { events <- err }})
			require.ErrorIs(t, c.Run(ctx), context.Canceled)
			require.Equal(t, !tc.invalid, handled.Load())
			if tc.invalid {
				var op *gosqs.OperationError
				require.ErrorAs(t, wait(t, events), &op)
				require.Equal(t, "decode", op.Operation)
			}
		})
	}
}

// Compile-time check of the SDK-compatible injection contract.
var _ gosqs.QueueClient = (*fakeClient)(nil)

func TestPollingFailureDrainsHandlers(t *testing.T) {
	boom := errors.New("polling failure")
	started, release, pollFailed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ack := make(chan error, 1)
	f := &fakeClient{receive: func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if calls.Add(1) == 1 {
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{message("{}")}}, nil
		}
		<-started
		close(pollFailed)
		return nil, boom
	}, delete: func(ctx context.Context, _ *sqs.DeleteMessageInput) error { ack <- ctx.Err(); return nil }}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { close(started); <-release; return nil }, gosqs.ConsumerOptions{MaxConcurrency: 2, MaxNumberOfMessages: 1})
	done := run(c, t.Context())
	wait(t, pollFailed)
	select {
	case <-done:
		t.Fatal("polling failure skipped drainage")
	default:
	}
	close(release)
	require.ErrorIs(t, wait(t, done), boom)
	require.NoError(t, wait(t, ack))
}

func TestRunAllPreservesChildDeadline(t *testing.T) {
	err := gosqs.RunAll(t.Context(), runnerFunc(func(context.Context) error { return context.DeadlineExceeded }), runnerFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestHandlerMetadataDoesNotChangeAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &fakeClient{receive: oneMessage(message("{}")), delete: func(_ context.Context, in *sqs.DeleteMessageInput) error {
		require.Equal(t, "receipt", aws.ToString(in.ReceiptHandle))
		cancel()
		return nil
	}}
	c := newConsumer(t, f, func(_ context.Context, m *gosqs.Message) error {
		m.Metadata.ReceiptHandle = "changed"
		m.Metadata.MessageID = "changed"
		return nil
	}, gosqs.ConsumerOptions{})
	require.ErrorIs(t, c.Run(ctx), context.Canceled)
}

func TestErrorCallbackIncludedInShutdownDeadline(t *testing.T) {
	entered, release := make(chan context.Context, 1), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newConsumer(t, &fakeClient{receive: oneMessage(message("{}"))}, func(context.Context, *gosqs.Message) error { return errors.New("handler failed") }, gosqs.ConsumerOptions{
		ShutdownTimeout: gosqs.Duration(20 * time.Millisecond), OnError: func(ctx context.Context, _ error) { entered <- ctx; <-release },
	})
	done := run(c, ctx)
	callbackCtx := wait(t, entered)
	cancel()
	err := wait(t, done)
	require.ErrorIs(t, err, gosqs.ErrShutdownTimeout)
	require.ErrorIs(t, callbackCtx.Err(), context.Canceled)
	close(release)
	// Ensure the callback and remaining confirmation have exited before test cleanup.
	retryCtx, stop := context.WithCancel(t.Context())
	stop()
	deadline := time.Now().Add(time.Second)
	for errors.Is(c.Run(retryCtx), gosqs.ErrAlreadyRunning) {
		if time.Now().After(deadline) {
			t.Fatal("callback did not exit")
		}
		runtime.Gosched()
	}
}
