# Basic SQS consumer

```sh
docker compose up -d
go run ./examples/setup
go run ./examples/basic
```

In another terminal:

```sh
go run ./examples/publish -target sqs
```

Read `main.go`: the local SDK configuration, JSON handler, `ConsumerOptions`,
`Run`, and signal-driven shutdown are all visible. The handler validates an order
ID and returns nil to acknowledge it. Processing and acknowledgement errors are
reported through `OnError`; invalid orders remain eligible for redelivery.

The consumer waits for new messages until Ctrl+C. It neither publishes messages
nor removes the queue. Use `-queue` for an existing queue. After stopping the
consumer, `go run ./examples/setup -cleanup` removes playground resources.
See [the example index](../README.md) for endpoint overrides and cleanup.
