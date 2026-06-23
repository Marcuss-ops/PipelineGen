package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Service is the canonical job-system facade presented to every
// consumer in PipelineGen. It is intentionally a CONCRETE struct
// (not an interface) wrapping delegate function pointers so that
// `*job.Service` (pointer-to-struct) is a valid field type in any
// consumer struct. This avoids Go's "type *job.Service is pointer to
// interface, not interface" gotcha that fires on `s.jobsSvc.Enqueue(...)`
// when the field is declared as `*Interface`.
//
// Pre-66c646b5, every consumer (the scheduler, the scriptflow
// generator, the clip enrichers, the script handlers) declared the
// job-service interface as `*job.Service` and called methods through
// the pointer. With an interface in that position, Go refuses to
// dispatch the call. Wrapping the interface in a concrete struct with
// thin method shims restores Go's "auto-deref pointer" behaviour
// without requiring consumers to be edited.
//
// Late binding: the orchestrator's bootstrap calls SetInner once the
// real implementation is available; until then every method returns
// ErrNotWired so mis-wiring is loud during development.
type Service struct {
	EnqueueFn         func(ctx context.Context, req *EnqueueRequest) (*Job, error)
	GetFn             func(ctx context.Context, id string) (*Job, error)
	CancelFn          func(ctx context.Context, id string) error
	ListFn            func(ctx context.Context, filter Filter) ([]*Job, error)
	IsTerminalFn      func(status Status) bool
	RegisterHandlerFn func(jobType string, handler any) error
	ListEventsFn      func(ctx context.Context, jobID string) ([]Event, error)
}

// ErrNotWired is the typed sentinel returned by Service methods when
// the orchestrator has not yet injected a real implementation via SetInner.
// Callers can errors.Is(err, job.ErrNotWired) to detect the mis-wiring
// during development.
var ErrNotWired = errors.New("job.Service: concrete implementation not wired")

// NewUnwiredService returns a Service with all delegate functions nil;
// every method returns ErrNotWired until SetInner is called.
func NewUnwiredService() *Service {
	return &Service{}
}

// NewService wires the delegate functions. The orchestrator's
// internal/jobs.Service fulfils every signature.
func NewService(
	enqueue func(ctx context.Context, req *EnqueueRequest) (*Job, error),
	get func(ctx context.Context, id string) (*Job, error),
	cancel func(ctx context.Context, id string) error,
	list func(ctx context.Context, filter Filter) ([]*Job, error),
	isTerminal func(status Status) bool,
) *Service {
	return &Service{
		EnqueueFn:    enqueue,
		GetFn:        get,
		CancelFn:     cancel,
		ListFn:       list,
		IsTerminalFn: isTerminal,
	}
}

// SetInner replaces every delegate function on the Service. Used by the
// orchestrator's bootstrap when the real worker pool comes online.
// Returns the receiver so calls can be chained.
func (s *Service) SetInner(c InnerService) *Service {
	if s == nil {
		s = NewUnwiredService()
	}
	if c == nil {
		return s
	}
	s.EnqueueFn = c.Enqueue
	s.GetFn = c.Get
	s.CancelFn = c.Cancel
	s.ListFn = c.List
	s.IsTerminalFn = c.IsTerminal
	return s
}

// InnerService is the interface that real worker-pool implementations
// satisfy. It is intentionally INTERNAL to package job — public consumers
// use the concrete *Service struct above to avoid the pointer-to-interface
// issue.
type InnerService interface {
	Enqueue(ctx context.Context, req *EnqueueRequest) (*Job, error)
	Get(ctx context.Context, id string) (*Job, error)
	Cancel(ctx context.Context, id string) error
	List(ctx context.Context, filter Filter) ([]*Job, error)
	IsTerminal(status Status) bool
}

// Enqueue, Get, Cancel, List, IsTerminal — public method shims that
// auto-derive the ErrNotWired sentinel when the orchestrator has not
// yet wired a real implementation.

func (s *Service) Enqueue(ctx context.Context, req *EnqueueRequest) (*Job, error) {
	if s == nil || s.EnqueueFn == nil {
		return nil, fmt.Errorf("%w (type=%s)", ErrNotWired, req.Type)
	}
	return s.EnqueueFn(ctx, req)
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
// (the marshaled-map path). Content-hashed caches and snapshot tests that
// previously broke under the map-as-payload path now produce stable bytes
// across restarts.
//
// Behavior:
//   - Returns ErrNotWired (wrapped) if svc is nil or its EnqueueFn is unset.
//     Wrapping uses %w so errors.Is(err, ErrNotWired) propagates correctly.
//   - Marshals payload exactly once; the wrapped EnqueueFn observes
//     `json.RawMessage` (verbatim pass-through; zero allocation).
//   - If req.Payload is non-nil on entry, the caller's value is
//     OVERWRITTEN by the marshaled typed payload. Construct req with
//     only metadata fields set and pass the typed payload separately.
//   - Oversized payloads (>1 MiB) defer to the application-layer Enqueue's
//     MaxPayloadSize check; no domain-layer pre-check is duplicated here
//     to avoid the constant-drift footgun.
//
// Backwards-compatible: Service.Enqueue is unaffected. Existing callers that
// build *EnqueueRequest with a Payload field continue to work.
//
// Note: implemented as a TOP-LEVEL generic function rather than a *Service
// method because Go forbids type parameters on methods (only on functions
// and types). The receiver (svc *Service) is the first explicit argument.
func EnqueueTyped[T any](ctx context.Context, svc *Service, req *EnqueueRequest, payload T) (*Job, error) {
	if svc == nil {
		return nil, fmt.Errorf("%w (EnqueueTyped type=%s, payload=%T)", ErrNotWired, req.Type, payload)
	}
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

func (s *Service) Get(ctx context.Context, id string) (*Job, error) {
	if s == nil || s.GetFn == nil {
		return nil, fmt.Errorf("%w (id=%s)", ErrNotWired, id)
	}
	return s.GetFn(ctx, id)
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	if s == nil || s.CancelFn == nil {
		return fmt.Errorf("%w (cancel id=%s)", ErrNotWired, id)
	}
	return s.CancelFn(ctx, id)
}

func (s *Service) List(ctx context.Context, filter Filter) ([]*Job, error) {
	if s == nil || s.ListFn == nil {
		return nil, fmt.Errorf("%w", ErrNotWired)
	}
	return s.ListFn(ctx, filter)
}

func (s *Service) IsTerminal(status Status) bool {
	if s == nil || s.IsTerminalFn == nil {
		return status.IsTerminal()
	}
	return s.IsTerminalFn(status)
}

// SetRegisterHandler wires the handler registration delegate.
func (s *Service) SetRegisterHandler(fn func(jobType string, handler any) error) *Service {
	if s == nil {
		s = NewUnwiredService()
	}
	s.RegisterHandlerFn = fn
	return s
}

// SetListEvents wires the event listing delegate.
func (s *Service) SetListEvents(fn func(ctx context.Context, jobID string) ([]Event, error)) *Service {
	if s == nil {
		s = NewUnwiredService()
	}
	s.ListEventsFn = fn
	return s
}

// ListEvents lists events for a job.
func (s *Service) ListEvents(ctx context.Context, jobID string) ([]Event, error) {
	if s == nil || s.ListEventsFn == nil {
		return nil, fmt.Errorf("%w (job_id=%s)", ErrNotWired, jobID)
	}
	return s.ListEventsFn(ctx, jobID)
}

// RegisterHandler registers a handler for the given job type.
func (s *Service) RegisterHandler(jobType string, handler any) error {
	if s == nil || s.RegisterHandlerFn == nil {
		return fmt.Errorf("%w (job_type=%s)", ErrNotWired, jobType)
	}
	return s.RegisterHandlerFn(jobType, handler)
}

// IsWired reports whether the orchestrator has injected a real impl.
func (s *Service) IsWired() bool {
	return s != nil && s.EnqueueFn != nil
}

// ── EnqueueRequest — augmenting Stage 2A with Project/ActiveKey/etc. ───────

// EnqueueRequest is the typed payload handed to Service.Enqueue.
//
// The fields map 1-1 to the Job columns written by the SQLite enqueue.
// Pre-66c646b5 the bulk_upload / clip_enrich / youtube_handlers paths
// passed these fields directly to the enqueue call, expecting them to
// be promoted onto the resulting Job by the worker pool. To preserve
// that contract without forcing every consumer to expand "Job directly",
// EnqueueRequest carries the same field names.
//
// Job.Type        ← req.Type
// Job.CorrelationID ← req.CorrelationID
// Job.MaxRetries  ← req.MaxRetries
// Job.Priority    ← req.Priority
// Job.Project     ← req.Project
// Job.ActiveKey   ← req.ActiveKey
// Job.VideoName   ← req.VideoName
// Job.Payload     ← json.Marshal(req.Payload)
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

// Compile-time assertion: *Service implements the InnerService interface
// (so callers can hand *job.Service to internal/jobs init functions
// accepting InnerService via a temporary adapter).
var _ InnerService = (*Service)(nil)
