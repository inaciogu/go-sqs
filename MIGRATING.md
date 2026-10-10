# Migrating to go-sqs v2

The library's v2 release is separate from AWS SDK v2. Update the module import:

```go
import gosqs "github.com/inaciogu/go-sqs/v2"
```

Go 1.25 remains the minimum. The module now ends in `/v2`; there are no compatibility
wrappers for the old API.

## Construction and execution

Use `NewConsumer(initCtx, handler, options)` followed by `Run(runCtx)`. No context is
stored on the consumer. Queue URL lookup is internal; remove calls to exported
`QueueURL`/`QueueURLs` helpers. `Run` drains in-flight work before returning, with a
30-second default deadline. Handle `ErrShutdownTimeout` even when the returned
error also matches `context.Canceled`.

The `Consumer` fields are private. Replace assignments to `Client`, `Logger`,
`ClientOptions`, or `Handler` with construction-time configuration. Supply custom
clients and loggers through `ConsumerOptions.Client` and `.Logger`.

## Option changes

| Previous API | v2 |
| --- | --- |
| `QueueName` with `PrefixBased: true` | `QueuePrefix`; leave QueueName empty. |
| Integer `VisibilityTimeout` seconds | `gosqs.Duration(n * time.Second)` |
| `WaitTimeSeconds` | `WaitTime: gosqs.Duration(n * time.Second)` |
| `LogLevel: "debug"` | `LogLevel: slog.LevelDebug` |
| Assigning `consumer.Client` | `Client` in ConsumerOptions |
| Assigning `consumer.Logger` | `Logger: *slog.Logger` in ConsumerOptions |
| Implicit `us-east-1` | Region resolved from SDK sources; configure one if absent. |
| Receive loop | `ReceiveWorkers`, default 1 per queue. Each message runs in its own goroutine without a processing limit. |
| Automatic SNS recognition | `MessageFormat: gosqs.MessageFormatSNS` |

`MaxNumberOfMessages` now uses `int`. Nil durations select defaults; explicit zero
is allowed through `gosqs.Duration(0)`. Client injection cannot be combined with
Region or Endpoint, because those settings would not configure the injected client.

## Upgrading from v2.0.0 to v2.1.0

This release remains on the `/v2` module path but intentionally includes the
following incompatible API and concurrency changes.

### Removal of MaxConcurrency

`ConsumerOptions.MaxConcurrency` and `DefaultMaxConcurrency` have been removed.
Remove references to these symbols when upgrading; existing references will fail
to compile. Configure `ReceiveWorkers` to set concurrent polling per queue (zero
selects the default of one), and `MaxNumberOfMessages` to set the receive batch
size. Neither option limits active message handlers: a shared dispatcher starts
one goroutine per accepted delivery. Applications must account for concurrent
load on their dependencies and implement idempotent handlers.

## AWS types and test doubles

Use `github.com/aws/aws-sdk-go-v2/service/sqs` for clients and operation types and
`github.com/aws/aws-sdk-go-v2/service/sqs/types` for messages and attributes.
`QueueClient` methods take context first and SDK options last:

```go
ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
```

SDK message slices, custom attribute maps, and system attribute maps use values
instead of pointers. `NewMessage` accepts `*types.Message` and copies the raw body;
it no longer unwraps SNS automatically.

## Metadata and handler outcomes

`Metadata.MessageId` becomes `Metadata.MessageID`. System attributes move out of
`Metadata.MessageAttributes` into `Metadata.SystemAttributes`. Custom attributes
now preserve their data type, optional text, and binary bytes:

```go
attempts := message.Metadata.SystemAttributes["ApproximateReceiveCount"]
attribute := message.Metadata.MessageAttributes["tenant"]
if attribute.StringValue != nil {
    tenant := *attribute.StringValue
    _ = tenant
}
_ = attempts
```

The handler still returns an error. `nil` and wrapped `ErrDrop` attempt deletion;
other errors request redelivery through visibility changes. Deletion/visibility
failures continue consumption and invoke optional `OnError` with `OperationError`.
`ErrDrop` does not forward a message to a DLQ. DLQ policies remain configured on SQS.

## Logging and limits

The old logger package and Zap dependency were removed. Use standard `log/slog`;
per-event severity is distinct from the filter, and the library never logs at a
panic/fatal level. Injected loggers retain their own filtering.

Processing contexts remain live while shutdown drains work, then are cancelled
at the deadline. Handlers and callbacks must cooperate with context; Run cannot
kill goroutines. Concurrent/restarted Run calls are rejected until prior work exits.

Visibility renewal, FIFO ordering, continuous queue discovery, automatic polling
recovery, and DLQ provisioning are not implemented. Set visibility long enough for
your handler, use idempotent processing, and supervise Run errors in the application.
