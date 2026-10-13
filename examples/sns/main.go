package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/inaciogu/go-sqs/v2/internal/localdev"
)

func main() {
	raw := flag.Bool("raw", false, "enable SNS raw delivery and consume as SQS")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := localdev.Demo(ctx, true, *raw); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
