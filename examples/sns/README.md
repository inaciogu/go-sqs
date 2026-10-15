# SNS envelopes and raw delivery

```sh
docker compose up -d
go run ./examples/setup
go run ./examples/sns
```

In another terminal:

```sh
go run ./examples/publish -target sns
```

`main.go` makes the format selection explicit. The default consumer reads
`example-sns` with `MessageFormatSNS`, unwrapping the notification before JSON
decoding. `go run ./examples/sns -raw` reads `example-sns-raw` with
`MessageFormatSQS`, because raw delivery contains the original payload.

Setup subscribes both queues to the same topic. Every publication reaches both;
run both consumers in separate terminals or consume them one at a time.
The consumers continue until Ctrl+C and preserve resources. `-queue` overrides
the selected queue but must agree with the delivery format chosen by `-raw`.

MiniStack 1.5.22 loses Binary attributes in SNS envelopes. The integration suite
covers String/Number publication and tests binary decoding using a valid envelope
sent directly to SQS. No consumer workaround is applied.
See [the example index](../README.md) for configuration and explicit cleanup.
