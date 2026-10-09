# go-sqs v2

A Go library for consuming Amazon SQS messages with concurrent receiving,
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
        ReceiveWorkers: 2,
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
| `MaxNumberOfMessages` | 10 | Maximum received batch, between 1 and 10. |
| `ReceiveWorkers` | 1 | Concurrent receive workers per queue. Does not limit active message handlers. |
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

`ReceiveWorkers` starts independent pollers per queue; each requests up to
`MaxNumberOfMessages`. Pollers send deliveries to a shared, unbuffered channel.
A single dispatcher consumes the channel and starts a new goroutine for every
message. Each goroutine handles decode, the handler, error callbacks and
confirmation independently; it does not wait for other messages or batches.

There is no configured limit on active message goroutines. The channel
synchronizes handoff but does not bound accumulated processing or downstream
load. `ReceiveWorkers` limits concurrent polling requests only. Cancellation
interrupts receive requests and channel sends. Messages not handed off remain
in SQS and become available again after visibility expires. Accepted deliveries
are drained after the receivers and dispatcher stop. There is no per-queue
fairness guarantee. Waiting for handoff consumes the message visibility timeout.

Handlers must finish within the configured visibility timeout. Visibility is not
automatically renewed. The consumer does not guarantee FIFO processing order:
messages in a group may be handled concurrently. Use this library for workloads
that tolerate concurrent, idempotent processing.

## Prefixes and multiple consumers

```go
consumer, err := gosqs.NewConsumer(initCtx, handler, gosqs.ConsumerOptions{
    QueuePrefix: "orders-",
    ReceiveWorkers: 2, // Two independent receive workers per queue.
})
```

For different queue handlers, construct separate consumers and use:

```go
err := gosqs.RunAll(runCtx, orderConsumer, paymentConsumer)
```

Each consumer has its own receivers and dispatcher. An error cancels sibling polling;
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
