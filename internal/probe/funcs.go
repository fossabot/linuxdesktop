// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package probe

import (
	"context"
	"fmt"
	"time"
)

// ContextError preserves cancellation even when a transport deadline fires
// just before the context's timer publishes its error.
func ContextError(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("probe: context: %w", err)
	}

	deadline, bounded := ctx.Deadline()
	if bounded && !time.Now().Before(deadline) {
		return fmt.Errorf("probe: deadline: %w", context.DeadlineExceeded)
	}

	return nil
}

// Default supplies a fallback only when the caller supplied the zero value.
func Default[T comparable](value T, fallback func() T) T {
	var zero T

	if value == zero {
		return fallback()
	}

	return value
}
