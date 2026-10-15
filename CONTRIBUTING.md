# Contributing

Install Go 1.25 or newer. Run the fast checks from the repository root:

```sh
go build ./...
go test -race ./...
go vet ./...
```

These checks do not need Docker, an AWS account, or AWS credentials. Examples
are part of the main module and compile against your local changes. The Go API
example in `example_test.go` is compiled but is not executed by `go test`.

## Local SQS and SNS

For local OTLP logs and metrics validation, see
[`examples/opentelemetry`](examples/opentelemetry/README.md). Its automated wire
tests run without Docker; the executable consumes real SQS traffic and an optional Collector receives telemetry.

Install Docker with Compose v2 and start the Docker daemon. The Compose service
uses MiniStack 1.5.22, pinned by its multi-platform digest for AMD64 and ARM64.
It enables only SQS/SNS and publishes port 4566 on localhost. There are no
volumes, nested containers, persistent state, sign-ups, or license keys.

```sh
docker compose up -d
go run ./examples/setup
go test -race -tags=integration -count=1 -timeout=2m ./...
go run ./examples/setup -cleanup
docker compose down
```

Manual examples use stable resources provisioned by `go run ./examples/setup`.
Consumers stay running until Ctrl+C; publish in another terminal with
`go run ./examples/publish -target sqs`, `-target sns`, or `-target otel`.
Stop consumers before explicit cleanup with `go run ./examples/setup -cleanup`.
See [the example index](examples/README.md) for complete workflows.
Integration tests continue creating unique temporary resources and removing only
their own resources automatically.

The local SDK clients use explicit `test/test` credentials and `us-east-1`, so
missing or invalid AWS profiles do not affect examples or integration tests.
No AWS CLI or `.env` file is needed. Integration resource names are unique, and
test cleanup uses an independent context. Manual resources have stable names and
are removed only by explicit cleanup. The suite never resets the emulator.

Setup and integration wait for successful `ListQueues` and `ListTopics` calls
for up to 30 seconds. SQS/OTel publishers require only SQS. Consumers discover
their existing queue directly, so provision it before starting. The integration
build tag is opt-in; if the endpoint is unavailable, tests fail with instructions
to start MiniStack instead of silently skipping.

## Endpoint overrides and troubleshooting

`SQS_TEST_ENDPOINT` configures the endpoint for both SQS and SNS:

```sh
SQS_TEST_ENDPOINT=http://localhost:14566 go run ./examples/sns
SQS_TEST_ENDPOINT=http://localhost:14566 go test -race -tags=integration -count=1 -timeout=2m ./...
```

If port 4566 is occupied, stop the conflicting service or change the Compose host
port to `127.0.0.1:14566:4566`, then use the override above. Leave the container
port at 4566. MiniStack returns queue URLs using the host reached by the client.
An endpoint override alone does not change the Compose port mapping. Use a local
test endpoint; the examples and tests create and delete resources there.

If startup or integration fails, check that Docker is running and inspect:

```sh
docker compose ps
docker compose logs --no-color --tail=200
```

After a forcibly interrupted process, its temporary resources may remain in
memory. Restarting the service clears all state; integration tests remove only
their own resources; manual resources remain until explicit cleanup. `docker compose down` removes the container and its network.

## Coverage and emulator limitations

The integration suite exercises the real AWS SDK and service calls: successful
processing, wrapped `ErrDrop`, visibility changes and redelivery, String/Number/
Binary SQS attributes, prefix discovery, cancellation, graceful drainage, and
SNS envelopes and raw delivery. It observes completed SDK settlement calls and
checks that acknowledged messages do not reappear after the visibility window.
Tests use bounded waits rather than assertions on exact scheduler timing.

MiniStack 1.5.22 parses SNS `BinaryValue` but builds envelope attribute `Value`
from `StringValue`, losing binary data. SNS-to-SQS integration covers String and
Number attributes. Binary SNS decoding remains covered by the existing unit
tests and an integration fixture containing a valid envelope sent directly to
SQS. This fixture does not validate binary publishing through SNS. Do not change
the consumer or convert Binary attributes to String to accommodate this gap.
See the [MiniStack SNS implementation](https://github.com/ministackorg/ministack/blob/v1.5.22/ministack/services/sns.py)
and [SQS timing limitations](https://ministack.org/docs/services/sqs).

IAM evaluation is disabled; these tests do not validate IAM policies, SNS
signatures, or CloudWatch metrics. The existing deterministic tests remain the
primary coverage for concurrency edge cases and artificial SDK failures.

CI keeps fast checks on the supported Go matrix and runs integration on Go 1.25
using the same Compose image. It prints service logs on failure and always tears
the environment down. When upgrading MiniStack, update both tag and digest,
verify AMD64/ARM64 availability, and rerun examples and integration twice.
