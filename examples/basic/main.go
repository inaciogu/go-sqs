package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/inaciogu/go-sqs/v2/internal/localdev"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := localdev.Demo(ctx, false, false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
