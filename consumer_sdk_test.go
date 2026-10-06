package gosqs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gosqs "github.com/inaciogu/go-sqs/v2"
	"github.com/stretchr/testify/require"
)

func TestRunCancelsSDKPolling(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		switch r.Header.Get("X-Amz-Target") {
		case "AmazonSQS.GetQueueUrl":
			_, _ = w.Write([]byte(`{"QueueUrl":"https://sqs.us-east-1.amazonaws.com/123456789012/test"}`))
		case "AmazonSQS.ReceiveMessage":
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		default:
			t.Errorf("unexpected SQS operation: %s", r.Header.Get("X-Amz-Target"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	defer close(release)

	initCtx, cancelInit := context.WithCancel(t.Context())
	defer cancelInit()
	consumer, err := gosqs.NewConsumer(initCtx, func(context.Context, *gosqs.Message) error {
		t.Error("handler called without receiving a message")
		return nil
	}, gosqs.ConsumerOptions{QueueName: "test", Region: "us-east-1", Endpoint: server.URL, Logger: quietLogger()})
	require.NoError(t, err)
	cancelInit() // Initialization lifetime must not affect Run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()

	select {
	case <-started:
	case err := <-done:
		t.Fatalf("consumer stopped before polling: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("SDK did not reach the custom endpoint")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not interrupt SDK polling")
	}
}

func TestNewConsumerReturnsConfigurationError(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(configFile, []byte("[default]\nregion = us-east-1\n"), 0600))
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", configFile)
	t.Setenv("AWS_PROFILE", "missing-profile")
	consumer, err := gosqs.NewConsumer(context.Background(), func(context.Context, *gosqs.Message) error {
		return nil
	}, gosqs.ConsumerOptions{QueueName: "test"})
	require.ErrorContains(t, err, "load AWS configuration")
	require.Nil(t, consumer)
}

func TestAWSRegionResolution(t *testing.T) {
	for _, tc := range []struct{ name, explicit, env, profile, want string }{
		{name: "explicit overrides sources", explicit: "eu-west-1", env: "us-east-2", profile: "ap-south-1", want: "eu-west-1"},
		{name: "environment", env: "us-east-2", profile: "ap-south-1", want: "us-east-2"},
		{name: "profile", profile: "ap-south-1", want: "ap-south-1"},
		{name: "missing region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_REGION", tc.env)
			t.Setenv("AWS_DEFAULT_REGION", "")
			t.Setenv("AWS_PROFILE", "default")
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			t.Setenv("AWS_SESSION_TOKEN", "")
			profile := filepath.Join(t.TempDir(), "config")
			data := "[default]\n"
			if tc.profile != "" {
				data += "region = " + tc.profile + "\n"
			}
			require.NoError(t, os.WriteFile(profile, []byte(data), 0600))
			t.Setenv("AWS_CONFIG_FILE", profile)
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", profile)
			signedRegion := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				signedRegion <- r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/x-amz-json-1.0")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"__type":"QueueDoesNotExist","message":"not found"}`))
			}))
			defer server.Close()
			c, err := gosqs.NewConsumer(t.Context(), func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{QueueName: "test", Region: tc.explicit, Endpoint: server.URL, Logger: quietLogger()})
			if tc.want == "" {
				require.ErrorContains(t, err, "AWS region is required")
				return
			}
			require.NoError(t, err)
			require.Error(t, c.Run(t.Context()))
			require.True(t, strings.Contains(wait(t, signedRegion), "/"+tc.want+"/sqs/"))
		})
	}
}

func TestInjectedClientBypassesAWSConfiguration(t *testing.T) {
	t.Setenv("AWS_PROFILE", "profile-that-does-not-exist")
	c, err := gosqs.NewConsumer(t.Context(), func(context.Context, *gosqs.Message) error { return nil }, gosqs.ConsumerOptions{QueueName: "test", Client: &fakeClient{}, Logger: quietLogger()})
	require.NoError(t, err)
	require.NotNil(t, c)
}
