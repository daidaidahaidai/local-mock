package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/daidaidahaidai/local-mock/internal/interfaces/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(cli.CLI{Out: os.Stdout, Err: os.Stderr}.Run(ctx, os.Args[1:]))
}
