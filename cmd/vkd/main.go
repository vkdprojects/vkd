// Command vkd is an AI-first scaffolding CLI that generates and updates projects.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/vkdprojects/vkd/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.New().Run(ctx, os.Args[1:]))
}
