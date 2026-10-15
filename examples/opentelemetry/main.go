// This example consumes real SQS messages and exports OTLP/HTTP logs and metrics.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	gosqs "github.com/inaciogu/go-sqs/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	logglobal "go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

func main() {
	duration := flag.Duration("duration", 0, "stop after this duration; zero runs until Ctrl+C")
	queue := flag.String("queue", "example-otel", "queue provisioned by examples/setup")
	flag.Parse()
	if *duration < 0 {
		fmt.Fprintln(os.Stderr, "duration must be nonnegative")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	endpoint := cmp.Or(os.Getenv("SQS_TEST_ENDPOINT"), "http://localhost:4566")
	client := sqs.NewFromConfig(aws.Config{
		Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
	if err := runConsumer(ctx, client, *queue, *duration); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Consumer drained; OTLP logs and metrics exported.")
}

// No endpoint/header options: the exporters read their standard OTEL_* variables.
func initTelemetry(ctx context.Context) (func(context.Context) error, error) {
	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, err
	}
	metrics, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	logs, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, errors.Join(err, metrics.Shutdown(ctx))
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)))
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)))
	oldMeter, oldLogger := otel.GetMeterProvider(), logglobal.GetLoggerProvider()
	otel.SetMeterProvider(mp)
	logglobal.SetLoggerProvider(lp)
	return func(ctx context.Context) error {
		defer otel.SetMeterProvider(oldMeter)
		defer logglobal.SetLoggerProvider(oldLogger)
		return errors.Join(mp.Shutdown(ctx), lp.Shutdown(ctx))
	}, nil
}

// Application providers must outlive consumer drainage and acknowledgement.
func runConsumer(parent context.Context, client gosqs.QueueClient, queue string, duration time.Duration) (result error) {
	ctx := parent
	if duration > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(parent, duration)
		defer stop()
	}
	initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	shutdown, err := initTelemetry(initCtx)
	if err != nil {
		cancel()
		return err
	}
	defer func() {
		flushCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		result = errors.Join(result, shutdown(flushCtx))
	}()
	consumer, err := gosqs.NewConsumer(initCtx, handleOrder, gosqs.ConsumerOptions{
		QueueName: queue, Client: client, Telemetry: true, LogLevel: slog.LevelDebug,
		WaitTime: gosqs.Duration(time.Second), ShutdownTimeout: gosqs.Duration(5 * time.Second),
		VisibilityTimeout: gosqs.Duration(5 * time.Second),
		OnError:           func(ctx context.Context, err error) { fmt.Fprintln(os.Stderr, err) },
	})
	cancel()
	if err != nil {
		return err
	}
	fmt.Printf("Consuming %s; publish with go run ./examples/publish -target otel. Ctrl+C drains and flushes.\n", queue)
	err = consumer.Run(ctx)
	if errors.Is(err, gosqs.ErrShutdownTimeout) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if duration > 0 && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func handleOrder(ctx context.Context, message *gosqs.Message) error {
	var order struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := message.Unmarshal(&order); err != nil {
		return err
	}
	if order.ID == "" {
		return errors.New("order id is required")
	}
	// These optional actions make settlement and real redelivery visible.
	delay := 50 * time.Millisecond
	switch order.Action {
	case "drop":
		delay = 150 * time.Millisecond
	case "retry":
		delay = 300 * time.Millisecond
	case "", "success":
	default:
		return errors.New("unknown action")
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	switch order.Action {
	case "drop":
		return gosqs.ErrDrop
	case "retry":
		if message.Metadata.SystemAttributes["ApproximateReceiveCount"] == "1" {
			return errors.New("example handler failure")
		}
	}
	return nil
}
