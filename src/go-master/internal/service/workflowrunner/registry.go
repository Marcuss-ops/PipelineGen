package workflowrunner

import (
	"context"
	"sync"
)

// Registry stores registered step executors
type Registry struct {
	mu        sync.RWMutex
	executors map[string]StepExecutor
}

var defaultRegistry = &Registry{
	executors: make(map[string]StepExecutor),
}

// Register adds a step executor to the default registry
func Register(name string, executor StepExecutor) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	defaultRegistry.executors[name] = executor
}

// Get retrieves a step executor from the default registry
func Get(name string) (StepExecutor, bool) {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	executor, ok := defaultRegistry.executors[name]
	return executor, ok
}

// List returns all registered executor names
func List() []string {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	names := make([]string, 0, len(defaultRegistry.executors))
	for name := range defaultRegistry.executors {
		names = append(names, name)
	}
	return names
}

// RegisterService is a convenience function to register a service as a step executor
// using a stepFunc adapter
func RegisterService(name string, fn StepFunc) {
	Register(name, &stepFuncExecutor{fn: fn})
}

// StepFunc is a function type that can be used as a StepExecutor
type StepFunc func(ctx context.Context, input *StepInput) (*StepOutput, error)

type stepFuncExecutor struct {
	fn StepFunc
}

func (e *stepFuncExecutor) Execute(ctx context.Context, input *StepInput) (*StepOutput, error) {
	return e.fn(ctx, input)
}
