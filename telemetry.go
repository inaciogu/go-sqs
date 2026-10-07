package gosqs

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const instrumentationScope = "github.com/inaciogu/go-sqs/v2"

// Messaging metric semantics are pinned to semantic conventions 1.44.0.
var messagingBuckets = []float64{.005, .01, .025, .05, .075, .1, .25, .5, .75, 1, 2.5, 5, 7.5, 10}
var (
	errInvalidQueueURL  = errors.New("SQS returned an empty queue URL")
	errInvalidReceive   = errors.New("invalid SQS receive response")
	errVisibilityBudget = errors.New("visibility budget exhausted")
)

type consumerTelemetry struct {
	receivedMessages                                     metric.Int64Counter
	processDuration, operationDuration, shutdownDuration metric.Float64Histogram
	errors                                               metric.Int64Counter
	workers, capacity                                    metric.Int64UpDownCounter
	base                                                 []attribute.KeyValue
	prefix                                               bool
	baseOption                                           metric.MeasurementOption
	queues                                               sync.Map // Immutable attribute slices, shared by concurrent workers.
}

func newConsumerTelemetry(cfg consumerConfig) (*consumerTelemetry, error) {
	t := &consumerTelemetry{prefix: cfg.queuePrefix != "", base: []attribute.KeyValue{attribute.String("messaging.system", "aws_sqs")}}
	if t.prefix {
		t.base = append(t.base, attribute.String("gosqs.consumer.queue_prefix", cfg.queuePrefix))
	} else {
		t.base = append(t.base, attribute.String("gosqs.consumer.queue_name", cfg.queueName))
	}
	t.baseOption = metricAttrs(t.base)
	m := otel.Meter(instrumentationScope)
	var err error
	if t.receivedMessages, err = m.Int64Counter("messaging.client.consumed.messages", metric.WithUnit("{message}")); err != nil {
		return nil, err
	}
	if t.processDuration, err = m.Float64Histogram("messaging.process.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(messagingBuckets...)); err != nil {
		return nil, err
	}
	if t.operationDuration, err = m.Float64Histogram("messaging.client.operation.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(messagingBuckets...)); err != nil {
		return nil, err
	}
	if t.errors, err = m.Int64Counter("gosqs.errors", metric.WithUnit("{error}")); err != nil {
		return nil, err
	}
	if t.workers, err = m.Int64UpDownCounter("gosqs.workers.active", metric.WithUnit("{worker}")); err != nil {
		return nil, err
	}
	if t.capacity, err = m.Int64UpDownCounter("gosqs.capacity.used", metric.WithUnit("{slot}")); err != nil {
		return nil, err
	}
	if t.shutdownDuration, err = m.Float64Histogram("gosqs.shutdown.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *consumerTelemetry) start() time.Time {
	if t == nil {
		return time.Time{}
	}
	return time.Now()
}
func (t *consumerTelemetry) queue(rawURL string) []attribute.KeyValue {
	if t == nil {
		return nil
	}
	if value, ok := t.queues.Load(rawURL); ok {
		return value.([]attribute.KeyValue)
	}
	attrs := append([]attribute.KeyValue(nil), t.base...)
	if u, err := url.Parse(rawURL); err == nil {
		if host := u.Hostname(); host != "" {
			attrs = append(attrs, attribute.String("server.address", host))
		}
		if !t.prefix {
			if name := strings.TrimRight(u.Path, "/"); name != "" {
				name = name[strings.LastIndex(name, "/")+1:]
				attrs = append(attrs, attribute.String("messaging.destination.name", name))
			}
		}
	}
	value, _ := t.queues.LoadOrStore(rawURL, attrs)
	return value.([]attribute.KeyValue)
}
func metricAttrs(base []attribute.KeyValue, extra ...attribute.KeyValue) metric.MeasurementOption {
	attrs := make([]attribute.KeyValue, 0, len(base)+len(extra))
	attrs = append(attrs, base...)
	attrs = append(attrs, extra...)
	return metric.WithAttributeSet(attribute.NewSet(attrs...))
}
func errorClass(op string, err error) string {
	switch {
	case errors.Is(err, ErrShutdownTimeout):
		return "shutdown_timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, errInvalidReceive), errors.Is(err, errInvalidQueueURL):
		return "invalid_response"
	case errors.Is(err, errVisibilityBudget):
		return "visibility_budget"
	case op == "decode":
		return "decode"
	case op == "handler":
		return "handler"
	case op == "discover":
		return "discovery"
	default:
		return "aws"
	}
}
func (t *consumerTelemetry) received(ctx context.Context, q []attribute.KeyValue, n int64) {
	if t == nil || n == 0 {
		return
	}
	t.receivedMessages.Add(ctx, n, metricAttrs(q, attribute.String("messaging.operation.name", "ReceiveMessage"), attribute.String("messaging.operation.type", "receive")))
}
func (t *consumerTelemetry) process(ctx context.Context, q []attribute.KeyValue, start time.Time, err error) {
	if t == nil {
		return
	}
	result := "success"
	attrs := []attribute.KeyValue{attribute.String("messaging.operation.name", "process"), attribute.String("messaging.operation.type", "process")}
	if errors.Is(err, ErrDrop) {
		result = "drop"
	} else if err != nil {
		result = "error"
		attrs = append(attrs, attribute.String("error.type", errorClass("handler", err)))
	}
	attrs = append(attrs, attribute.String("gosqs.handler.result", result))
	t.processDuration.Record(ctx, time.Since(start).Seconds(), metricAttrs(q, attrs...))
}
func (t *consumerTelemetry) operation(ctx context.Context, q []attribute.KeyValue, name, kind string, start time.Time, err error) {
	if t == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("messaging.operation.name", name), attribute.String("messaging.operation.type", kind)}
	if err != nil {
		attrs = append(attrs, attribute.String("error.type", errorClass(name, err)))
	}
	t.operationDuration.Record(ctx, time.Since(start).Seconds(), metricAttrs(q, attrs...))
}
func (t *consumerTelemetry) failure(ctx context.Context, q []attribute.KeyValue, op string, err error, stopping bool) {
	if t == nil || err == nil {
		return
	}
	if stopping && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && !errors.Is(err, ErrShutdownTimeout) {
		return
	}
	if q == nil {
		q = t.base
	}
	t.errors.Add(ctx, 1, metricAttrs(q, attribute.String("gosqs.operation", op), attribute.String("error.type", errorClass(op, err))))
}
func (t *consumerTelemetry) workerChange(ctx context.Context, n int64) {
	if t != nil {
		t.workers.Add(ctx, n, t.baseOption)
	}
}
func (t *consumerTelemetry) capacityChange(ctx context.Context, n int64) {
	if t != nil && n != 0 {
		t.capacity.Add(ctx, n, t.baseOption)
	}
}
func (t *consumerTelemetry) shutdown(ctx context.Context, start time.Time, result string) {
	if t != nil {
		t.shutdownDuration.Record(ctx, time.Since(start).Seconds(), metricAttrs(t.base, attribute.String("gosqs.shutdown.result", result)))
	}
}

// levelHandler preserves LogLevel for the library-owned bridge, including derived handlers.
type levelHandler struct {
	handler slog.Handler
	level   slog.Level
}

func (h levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level && h.handler.Enabled(ctx, level)
}
func (h levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.handler.Handle(ctx, r)
}
func (h levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return levelHandler{h.handler.WithAttrs(attrs), h.level}
}
func (h levelHandler) WithGroup(name string) slog.Handler {
	return levelHandler{h.handler.WithGroup(name), h.level}
}
func telemetryLogger(level slog.Level) *slog.Logger {
	return slog.New(levelHandler{otelslog.NewHandler(instrumentationScope), level})
}
