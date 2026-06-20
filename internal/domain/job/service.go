package job

import (
	"context"
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
	EnqueueFn    func(ctx context.Context, req *EnqueueRequest) (*Job, error)
	GetFn        func(ctx context.Context, id string) (*Job, error)
	CancelFn     func(ctx context.Context, id string) error
	ListFn       func(ctx context.Context, filter Filter) ([]*Job, error)
	IsTerminalFn func(status Status) bool
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
