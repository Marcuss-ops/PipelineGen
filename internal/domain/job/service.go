package job

import (
	"context"
	"encoding/json"
	"fmt"
)

// Service is the canonical job-system contract presented to every
// consumer in PipelineGen. It is a Go interface — consumers declare
// their dependency as `job.Service` (interface value, not pointer-to-
// interface) and the composition root injects the concrete
// *application/jobs.Service, which satisfies this interface directly.
//
// Pre-June-2026 this package held a concrete struct facade with
// delegate function pointers. That facade has been eliminated in
// favour of a plain interface + compile-time assertion in the
// application layer (`var _ job.Service = (*appjobs.Service)(nil)`).
type Service interface {
	Enqueue(ctx context.Context, req *EnqueueRequest) (*Job, error)
	Get(ctx context.Context, id string) (*Job, error)
	Cancel(ctx context.Context, id string) error
	List(ctx context.Context, filter Filter) ([]Job, error)
	IsTerminal(status Status) bool
	RegisterHandler(jobType string, handler any) error
	ListEvents(ctx context.Context, jobID string) ([]Event, error)
}

// EnqueueRequest is the typed payload handed to Service.Enqueue.
//
// The fields map 1-1 to the Job columns written by the SQLite enqueue.
type EnqueueRequest struct {
	Type          string
	Payload       any
	CorrelationID string
	MaxRetries    int
	Priority      int
	Project       string
	ActiveKey     string
	VideoName     string
}

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
