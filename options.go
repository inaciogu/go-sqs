package gosqs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// ConsumerOptions configures an immutable consumer. Nil durations select defaults.
// Client and Logger are optional. OnError runs concurrently in message workers and
// must return promptly. It is included in the shutdown deadline.
type ConsumerOptions struct {
	QueueName   string
	QueuePrefix string
	Region      string
	Endpoint    string
	Client      QueueClient
	Logger      *slog.Logger
	LogLevel    slog.Level
	// Telemetry enables metrics and the default OTel log bridge using global providers.
	Telemetry           bool
	MaxNumberOfMessages int
	MaxConcurrency      int
	VisibilityTimeout   *time.Duration
	WaitTime            *time.Duration
	ShutdownTimeout     *time.Duration
	BackoffMultiplier   float64
	MessageFormat       MessageFormat
	OnError             func(context.Context, error)
}

const (
	DefaultMaxNumberOfMessages = 10
	DefaultMaxConcurrency      = 10
	DefaultVisibilityTimeout   = 30 * time.Second
	DefaultWaitTime            = 20 * time.Second
	DefaultShutdownTimeout     = 30 * time.Second
)

// Duration returns an optional duration, including an explicit zero.
func Duration(value time.Duration) *time.Duration { return &value }

type consumerConfig struct {
	queueName, queuePrefix                       string
	maxNumberOfMessages, maxConcurrency          int
	visibilityTimeout, waitTime, shutdownTimeout time.Duration
	backoffMultiplier                            float64
	messageFormat                                MessageFormat
	onError                                      func(context.Context, error)
}

func durationOr(value *time.Duration, fallback time.Duration) time.Duration {
	if value == nil {
		return fallback
	}
	return *value
}

// NewConsumer loads AWS configuration using ctx. Its context is not stored;
// Run receives the independent execution context. Client bypasses AWS loading.
func NewConsumer(ctx context.Context, handler MessageHandler, options ConsumerOptions) (*Consumer, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if handler == nil {
		return nil, errors.New("handler is required")
	}
	if (options.QueueName == "") == (options.QueuePrefix == "") {
		return nil, errors.New("exactly one of QueueName and QueuePrefix is required")
	}
	if options.MaxNumberOfMessages == 0 {
		options.MaxNumberOfMessages = DefaultMaxNumberOfMessages
	}
	if options.MaxConcurrency == 0 {
		options.MaxConcurrency = DefaultMaxConcurrency
	}
	if options.BackoffMultiplier == 0 {
		options.BackoffMultiplier = 2
	}
	cfg := consumerConfig{
		queueName: options.QueueName, queuePrefix: options.QueuePrefix,
		maxNumberOfMessages: options.MaxNumberOfMessages, maxConcurrency: options.MaxConcurrency,
		visibilityTimeout: durationOr(options.VisibilityTimeout, DefaultVisibilityTimeout),
		waitTime:          durationOr(options.WaitTime, DefaultWaitTime),
		shutdownTimeout:   durationOr(options.ShutdownTimeout, DefaultShutdownTimeout),
		backoffMultiplier: options.BackoffMultiplier, messageFormat: options.MessageFormat, onError: options.OnError,
	}
	if cfg.maxNumberOfMessages < 1 || cfg.maxNumberOfMessages > 10 {
		return nil, errors.New("MaxNumberOfMessages must be between 1 and 10")
	}
	if cfg.maxConcurrency < 1 {
		return nil, errors.New("MaxConcurrency must be positive")
	}
	for _, limit := range []struct {
		name       string
		value, max time.Duration
	}{
		{"VisibilityTimeout", cfg.visibilityTimeout, 12 * time.Hour}, {"WaitTime", cfg.waitTime, 20 * time.Second},
	} {
		if limit.value < 0 || limit.value > limit.max || limit.value%time.Second != 0 {
			return nil, fmt.Errorf("%s must be whole seconds between zero and %s", limit.name, limit.max)
		}
	}
	if cfg.shutdownTimeout < 0 {
		return nil, errors.New("ShutdownTimeout must be nonnegative")
	}
	if math.IsNaN(cfg.backoffMultiplier) || math.IsInf(cfg.backoffMultiplier, 0) || cfg.backoffMultiplier < 1 {
		return nil, errors.New("BackoffMultiplier must be finite and at least 1")
	}
	if cfg.messageFormat != MessageFormatSQS && cfg.messageFormat != MessageFormatSNS {
		return nil, errors.New("invalid MessageFormat")
	}
	client := options.Client
	if client != nil {
		if options.Region != "" || options.Endpoint != "" {
			return nil, errors.New("Client cannot be combined with Region or Endpoint")
		}
	} else {
		var loaders []func(*config.LoadOptions) error
		if options.Region != "" {
			loaders = append(loaders, config.WithRegion(options.Region))
		}
		awsConfig, err := config.LoadDefaultConfig(ctx, loaders...)
		if err != nil {
			return nil, fmt.Errorf("load AWS configuration: %w", err)
		}
		if awsConfig.Region == "" {
			return nil, errors.New("AWS region is required: configure Region, AWS_REGION, or a shared profile")
		}
		client = sqs.NewFromConfig(awsConfig, func(o *sqs.Options) {
			if options.Endpoint != "" {
				o.BaseEndpoint = aws.String(options.Endpoint)
			}
		})
	}
	var telemetry *consumerTelemetry
	if options.Telemetry {
		var err error
		telemetry, err = newConsumerTelemetry(cfg)
		if err != nil {
			return nil, fmt.Errorf("initialize telemetry: %w", err)
		}
	}
	log := options.Logger
	if log == nil {
		if options.Telemetry {
			log = telemetryLogger(options.LogLevel)
		} else {
			log = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: options.LogLevel}))
		}
	}
	return &Consumer{client: client, config: cfg, handler: handler, logger: log, telemetry: telemetry}, nil
}
