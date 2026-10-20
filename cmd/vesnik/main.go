package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/nklmilojevic/vesnik/internal/cli"
	"github.com/nklmilojevic/vesnik/internal/release"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := cli.Execute(ctx, version, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "vesnik:", err)
		}
		os.Exit(exitCode(err))
	}
}

// Exit codes let scripts such as a just recipe decide whether falling back
// to another release path is safe.
const (
	exitFailed    = 1 // failed before changing anything; safe to fall back
	exitAborted   = 3 // the human aborted; do not fall back
	exitPublished = 4 // tags are pushed but a GitHub Release failed; do not fall back
)

func exitCode(err error) int {
	if _, ok := errors.AsType[*release.PublishError](err); ok {
		return exitPublished
	}
	if errors.Is(err, release.ErrAborted) || errors.Is(err, context.Canceled) {
		return exitAborted
	}
	return exitFailed
}
