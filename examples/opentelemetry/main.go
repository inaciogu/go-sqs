// This example exercises real OTLP/HTTP export with a deterministic SQS client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
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
	duration := flag.Duration("duration", 0, "generate continuous traffic for this duration (e.g. 30m); zero sends three messages")
	flag.Parse()
	if *duration < 0 {
		fmt.Fprintln(os.Stderr, "duration must be nonnegative")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runFor(ctx, *duration); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Consumer drained; OTLP logs and metrics exported. Inspect the Collector output.")
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

func run(parent context.Context) (result error) {
	return runFor(parent, 0)
}

func runFor(parent context.Context, duration time.Duration) (result error) {
	continuous := duration > 0
	if !continuous {
		duration = 10 * time.Second
	}
	ctx, stop := context.WithTimeout(parent, duration)
	defer stop()
	shutdown, err := initTelemetry(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// The consumer has joined its workers before the providers are shut down.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result = errors.Join(result, shutdown(flushCtx))
	}()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := &demoClient{cancel: cancel, continuous: continuous}
	consumer, err := gosqs.NewConsumer(ctx, func(ctx context.Context, m *gosqs.Message) error {
		if continuous {
			// Vary processing duration so the real library histogram has a distribution.
			delay := map[string]time.Duration{"success": 50 * time.Millisecond, "drop": 150 * time.Millisecond, "retry": 300 * time.Millisecond}[m.Content]
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		switch m.Content {
		case "drop":
			return gosqs.ErrDrop
		case "retry":
			return errors.New("example handler failure")
		default:
			return nil
		}
	}, gosqs.ConsumerOptions{QueueName: "otel-demo", Client: client, Telemetry: true, LogLevel: slog.LevelDebug, MaxConcurrency: 1})
	if err != nil {
		return err
	}
	err = consumer.Run(runCtx)
	if continuous && !errors.Is(err, gosqs.ErrShutdownTimeout) && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		return nil
	}
	if ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	if errors.Is(err, gosqs.ErrShutdownTimeout) || !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// Only SQS is simulated. Each delivery is settled before cancelling Run.
type demoClient struct {
	gosqs.QueueClient
	mu            sync.Mutex
	next, settled int
	cancel        context.CancelFunc
	continuous    bool
}

func (*demoClient) GetQueueUrl(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	return &sqs.GetQueueUrlOutput{QueueUrl: aws.String("http://localhost/000000000000/otel-demo")}, nil
}
func (c *demoClient) ReceiveMessage(ctx context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if c.continuous {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	c.mu.Lock()
	if c.continuous || c.next < 3 {
		body := []string{"success", "drop", "retry"}[c.next%3]
		c.next++
		c.mu.Unlock()
		return &sqs.ReceiveMessageOutput{Messages: []types.Message{{Body: aws.String(body), MessageId: aws.String(body), ReceiptHandle: aws.String("example-receipt")}}}, nil
	}
	c.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *demoClient) settle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settled++
	if !c.continuous && c.settled == 3 {
		c.cancel()
	}
}
func (c *demoClient) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	c.settle()
	return &sqs.DeleteMessageOutput{}, nil
}
func (c *demoClient) ChangeMessageVisibility(context.Context, *sqs.ChangeMessageVisibilityInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	c.settle()
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}
