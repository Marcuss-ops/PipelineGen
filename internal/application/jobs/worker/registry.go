package worker

import (
	"context"
	"fmt"
	"sync"

	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

type Handler func(ctx context.Context, j *domainjob.Job, tools *Tools) (map[string]any, error)

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(jobType string, h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if jobType == "" {
		return fmt.Errorf("job type is required")
	}
	if h == nil {
		return fmt.Errorf("handler is nil")
	}
	if _, exists := r.handlers[jobType]; exists {
		return fmt.Errorf("handler already registered for %s", jobType)
	}
	r.handlers[jobType] = h
	return nil
}

func (r *Registry) Dispatch(ctx context.Context, j *domainjob.Job, tools *Tools) (map[string]any, error) {
	r.mu.RLock()
	h, ok := r.handlers[j.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no worker handler registered for job type %s", j.Type)
	}
	return h(ctx, j, tools)
}
