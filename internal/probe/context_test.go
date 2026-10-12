package probe

import (
	"context"
	"errors"
	"testing"
	"time"
)

type deadlineContext struct {
	context.Context
	deadline time.Time
}

func (ctx deadlineContext) Deadline() (time.Time, bool) { return ctx.deadline, true }

func TestContextErrorPreservesCancellationAndTransportDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ContextError(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, expired := range []bool{false, true} {
		delta := time.Hour
		if expired {
			delta = -delta
		}
		ctx := deadlineContext{Context: context.Background(), deadline: time.Now().Add(delta)}
		err := ContextError(ctx)
		if expired && !errors.Is(err, context.DeadlineExceeded) || !expired && err != nil {
			t.Fatal(err)
		}
	}
	if err := ContextError(context.Background()); err != nil {
		t.Fatal(err)
	}
}
