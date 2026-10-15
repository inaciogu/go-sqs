# OpenTelemetry with real SQS traffic

The executable uses the AWS SQS SDK against MiniStack and the library's actual
logs and metrics. `main.go` shows local client configuration, global provider
registration, OTLP/HTTP exporters, the handler, `Telemetry: true`, and shutdown.
There are no traces. A separate publisher supplies traffic; the consumer stays
running until Ctrl+C or an optional `-duration` deadline.

## Run with the Collector

From the repository root:

```sh
docker compose up -d
go run ./examples/setup
docker compose -p go-sqs-otel-example -f examples/opentelemetry/compose.yaml up -d
```

Wait for the Collector to report readiness, then start the consumer:

```sh
env OTEL_SERVICE_NAME=go-sqs-otel-example OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local,service.version=example OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_METRIC_EXPORT_INTERVAL=2000 OTEL_BLRP_SCHEDULE_DELAY=1000 go run ./examples/opentelemetry
```

In another terminal:

```sh
go run ./examples/publish -target otel -action cycle
docker compose -p go-sqs-otel-example -f examples/opentelemetry/compose.yaml logs --no-color collector
```

The three published JSON orders carry `action=success`, `drop`, and `retry`.
The handler varies duration and returns nil, `ErrDrop`, or an intentional error.
The retry order fails on its first SQS delivery, then succeeds on redelivery;
`ApproximateReceiveCount` controls that decision. Thus three published messages
normally produce four receive/handler executions and one handler error. This is
actual service redelivery, not an in-process handler retry.

The Collector's debug exporter prints scope, resource, logs, and metrics.
Look for scope `github.com/inaciogu/go-sqs/v2` and the configured service name.
Logs printed in the terminal by `OnError` are separate from the library's OTel
log pipeline. Pending logs and metrics flush with a fresh five-second context
only after the consumer drains. No resources are deleted on shutdown.

For continuous traffic, run:

```sh
go run ./examples/publish -target otel -action cycle -count 0 -interval 500ms
```

`-duration=30m` limits how long the consumer runs; it no longer generates traffic.
`-queue` selects an existing queue, defaulting to `example-otel`. Use its own
queue to avoid competing with the basic consumer. For charts and searchable
logs, see the optional [Grafana stack](grafana/README.md).

## Automated export validation (without Docker)

```sh
go test -race -count=1 -v ./examples/opentelemetry
```

Tests inject a deterministic SQS client defined only in `client_test.go` while
using the same application handler, providers, and exporters. An HTTP receiver
on a random loopback port decodes actual OTLP protobuf requests and checks
resource identity, common/signal-specific endpoints, headers, gzip compression,
metric counts and boundaries, library logs, shutdown flush, and export rejection.
The fixture supplies three deliveries with success/drop/error outcomes; it does
not simulate redelivery. Its three-delivery assertions differ deliberately from
the executable's real retry scenario. These tests run in ordinary `go test ./...`.

The SDK/exporters read `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, and
standard endpoint/header/compression variables. This example explicitly chooses
HTTP/protobuf exporters; `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_LOGS_EXPORTER`, and
`OTEL_METRICS_EXPORTER` do not select exporters here. TLS, cloud delivery, every
supported SDK variable, and backend storage are outside the wire tests.

## Stop and clean up

Stop the publisher and consumer with Ctrl+C before:

```sh
docker compose -p go-sqs-otel-example -f examples/opentelemetry/compose.yaml down
go run ./examples/setup -cleanup
docker compose down
```

The Collector uses localhost port 4318 and is separate from MiniStack (4566).
It cannot run alongside the Grafana recipe on that same host port. For conflicts,
change the host mapping and corresponding endpoint. `SQS_TEST_ENDPOINT` controls
the SQS endpoint independently of the OTLP endpoint.

See [all examples](../README.md), the official
[Go exporters guide](https://opentelemetry.io/docs/languages/go/exporters/), and
[Collector troubleshooting](https://opentelemetry.io/docs/collector/troubleshooting/).
