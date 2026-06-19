// Package corid exposes a context key and helpers for propagating a single
// correlation id (the same value the API layer exposes as the
// X-Request-ID header) from the HTTP request down through background
// jobs, the Ollama client, and the Python scripts we exec into.
//
// Storing it under one type-safe key means any layer that needs to log
// "the trace that produced this log line" can pull it out without
// having to know who set it.
package platform

import "context"

type key struct{}

// WithCorrelationID returns a new context carrying the given correlation id.
// An empty id is ignored, so callers do not need to guard for it.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, key{}, id)
}

// FromContext returns the correlation id stored in ctx, or "" if none was
// set. Safe to call from anywhere in the call chain.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(key{}).(string)
	return v
}
