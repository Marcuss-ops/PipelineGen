package workflowservice

import (
	"fmt"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/workflow"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]workflow.Definition
}

func NewRegistry() Registry {
	return Registry{items: make(map[string]workflow.Definition)}
}

func (r *Registry) Register(def workflow.Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if def == nil {
		return fmt.Errorf("workflow definition is nil")
	}
	key := def.Type()
	if key == "" {
		return fmt.Errorf("workflow definition type is empty")
	}
	if _, exists := r.items[key]; exists {
		return fmt.Errorf("workflow definition already registered: %s", key)
	}
	r.items[key] = def
	return nil
}

func (r *Registry) Get(typeName string) (workflow.Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.items[typeName]
	return def, ok
}
