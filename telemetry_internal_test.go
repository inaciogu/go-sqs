package gosqs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type failingMeterProvider struct{ metric.MeterProvider }

func (failingMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return failingMeter{Meter: noop.NewMeterProvider().Meter("test")}
}

type failingMeter struct{ metric.Meter }

var errInstrument = errors.New("instrument creation failed")

func (failingMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errInstrument
}
func TestTelemetryConstructorError(t *testing.T) {
	old := otel.GetMeterProvider()
	otel.SetMeterProvider(failingMeterProvider{old})
	t.Cleanup(func() { otel.SetMeterProvider(old) })
	_, err := NewConsumer(t.Context(), func(context.Context, *Message) error { return nil }, ConsumerOptions{QueueName: "orders", Client: &visibilityClient{}, Telemetry: true})
	require.ErrorIs(t, err, errInstrument)
	// Disabled telemetry must never call the provider.
	_, err = NewConsumer(t.Context(), func(context.Context, *Message) error { return nil }, ConsumerOptions{QueueName: "orders", Client: &visibilityClient{}})
	require.NoError(t, err)
}
func TestTelemetryErrorClassesAndDerivedLogFilters(t *testing.T) {
	for _, tc := range []struct {
		op   string
		err  error
		want string
	}{
		{"decode", errors.New("secret"), "decode"}, {"handler", errors.New("secret"), "handler"}, {"receive", errInvalidReceive, "invalid_response"}, {"discover", errInvalidQueueURL, "invalid_response"}, {"discover", ErrNoQueues, "discovery"}, {"change_visibility", errVisibilityBudget, "visibility_budget"}, {"receive", context.Canceled, "canceled"}, {"handler", context.DeadlineExceeded, "deadline"}, {"shutdown", ErrShutdownTimeout, "shutdown_timeout"},
	} {
		require.Equal(t, tc.want, errorClass(tc.op, tc.err))
	}
	h := levelHandler{slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}), slog.LevelError}
	for _, derived := range []slog.Handler{h, h.WithAttrs([]slog.Attr{slog.String("a", "b")}), h.WithGroup("group")} {
		require.False(t, derived.Enabled(t.Context(), slog.LevelWarn))
		require.True(t, derived.Enabled(t.Context(), slog.LevelError))
	}
}

type benchmarkClient struct{ QueueClient }

func (benchmarkClient) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return &sqs.DeleteMessageOutput{}, nil
}
func BenchmarkTelemetryHandleMessage(b *testing.B) {
	for _, mode := range []string{"disabled", "noop", "memory"} {
		b.Run(mode, func(b *testing.B) {
			old := otel.GetMeterProvider()
			if mode == "memory" {
				p := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
				otel.SetMeterProvider(p)
				b.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			} else {
				otel.SetMeterProvider(noop.NewMeterProvider())
			}
			b.Cleanup(func() { otel.SetMeterProvider(old) })
			c, err := NewConsumer(context.Background(), func(context.Context, *Message) error { return nil }, ConsumerOptions{QueueName: "orders", Client: benchmarkClient{}, Telemetry: mode != "disabled", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				b.Fatal(err)
			}
			body, id, receipt := "{}", "id", "receipt"
			raw := &types.Message{Body: &body, MessageId: &id, ReceiptHandle: &receipt}
			ctx := context.Background()
			receivedAt := time.Now()
			// Warm attribute caching outside the measured loop.
			c.handleMessage(ctx, "https://example.com/orders", raw, receivedAt)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.handleMessage(ctx, "https://example.com/orders", raw, receivedAt)
			}
		})
	}
}

func TestTelemetryVisibilityBudgetExhausted(t *testing.T) {
	old := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	p := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(p)
	t.Cleanup(func() { otel.SetMeterProvider(old); require.NoError(t, p.Shutdown(context.Background())) })
	c, err := NewConsumer(t.Context(), func(context.Context, *Message) error { return nil }, ConsumerOptions{QueueName: "orders", Telemetry: true, Client: &visibilityClient{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	c.retryMessage(t.Context(), &Message{Metadata: MessageMetadata{QueueURL: "https://example.com/orders"}}, time.Now().Add(-12*time.Hour))
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	var errorsSeen int64
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "gosqs.errors" {
				for _, point := range m.Data.(metricdata.Sum[int64]).DataPoints {
					errorsSeen += point.Value
					class, _ := point.Attributes.Value("error.type")
					require.Equal(t, "visibility_budget", class.AsString())
				}
			}
			require.NotEqual(t, "messaging.client.operation.duration", m.Name)
		}
	}
	require.Equal(t, int64(1), errorsSeen)
}
