package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Sentinel errors for registry operations.
var (
	ErrHandlerNotRegistered = errors.New("worker handler not registered")
	ErrEmptyJobType         = errors.New("job type must not be empty or whitespace")
	ErrNilHandler           = errors.New("handler must not be nil")
	ErrDuplicateHandler     = errors.New("handler already registered")
	ErrRegistryFrozen       = errors.New("registry is frozen: cannot register after startup")
	ErrNoHandlers           = errors.New("worker has no registered handlers")
	ErrUnsupportedJobType   = errors.New("unsupported job type")
)

type Handler func(ctx context.Context, j *domainjob.Job, tools *Tools) (map[string]any, error)

// Registry maps job types to handler functions. Once frozen, no new
// registrations are accepted — this prevents the claim loop from picking
// up handlers added after startup. Safe for concurrent reads.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	frozen   bool
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register adds a handler for the given job type. Returns a sentinel error
// if the type is empty/whitespace, the handler is nil, a duplicate exists,
// or the registry is already frozen.
func (r *Registry) Register(jobType string, h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return ErrRegistryFrozen
	}
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return ErrEmptyJobType
	}
	if h == nil {
		return ErrNilHandler
	}
	if _, exists := r.handlers[jobType]; exists {
		return ErrDuplicateHandler
	}
	r.handlers[jobType] = h
	return nil
}

// Freeze prevents any further registrations. Must be called before the
// claim loop starts. Once frozen, Register returns ErrRegistryFrozen.
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// Len returns the number of registered handlers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers)
}

// Has returns true if a handler is registered for the given job type.
func (r *Registry) Has(jobType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[jobType]
	return ok
}

// JobTypes returns a sorted, defensive copy of all registered job types.
// The returned slice is safe to modify without affecting the registry.
func (r *Registry) JobTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for t := range r.handlers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Dispatch routes a job to its registered handler. Returns
// ErrHandlerNotRegistered if no handler exists for the job type.
func (r *Registry) Dispatch(ctx context.Context, j *domainjob.Job, tools *Tools) (map[string]any, error) {
	r.mu.RLock()
	h, ok := r.handlers[j.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrHandlerNotRegistered, j.Type)
	}
	return h(ctx, j, tools)
}
