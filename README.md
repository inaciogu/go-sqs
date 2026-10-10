# go-sqs v2

A Go library for consuming Amazon SQS messages with bounded concurrency,
explicit payload decoding, and graceful shutdown. Requires Go 1.25 or newer.

```sh
go get github.com/inaciogu/go-sqs/v2
```

## Usage

Construct a consumer with queue configuration and a handler, then call `Run`.
Queue discovery and message acknowledgement are handled internally.

```go
package main

import (
    "context"
    "errors"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    gosqs "github.com/inaciogu/go-sqs/v2"
)

func main() {
    runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    initCtx, cancelInit := context.WithTimeout(context.Background(), 5*time.Second)
    consumer, err := gosqs.NewConsumer(initCtx, handleOrder, gosqs.ConsumerOptions{
        QueueName: "orders",
        MaxConcurrency: 10,
    })
    cancelInit()
    if err != nil {
        log.Print(err)
        return
    }
    if err := consumer.Run(runCtx); err != nil {
        // A shutdown timeout must still be reported even if it includes cancellation.
        if errors.Is(err, gosqs.ErrShutdownTimeout) || !errors.Is(err, context.Canceled) {
            log.Print(err)
        }
    }
}

func handleOrder(ctx context.Context, message *gosqs.Message) error {
    var order struct { ID string `json:"id"` }
    if err := message.Unmarshal(&order); err != nil {
        return err
    }
    // Perform idempotent work using ctx. The handler can run concurrently.
    return nil
}
```

The constructor context controls AWS configuration loading and is not stored.
`Run` accepts an independent execution context. You can also pass the same context
to both when their lifetimes match.

## Configuration

| Option | Default | Meaning |
| --- | --- | --- |
| `QueueName` / `QueuePrefix` | Required | Set exactly one. Prefix discovery happens once per Run. |
| `Region` | SDK configuration | An explicit value overrides environment/profile configuration. |
| `Endpoint` | AWS endpoint | Custom SQS base endpoint, such as LocalStack. |
| `Client` | SDK v2 SQS client | Injection skips AWS loading; cannot combine with Region or Endpoint. |
| `Logger` | JSON slog logger on stderr | An injected logger keeps its own filters and handler. |
| `LogLevel` | `slog.LevelInfo` | Filter for the default logger only. |
| `Telemetry` | `false` | Enable OTel metrics and the default OTel log bridge using application global providers. |
| `MaxNumberOfMessages` | 10 | Maximum received batch, between 1 and 10. |
| `MaxConcurrency` | 10 | Global capacity per consumer, including polling reservations and confirmations. |
| `VisibilityTimeout` | 30 seconds | Optional duration; whole seconds from zero to 12 hours. |
| `WaitTime` | 20 seconds | Optional duration; whole seconds from zero to 20 seconds. |
| `ShutdownTimeout` | 30 seconds | Optional nonnegative duration for draining work. |
| `BackoffMultiplier` | 2 | Finite multiplier of at least 1; zero selects the default. |
| `MessageFormat` | `MessageFormatSQS` | Preserve SQS payloads, or explicitly unwrap SNS. |
| `OnError` | None | Concurrent processing-error callback; must return promptly. |

Nil duration pointers select defaults. `gosqs.Duration(0)` sets an explicit zero.
Options are copied at construction; changing an original duration afterward does
not change the consumer. SDK credentials use the default AWS chain, including
shared profiles and IAM roles. Configure a region in the environment/profile or
provide `Region`; the library does not supply a default region.

```go
options := gosqs.ConsumerOptions{
    QueueName: "orders",
    WaitTime: gosqs.Duration(0),
    VisibilityTimeout: gosqs.Duration(2*time.Minute),
    ShutdownTimeout: gosqs.Duration(45*time.Second),
}
```

## Handler results and errors

| Handler result | Action |
| --- | --- |
| `nil` | Attempt to delete the message. |
| `ErrDrop`, including wrapped errors | Attempt to delete without retry. This does not send to a DLQ. |
| Any other error | Attempt to change visibility for a later delivery. |

Handler success does not guarantee that deletion succeeds. Make processing
idempotent: messages may be delivered again. Configure DLQ redrive policies on SQS.
Backoff uses the system `ApproximateReceiveCount`, is capped by the remaining
visibility budget, and does not rerun the handler locally.

Decode, handler, deletion, and visibility errors are logged and delivered to
`OnError` as `*gosqs.OperationError`. It exposes `Operation`, `QueueURL`, `MessageID`,
and the underlying `Err`, supporting `errors.Is` and `errors.As`. Operations are
`decode`, `handler`, `delete`, and `change_visibility`. These failures do not stop
the consumer. The callback shares the message's processing context, can run
concurrently, and is included in the shutdown deadline.

```go
options.OnError = func(ctx context.Context, err error) {
    var event *gosqs.OperationError
    if errors.As(err, &event) {
        // Record a metric or send to the application's error reporter.
        // Avoid blocking and protect shared state.
    }
}
```

Discovery and polling errors, after the SDK's own retries, stop `Run` and initiate
drainage. The library does not continuously restart failed polling. A prefix with
no matching queues returns `ErrNoQueues`.

## Shutdown and concurrency

Cancellation stops new polling and waits for active handlers and confirmations.
Processing contexts preserve values from `Run` but stay live during drainage;
handlers can finish their work and delete messages before the deadline.
At `ShutdownTimeout`, processing contexts are cancelled and `Run` returns an error
matching both `ErrShutdownTimeout` and the original stopping cause.

Go cannot forcibly terminate a handler or callback that ignores context. Such
work may outlive `Run`, and a new call returns `ErrAlreadyRunning` until all old
workers and pollers exit. Never copy a Consumer after use. Completed consumers can
be run again; discovery is repeated for each execution.

Capacity is reserved before each receive request, released for empty/partial
batches, and held through processing and acknowledgement. Prefix queues share
one capacity pool; long polls also occupy reservations. There is no per-queue
fairness guarantee. Cancellation interrupts requests and capacity waits.

Handlers must finish within the configured visibility timeout. Visibility is not
automatically renewed. The consumer does not guarantee FIFO processing order:
messages in a group may be handled concurrently. Use this library for workloads
that tolerate concurrent, idempotent processing.

## Prefixes and multiple consumers

```go
consumer, err := gosqs.NewConsumer(initCtx, handler, gosqs.ConsumerOptions{
    QueuePrefix: "orders-",
    MaxConcurrency: 20, // Shared across all matching queues, not 20 per queue.
})
```

For different queue handlers, construct separate consumers and use:

```go
err := gosqs.RunAll(runCtx, orderConsumer, paymentConsumer)
```

Each consumer has its own concurrency limit. An error cancels sibling polling;
`RunAll` waits for their drainage and aggregates shutdown failures with the
initiating error.

## SQS and SNS messages

By default, `Content` is the exact SQS body, even when JSON contains `Message`.
To consume SNS notification envelopes, set `MessageFormat: gosqs.MessageFormatSNS`.
SNS raw message delivery should use the default SQS format.

SNS mode requires `Type: Notification`, `TopicArn`, `MessageId`, and `Message`.
An empty message is valid. Invalid envelopes skip the handler and follow the
retry/error-reporting path. `NewMessage(*types.Message)` always preserves the raw
SQS payload and copies attributes; SNS parsing is internal to the consumer.

Metadata exposes the SQS `MessageID`, `ReceiptHandle`, `QueueURL`, `SystemAttributes`,
and typed `MessageAttributes`. `Attribute` preserves `DataType`, `StringValue`, and
`BinaryValue`. SNS binary attributes are decoded from Base64. SNS payload
attributes override same-named SQS custom attributes; system attributes remain
separate and cannot be overridden by custom attributes.

## Logging

Inject a `*slog.Logger` to use your application's logging pipeline. Default logging
uses Debug for polling/acknowledgement, Warn for handler failures, and Error for
operational failures. Logs identify operation, queue, and message ID, omitting
payload, receipt handle, and arbitrary error text. Original errors remain available
through `OnError` and returned errors. There are no panic/fatal log operations.

## OpenTelemetry

For a runnable OTLP/HTTP example, automated export assertions, and an optional
local Collector and Grafana dashboard, see [`examples/opentelemetry`](examples/opentelemetry/README.md).

Enable built-in metrics and logs with `Telemetry: true`:

```go
consumer, err := gosqs.NewConsumer(ctx, handler, gosqs.ConsumerOptions{
    QueueName: "orders",
    Telemetry: true,
})
```

The application must initialize and register global providers before constructing
consumers. Metrics use `otel.GetMeterProvider()`; the default log bridge uses
`log/global.GetLoggerProvider()`. Both use instrumentation scope
`github.com/inaciogu/go-sqs/v2`. Configuring only a trace provider does not enable
metrics or logs. Without configured providers, the OTel path is no-op.

With telemetry disabled, logging retains the existing JSON stderr default. With
telemetry enabled, the default logger forwards to OTel **instead of stderr**.
`LogLevel` filters that bridge. An injected `Logger` always takes precedence,
retains its own filters, and does not disable metrics. Logs retain their existing
structural fields and omit payloads, receipt handles, and arbitrary error text.

The following application helper registers providers using exporters selected by
the application (for example OTLP/HTTP exporters). It is not library configuration:

```go
import (
    "context"
    "errors"

    "go.opentelemetry.io/otel"
    logglobal "go.opentelemetry.io/otel/log/global"
    sdklog "go.opentelemetry.io/otel/sdk/log"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"
    "go.opentelemetry.io/otel/sdk/resource"
)

func initTelemetry(ctx context.Context, metrics sdkmetric.Exporter, logs sdklog.Exporter) (func(context.Context) error, error) {
    res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
    if err != nil {
        return nil, err
    }
    mp := sdkmetric.NewMeterProvider(
        sdkmetric.WithResource(res),
        sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)),
    )
    lp := sdklog.NewLoggerProvider(
        sdklog.WithResource(res),
        sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)),
    )
    otel.SetMeterProvider(mp)
    logglobal.SetLoggerProvider(lp)
    return func(ctx context.Context) error {
        return errors.Join(mp.Shutdown(ctx), lp.Shutdown(ctx))
    }, nil
}
```

The application owns SDKs, exporters, resources, credentials and lifecycle. Stop
and drain consumers first, then call the returned shutdown function with a fresh,
non-cancelled timeout context. Work that ignores cancellation may still outlive a
consumer shutdown timeout; join that work before shutting down providers to retain
its final telemetry. The library never replaces or shuts down providers.

For an application using OTLP/HTTP exporters, environment configuration can include:

```env
OTEL_SERVICE_NAME=orders-worker
OTEL_RESOURCE_ATTRIBUTES=service.version=1.0.0,deployment.environment.name=production
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
```

Environment variables do not initialize the Go SDK by themselves. Use the HTTP
exporters explicitly, or use `autoexport` with `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`,
`OTEL_METRICS_EXPORTER=otlp` and `OTEL_LOGS_EXPORTER=otlp` to choose exporters. Register
the providers in either case. The destination must accept both metrics and logs;
authentication headers depend on the platform.

### Metrics contract

Messaging names, units, attributes and histogram boundaries follow
[OpenTelemetry semantic conventions 1.44.0](https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/messaging/messaging-metrics.md).
These messaging conventions are in development; this library pins its contract
and will explicitly document future changes. It does not create spans or extract
trace context from messages in this release.

| Metric | Instrument / unit | Meaning |
| --- | --- | --- |
| `messaging.client.consumed.messages` | Counter / `{message}` | Messages in valid receive responses, once per delivery, including redeliveries. Empty batches contribute nothing. |
| `messaging.process.duration` | Histogram / `s` | Handler execution only; excludes decode, confirmation and `OnError`. |
| `messaging.client.operation.duration` | Histogram / `s` | ReceiveMessage, DeleteMessage and ChangeMessageVisibility calls, including SDK retries. |
| `gosqs.errors` | Counter / `{error}` | Each decode, handler, SQS, discovery or shutdown failure once. |
| `gosqs.workers.active` | UpDownCounter / `{worker}` | Workers including decode, handler, confirmation and `OnError`. |
| `gosqs.capacity.used` | UpDownCounter / `{slot}` | Capacity reserved by polls or workers; long polling also occupies slots. |
| `gosqs.shutdown.duration` | Histogram / `s` | Drain duration up to completion or timeout; `gosqs.shutdown.result=complete|timeout`. |

Messaging histogram boundaries in seconds are
`0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10`.
Histograms include counts; filter operation duration by `DeleteMessage` and absence
of `error.type` to count successful confirmations. Handler success is independent
of delete success. Changing visibility successfully does not confirm redelivery.

Attributes include `messaging.system=aws_sqs`, `messaging.operation.name` (AWS API
name or `process`) and `messaging.operation.type=receive|process|settle` on messaging
metrics. Handler duration also has `gosqs.handler.result=success|drop|error`;
wrapped `ErrDrop` is a drop, not an error. Decode failures skip the handler metric.

Consumer dimensions are `gosqs.consumer.queue_name` or
`gosqs.consumer.queue_prefix`. Queue operations include `server.address` when
available and, for QueueName consumers, `messaging.destination.name` extracted
from the QueueURL. Prefix consumers omit individual destination names to aggregate
queues under their selector. Consumers sharing a selector and provider aggregate
values. Message IDs, receipt handles, full queue URLs, payloads and error text are
never metric attributes. Service and environment identity belong to the
application resource.

`error.type` is present only for failures and uses these bounded classes:
`decode`, `handler`, `aws`, `invalid_response`, `visibility_budget`, `canceled`,
`deadline`, `shutdown_timeout`, and `discovery` (such as no matching queues).
Context and library sentinel errors take precedence over operation-specific
classes. `gosqs.errors` uses `gosqs.operation` with the existing operation names,
plus `discover` and `shutdown`. Normal polling/discovery cancellation is excluded
from that counter; interrupted client calls still record duration and error class.
Processing failures, including cancelled handler or confirmation operations, are
counted because they can leave a message unconfirmed.

Active workers and occupied slots decrease only when the work actually ends, even
if `Run` has already returned a shutdown timeout. Instrument creation errors fail
`NewConsumer`; export failures are handled by the application's OTel pipeline,
not by the consumer's `OnError` callback.

## Local development and validation

```sh
docker compose up
AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 go run ./your-app
```

Use `Region: "us-east-1"` and `Endpoint: "http://localhost:4566"` in the local
consumer configuration. The compose setup provisions SQS queues and SNS
subscriptions through Terraform; clients on the host use the published port.

```sh
go test -race ./...
go vet ./...
```

Tests use synchronized clients and a local HTTP server for SDK integration,
without requiring an AWS account. See [MIGRATING.md](MIGRATING.md) for v1 migration.
The library uses the MIT license.
