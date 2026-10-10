# Local OpenTelemetry export

This example uses the library's actual telemetry, Go SDK providers, batch log
processor, periodic metric reader, and OTLP/HTTP protobuf exporters. Only the
SQS client is simulated: three deliveries produce success, drop, and handler
error outcomes. No Docker, AWS account, credentials, or SQS emulator is needed
for the automated tests. The example creates no spans.

## Automated wire validation

From the repository root:

```sh
rtk go test -race -count=1 -v ./examples/opentelemetry
```

Tests run the same example against an HTTP server on a random loopback port,
decode the actual OTLP protobuf requests, and assert:

- Logs and metrics reach their expected HTTP endpoints as protobuf POSTs.
- `OTEL_SERVICE_NAME` takes precedence over `service.name` in
  `OTEL_RESOURCE_ATTRIBUTES`; resource attributes appear on both signals.
- `OTEL_EXPORTER_OTLP_ENDPOINT` supplies `/v1/logs` and `/v1/metrics`.
- Signal-specific `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` and
  `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` override the common endpoint and retain
  their complete custom paths.
- Common and signal-specific `OTEL_EXPORTER_OTLP_*HEADERS` are sent with the
  corresponding requests; `OTEL_EXPORTER_OTLP_COMPRESSION=gzip` compresses both.
- Exported counts are three received messages, one handler error, one each of
  success/drop/error processing outcomes, and zero active workers after drainage.
  Histograms retain their boundaries and queue attributes.
- Library logs contain the handler warning, severity, timestamp, and scope,
  without exporting receipt handles or arbitrary error text.
- Pending metrics and batched logs are exported at shutdown, using a fresh
  context after the consumer's execution context has been cancelled.
- An HTTP 400 export rejection causes the example to return an error.

The tests clear relevant inherited OTel settings and use serial global-provider
tests. They belong to the example's application configuration, while the root
telemetry tests cover the library's instrumentation contract. They run under
the ordinary `go test ./...` checks, without adding a Docker dependency to CI.

## Inspect with a real Collector

For local charts and searchable logs, see the optional
[`grafana/`](grafana/README.md) setup. It uses this same consumer with
`-duration=30m` to produce continuous traffic and real library telemetry.

From the repository root:

```sh
rtk docker compose -p go-sqs-otel-example -f examples/opentelemetry/compose.yaml up -d
rtk proxy env OTEL_SERVICE_NAME=go-sqs-otel-example OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local,service.version=example OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 rtk go run ./examples/opentelemetry
rtk docker compose -p go-sqs-otel-example -f examples/opentelemetry/compose.yaml logs --no-color collector
rtk docker compose -p go-sqs-otel-example -f examples/opentelemetry/compose.yaml down
```

Wait for the Collector to report that it is ready before running the example.
Its `debug` exporter prints detailed resource, scope, log, and metric data.
Look for `service.name=go-sqs-otel-example`, the `github.com/inaciogu/go-sqs/v2`
scope, a handler warning, `messaging.client.consumed.messages` with value 3,
and `gosqs.errors` with value 1. The simulated handler failure is intentional.
Successful completion means the receiver accepted both exports; the automated
test provides assertions about their contents.

The Collector binds host port 4318 to localhost. If occupied, change the host
port in `compose.yaml` and the endpoint together. This separate Compose project
does not start or stop the repository's SQS/SNS emulator. No persistent volumes
or cloud destination are configured.

This example explicitly selects HTTP/protobuf exporters. It does not use
`OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_LOGS_EXPORTER`, or `OTEL_METRICS_EXPORTER`
to select exporters. Endpoint, headers, compression, and resource variables
are read by the actual SDK/exporters, rather than reimplemented in the example.
TLS credentials, every supported environment variable, periodic scheduling,
network retry behavior, and downstream storage/querying are outside this
validation's coverage. The Collector's console output is evidence of local
reception, not of delivery to a cloud backend.

See the official [Go exporters guide](https://opentelemetry.io/docs/languages/go/exporters/)
and [Collector troubleshooting guide](https://opentelemetry.io/docs/collector/troubleshooting/).
