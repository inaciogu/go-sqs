// Package localdev shares explicit local AWS clients between examples and integration tests.
// It is not used by the library's consumer implementation.
package localdev

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type Clients struct {
	Endpoint string
	SQS      *sqs.Client
	SNS      *sns.Client
}

func NewClients() *Clients {
	endpoint := cmp.Or(os.Getenv("SQS_TEST_ENDPOINT"), "http://localhost:4566")
	cfg := aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		HTTPClient:       &http.Client{Timeout: 25 * time.Second},
		RetryMaxAttempts: 1,
	}
	return &Clients{
		Endpoint: endpoint,
		SQS:      sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) }),
		SNS:      sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(endpoint) }),
	}
}

// WaitReady verifies both AWS APIs, not merely the emulator's health endpoint.
func (c *Clients) WaitReady(ctx context.Context) error {
	return c.waitReady(ctx, true)
}

// WaitSQS lets SQS-only examples run without requiring SNS.
func (c *Clients) WaitSQS(ctx context.Context) error {
	return c.waitReady(ctx, false)
}

func (c *Clients) waitReady(ctx context.Context, includeSNS bool) error {
	services := "SQS"
	if includeSNS {
		services = "SQS/SNS"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		probe, stop := context.WithTimeout(ctx, time.Second)
		_, last = c.SQS.ListQueues(probe, &sqs.ListQueuesInput{MaxResults: aws.Int32(1)})
		stop()
		if last == nil && includeSNS {
			probe, stop = context.WithTimeout(ctx, time.Second)
			_, last = c.SNS.ListTopics(probe, &sns.ListTopicsInput{})
			stop()
		}
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s unavailable at %s; run docker compose up -d: %w (last probe: %v)", services, c.Endpoint, ctx.Err(), last)
		case <-ticker.C:
		}
	}
}
