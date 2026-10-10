package gosqs_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	gosqs "github.com/inaciogu/go-sqs/v2"
)

func ExampleNewConsumer() {
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	initCtx, cancelInit := context.WithTimeout(context.Background(), 5*time.Second)
	consumer, err := gosqs.NewConsumer(initCtx, func(ctx context.Context, message *gosqs.Message) error {
		var order struct {
			ID string `json:"id"`
		}
		return message.Unmarshal(&order)
	}, gosqs.ConsumerOptions{QueueName: "orders"})
	cancelInit()
	if err != nil {
		slog.Error("initialize consumer", "error", err)
		return
	}
	if err := consumer.Run(runCtx); err != nil && (errors.Is(err, gosqs.ErrShutdownTimeout) || !errors.Is(err, context.Canceled)) {
		slog.Error("run consumer", "error", err)
	}
}
