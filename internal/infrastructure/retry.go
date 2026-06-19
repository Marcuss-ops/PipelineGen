// Package retry provides retry primitives with exponential backoff.
//
// Deprecated: use pkg/retry instead. This file delegates to the canonical
// implementation in pkg/retry/ so call sites can migrate incrementally.
package platform

import (
	"context"

	pkgretry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// RetryOptions configures retry behaviour.
// Deprecated: use pkg/retry.Options instead.
type RetryOptions = pkgretry.Options

// DefaultRetryOptions returns sensible defaults.
// Deprecated: use pkg/retry.DefaultOptions instead.
func DefaultRetryOptions() RetryOptions { return pkgretry.DefaultOptions() }

// Deprecated: use pkg/retry.Do.
func Do(ctx context.Context, fn func() error, opts RetryOptions) error {
	return pkgretry.Do(ctx, fn, opts)
}

// Deprecated: use pkg/retry.DoWithValue.
func DoWithValue[T any](ctx context.Context, fn func() (T, error), opts RetryOptions) (T, error) {
	return pkgretry.DoWithValue(ctx, fn, opts)
}
