package gosqs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	gosqs "github.com/inaciogu/go-sqs/v2"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	logglobal "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// All global-provider tests are serial and join workers before cleanup.
type memoryLogs struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *memoryLogs) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range records {
		e.records = append(e.records, r.Clone())
	}
	return nil
}
func (*memoryLogs) Shutdown(context.Context) error   { return nil }
func (*memoryLogs) ForceFlush(context.Context) error { return nil }
func (e *memoryLogs) snapshot() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdklog.Record(nil), e.records...)
}
func setupTelemetry(t *testing.T) (*sdkmetric.ManualReader, *memoryLogs) {
	t.Helper()
	oldMeter, oldLogger := otel.GetMeterProvider(), logglobal.GetLoggerProvider()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	logs := &memoryLogs{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logs)))
	otel.SetMeterProvider(mp)
	logglobal.SetLoggerProvider(lp)
	t.Cleanup(func() {
		otel.SetMeterProvider(oldMeter)
		logglobal.SetLoggerProvider(oldLogger)
		require.NoError(t, mp.Shutdown(context.Background()))
		require.NoError(t, lp.Shutdown(context.Background()))
	})
	return reader, logs
}
func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	result := map[string]metricdata.Metrics{}
	for _, scope := range rm.ScopeMetrics {
		require.Equal(t, "github.com/inaciogu/go-sqs/v2", scope.Scope.Name)
		for _, m := range scope.Metrics {
			result[m.Name] = m
		}
	}
	return result
}
func sumMetric(m metricdata.Metrics) int64 {
	var n int64
	if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
		for _, p := range sum.DataPoints {
			n += p.Value
		}
	}
	return n
}
func histogramCount(m metricdata.Metrics) uint64 {
	var n uint64
	if h, ok := m.Data.(metricdata.Histogram[float64]); ok {
		for _, p := range h.DataPoints {
			n += p.Count
		}
	}
	return n
}
func attr(set attribute.Set, key string) string {
	value, _ := set.Value(attribute.Key(key))
	return value.AsString()
}
func assertBalanced(t *testing.T, m map[string]metricdata.Metrics) {
	t.Helper()
	require.Zero(t, sumMetric(m["gosqs.workers.active"]))
}

func TestTelemetryMessageOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, result, action string
		handlerErr, ackErr   error
		format               gosqs.MessageFormat
		wantErrors           int64
	}{
		{name: "success", result: "success", action: "DeleteMessage"},
		{name: "wrapped drop", result: "drop", action: "DeleteMessage", handlerErr: fmt.Errorf("wrapped: %w", gosqs.ErrDrop)},
		{name: "retry", result: "error", action: "ChangeMessageVisibility", handlerErr: errors.New("secret"), wantErrors: 1},
		{name: "delete failure", result: "success", action: "DeleteMessage", ackErr: errors.New("secret"), wantErrors: 1},
		{name: "visibility failure", result: "error", action: "ChangeMessageVisibility", handlerErr: errors.New("secret"), ackErr: errors.New("secret"), wantErrors: 2},
		{name: "SNS decode", action: "ChangeMessageVisibility", format: gosqs.MessageFormatSNS, wantErrors: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, _ := setupTelemetry(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls atomic.Int32
			f := &fakeClient{receive: oneMessage(message("secret-payload"))}
			f.delete = func(context.Context, *sqs.DeleteMessageInput) error { cancel(); return tc.ackErr }
			f.visibility = func(context.Context, *sqs.ChangeMessageVisibilityInput) error { cancel(); return tc.ackErr }
			c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { calls.Add(1); return tc.handlerErr }, gosqs.ConsumerOptions{Telemetry: true, MessageFormat: tc.format})
			require.ErrorIs(t, c.Run(ctx), context.Canceled)
			metrics := collectMetrics(t, reader)
			require.Equal(t, int64(1), sumMetric(metrics["messaging.client.consumed.messages"]))
			require.Equal(t, tc.wantErrors, sumMetric(metrics["gosqs.errors"]))
			assertBalanced(t, metrics)
			require.Equal(t, uint64(1), histogramCount(metrics["gosqs.shutdown.duration"]))
			require.Equal(t, "{message}", metrics["messaging.client.consumed.messages"].Unit)
			if tc.result == "" {
				require.Zero(t, calls.Load())
				require.Zero(t, histogramCount(metrics["messaging.process.duration"]))
			} else {
				require.Equal(t, int32(1), calls.Load())
				h := metrics["messaging.process.duration"].Data.(metricdata.Histogram[float64])
				require.Len(t, h.DataPoints, 1)
				p := h.DataPoints[0]
				require.Equal(t, tc.result, attr(p.Attributes, "gosqs.handler.result"))
				require.Equal(t, "process", attr(p.Attributes, "messaging.operation.type"))
				require.Equal(t, "queue", attr(p.Attributes, "messaging.destination.name"))
				require.Equal(t, "example.com", attr(p.Attributes, "server.address"))
				require.Equal(t, "aws_sqs", attr(p.Attributes, "messaging.system"))
				require.Equal(t, []float64{.005, .01, .025, .05, .075, .1, .25, .5, .75, 1, 2.5, 5, 7.5, 10}, p.Bounds)
				require.NotContains(t, p.Attributes.ToSlice(), attribute.String("message_id", "id"))
				if tc.result != "error" {
					_, present := p.Attributes.Value("error.type")
					require.False(t, present)
				}
			}
			operations := metrics["messaging.client.operation.duration"].Data.(metricdata.Histogram[float64])
			var actionCount uint64
			for _, p := range operations.DataPoints {
				if attr(p.Attributes, "messaging.operation.name") == tc.action {
					actionCount += p.Count
					if tc.ackErr != nil {
						require.Equal(t, "aws", attr(p.Attributes, "error.type"))
					}
				}
			}
			require.Equal(t, uint64(1), actionCount)
		})
	}
}

func TestTelemetryEmptyPartialAndRedelivery(t *testing.T) {
	reader, _ := setupTelemetry(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var receives, deletes atomic.Int32
	f := &fakeClient{receive: func(ctx context.Context, in *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		switch receives.Add(1) {
		case 1:
			return &sqs.ReceiveMessageOutput{}, nil
		case 2, 3:
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{message("{}")}}, nil
		default:
			<-ctx.Done()
			return nil, ctx.Err()
		}
	}}
	f.delete = func(context.Context, *sqs.DeleteMessageInput) error {
		if deletes.Add(1) == 2 {
			cancel()
		}
		return nil
	}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{Telemetry: true, ReceiveWorkers: 3})
	require.ErrorIs(t, c.Run(ctx), context.Canceled)
	m := collectMetrics(t, reader)
	require.Equal(t, int64(2), sumMetric(m["messaging.client.consumed.messages"]))
	require.Equal(t, uint64(2), histogramCount(m["messaging.process.duration"]))
	require.Zero(t, sumMetric(m["gosqs.errors"]))
	assertBalanced(t, m)
}

func TestTelemetryShutdownTracksLateWorkerAndRerun(t *testing.T) {
	reader, _ := setupTelemetry(t)
	entered, release, ack := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &fakeClient{receive: oneMessage(message("{}"))}
	f.delete = func(context.Context, *sqs.DeleteMessageInput) error { close(ack); return nil }
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { close(entered); <-release; return nil }, gosqs.ConsumerOptions{Telemetry: true, ReceiveWorkers: 1, ShutdownTimeout: gosqs.Duration(0)})
	done := run(c, ctx)
	wait(t, entered)
	cancel()
	require.ErrorIs(t, wait(t, done), gosqs.ErrShutdownTimeout)
	m := collectMetrics(t, reader)
	require.Equal(t, int64(1), sumMetric(m["gosqs.workers.active"]))
	require.Equal(t, int64(1), sumMetric(m["gosqs.errors"]))
	require.ErrorIs(t, c.Run(t.Context()), gosqs.ErrAlreadyRunning)
	close(release)
	wait(t, ack)
	require.Eventually(t, func() bool {
		m := collectMetrics(t, reader)
		return sumMetric(m["gosqs.workers.active"]) == 0
	}, time.Second, time.Millisecond)
	// Retry after old workers have exited; discovery uses the already-canceled context.
	require.Eventually(t, func() bool { return !errors.Is(c.Run(ctx), gosqs.ErrAlreadyRunning) }, time.Second, time.Millisecond)
	assertBalanced(t, collectMetrics(t, reader))
}

func TestTelemetryFailuresAndPrefix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prefix  bool
		getErr  error
		receive func(context.Context, *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
		class   string
	}{
		{name: "AWS receive", receive: func(context.Context, *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
			return nil, errors.New("secret")
		}, class: "aws"},
		{name: "invalid receive", receive: func(context.Context, *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) { return nil, nil }, class: "invalid_response"},
		{name: "discovery", getErr: errors.New("secret"), class: "aws"},
		{name: "prefix", prefix: true, receive: func(context.Context, *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
			return nil, errors.New("secret")
		}, class: "aws"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, _ := setupTelemetry(t)
			f := &fakeClient{receive: tc.receive}
			if tc.getErr != nil {
				f.get = func(context.Context, *sqs.GetQueueUrlInput) (*sqs.GetQueueUrlOutput, error) { return nil, tc.getErr }
			}
			o := gosqs.ConsumerOptions{Telemetry: true}
			if tc.prefix {
				o.QueuePrefix = "orders-"
				f.list = func(context.Context, *sqs.ListQueuesInput) (*sqs.ListQueuesOutput, error) {
					return &sqs.ListQueuesOutput{QueueUrls: []string{"https://example.com/orders-a"}}, nil
				}
			}
			c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, o)
			require.Error(t, c.Run(t.Context()))
			m := collectMetrics(t, reader)
			require.Equal(t, int64(1), sumMetric(m["gosqs.errors"]))
			assertBalanced(t, m)
			for _, p := range m["gosqs.errors"].Data.(metricdata.Sum[int64]).DataPoints {
				require.Equal(t, tc.class, attr(p.Attributes, "error.type"))
				if tc.prefix {
					require.Equal(t, "orders-", attr(p.Attributes, "gosqs.consumer.queue_prefix"))
					_, present := p.Attributes.Value("messaging.destination.name")
					require.False(t, present)
				}
			}
		})
	}
}

func TestTelemetryLogsAndLoggerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		level  slog.Level
		custom bool
		want   int
	}{{"warnings", slog.LevelWarn, false, 2}, {"errors", slog.LevelError, false, 1}, {"custom", slog.LevelError, true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			_, logs := setupTelemetry(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f := &fakeClient{receive: oneMessage(message("secret-payload")), visibility: func(context.Context, *sqs.ChangeMessageVisibilityInput) error {
				cancel()
				return errors.New("secret-receipt")
			}}
			var output bytes.Buffer
			o := gosqs.ConsumerOptions{QueueName: "orders", Client: f, Telemetry: true, LogLevel: tc.level}
			if tc.custom {
				o.Logger = slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelWarn}))
			}
			c, err := gosqs.NewConsumer(t.Context(), func(context.Context, *gosqs.Message) error { return errors.New("secret-handler") }, o)
			require.NoError(t, err)
			require.ErrorIs(t, c.Run(ctx), context.Canceled)
			records := logs.snapshot()
			require.Len(t, records, tc.want)
			for _, r := range records {
				require.Equal(t, "github.com/inaciogu/go-sqs/v2", r.InstrumentationScope().Name)
				require.GreaterOrEqual(t, r.Severity(), otellog.SeverityWarn)
				r.WalkAttributes(func(kv otellog.KeyValue) bool {
					require.NotContains(t, kv.Value.String(), "secret")
					require.NotEqual(t, "receipt_handle", kv.Key)
					return true
				})
			}
			if tc.custom {
				require.Contains(t, output.String(), "WARN")
				require.NotContains(t, output.String(), "secret")
			}
		})
	}
}

func TestTelemetryDisabledAndNoop(t *testing.T) {
	reader, logs := setupTelemetry(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &fakeClient{receive: oneMessage(message("{}")), delete: func(context.Context, *sqs.DeleteMessageInput) error { cancel(); return nil }}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{})
	require.ErrorIs(t, c.Run(ctx), context.Canceled)
	require.Empty(t, collectMetrics(t, reader))
	require.Empty(t, logs.snapshot())
	otel.SetMeterProvider(metricnoop.NewMeterProvider())
	logglobal.SetLoggerProvider(lognoop.NewLoggerProvider())
	c, err := gosqs.NewConsumer(t.Context(), func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{QueueName: "orders", Client: &fakeClient{}, Telemetry: true})
	require.NoError(t, err)
	require.ErrorIs(t, c.Run(ctx), context.Canceled)
}

// Verify the combined receiver model and telemetry while all handlers overlap.
func TestTelemetryConcurrentReceivers(t *testing.T) {
	reader, _ := setupTelemetry(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	const count = 3
	entered := make(chan struct{}, count)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var receives atomic.Int32
	f := &fakeClient{receive: func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if receives.Add(1) <= count {
			return &sqs.ReceiveMessageOutput{Messages: []types.Message{message("{}")}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	c := newConsumer(t, f, func(context.Context, *gosqs.Message) error {
		entered <- struct{}{}
		<-release
		return nil
	}, gosqs.ConsumerOptions{Telemetry: true, ReceiveWorkers: count})
	done := run(c, ctx)
	for i := 0; i < count; i++ {
		wait(t, entered)
	}
	metrics := collectMetrics(t, reader)
	require.Equal(t, int64(count), sumMetric(metrics["gosqs.workers.active"]))
	require.Equal(t, int64(count), sumMetric(metrics["messaging.client.consumed.messages"]))
	require.NotContains(t, metrics, "gosqs.capacity.used")
	cancel()
	releaseOnce.Do(func() { close(release) })
	require.ErrorIs(t, wait(t, done), context.Canceled)
	metrics = collectMetrics(t, reader)
	assertBalanced(t, metrics)
	require.Equal(t, uint64(count), histogramCount(metrics["messaging.process.duration"]))
	require.Zero(t, sumMetric(metrics["gosqs.errors"]))
}
