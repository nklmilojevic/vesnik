package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/release"
)

func TestExitCode(t *testing.T) {
	for err, want := range map[error]int{
		errors.New("claude is not on PATH"):                                   exitFailed,
		release.ErrAborted:                                                    exitAborted,
		fmt.Errorf("plan: %w", context.Canceled):                              exitAborted,
		&release.PublishError{Failed: 1, Total: 2}:                            exitPublished,
		fmt.Errorf("publish: %w", &release.PublishError{Failed: 1, Total: 1}): exitPublished,
	} {
		if got := exitCode(err); got != want {
			t.Errorf("exitCode(%v) = %d, want %d", err, got, want)
		}
	}
}
