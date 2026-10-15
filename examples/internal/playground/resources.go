// Package playground defines resources provisioned for the manual examples.
package playground

// Stable resource names belong to the manual playground, not integration tests.
const (
	BasicQueue     = "example-orders"
	SNSQueue       = "example-sns"
	SNSRawQueue    = "example-sns-raw"
	TelemetryQueue = "example-otel"
	TopicName      = "example-events"
)
