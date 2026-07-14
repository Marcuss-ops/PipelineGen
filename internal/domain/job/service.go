// Package job — Service + EnqueueRequest type aliases (Phase A.2).
//
// Production definitions of Service (the canonical job-management
// interface) and EnqueueRequest live in internal/kernel/job/. This
// file re-exports them as type aliases for back-compat with 107
// import sites in the codebase. Go's type aliases resolve
// transparently: `job.Service` and `job.Service` are the same
// type as far as the compiler and runtime are concerned.
//
// EnqueueTyped[T] (the generic helper for typed-payload enqueue)
// stays in domain/job: it bridges the kernel/service interface with
// stdlib marshaling and is consumed by api/handlers without taking
// on kernel-cross-zone imports of its own.
package job

import (
	"context"
	"encoding/json"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2) ──────────

type (
	// Service is the canonical job-system contract (see kernel/job.Service).
	Service = job.Service
	// EnqueueRequest is the typed payload handed to Service.Enqueue.
	EnqueueRequest = job.EnqueueRequest
)

// EnqueueTyped is a deterministic, type-safe alternative to Enqueue.
//
// Equivalent to calling svc.Enqueue with `req.Payload = json.Marshal(payload)`.
// The single marshal pass produces stable key ordering (Go structs follow
// declaration order; map iteration is randomized). This eliminates the
// brittle `json.Marshal → json.Unmarshal-to-map` round-trip some callers
// historically inserted to coerce a typed payload into the any-typed
// Payload slot.
//
// Wire-format guarantee: the JSON content (parsed key/value pairs) written
// to the `payload_json` column is identical to what Enqueue(req, Payload:
// payloadStruct) would have stored. The byte-level key ordering is now
// deterministic (struct field declaration order) instead of randomized
// (the marshaled-map path).
//
// Behavior:
//   - Marshals payload exactly once; the wrapped EnqueueFn observes
//     `json.RawMessage` (verbatim pass-through; zero allocation).
//   - If req.Payload is non-nil on entry, the caller's value is
//     OVERWRITTEN by the marshaled typed payload. Construct req with
//     only metadata fields set and pass the typed payload separately.
//   - Oversized payloads (>1 MiB) defer to the application-layer Enqueue's
//     MaxPayloadSize check; no domain-layer pre-check is duplicated here
//     to avoid the constant-drift footgun.
//
// Note: implemented as a TOP-LEVEL generic function rather than a method
// because Go forbids type parameters on methods (only on functions
// and types). The service is the first explicit argument.
func EnqueueTyped[T any](ctx context.Context, svc Service, req *EnqueueRequest, payload T) (*Job, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("job.EnqueueTyped (type=%s, payload=%T): marshal: %w", req.Type, payload, err)
	}
	// json.RawMessage is a Marshaler: the downstream application/jobs/Service.Enqueue
	// sees a verbatim pass-through. The MaxPayloadSize limit is enforced there
	// (single canonical constant).
	req.Payload = json.RawMessage(raw)
	return svc.Enqueue(ctx, req)
}
