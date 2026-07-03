// Package jobs — handler_registration.go: handler binding surface.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behavior changes.
//
// Forward-pointer: the reflection-based RegisterHandler fallback
// (reflect.ValueOf/Call) is a godlike/07 anti-pattern retained for
// mechanical-split purity; a follow-up PR will remove it and enforce
// HandlerFunc-only registration.
package jobs

import (
	"context"
	"fmt"
	"reflect"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// RegisterHandler registers a handler for the given job type.
// Accepts any handler; performs a type-assertion to HandlerFunc.
// Implements job.Service interface.
func (s *Service) RegisterHandler(jobType string, handler any) error {
	switch h := handler.(type) {
	case HandlerFunc:
		return s.dispatcher.Register(jobType, h)
	case func(context.Context, *job.Job, *JobTools) (map[string]any, error):
		return s.dispatcher.Register(jobType, HandlerFunc(h))
	}

	rv := reflect.ValueOf(handler)
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
	}
	rt := rv.Type()
	if rt.NumIn() != 3 || rt.NumOut() != 2 {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
	}
	if !rt.In(0).AssignableTo(reflect.TypeOf((*context.Context)(nil)).Elem()) ||
		!rt.In(1).AssignableTo(reflect.TypeOf((*job.Job)(nil))) ||
		!rt.In(2).AssignableTo(reflect.TypeOf((*JobTools)(nil))) ||
		!rt.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
	}

	wrapped := func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		results := rv.Call([]reflect.Value{
			reflect.ValueOf(ctx),
			reflect.ValueOf(j),
			reflect.ValueOf(tools),
		})
		var out map[string]any
		if !results[0].IsNil() {
			out, _ = results[0].Interface().(map[string]any)
		}
		var err error
		if !results[1].IsNil() {
			err, _ = results[1].Interface().(error)
		}
		return out, err
	}
	return s.dispatcher.Register(jobType, wrapped)
}

// HasHandler reports whether the broker has a handler registered
// for the given job type. Issue 7 / P1 (June 2026): added so the
// composition root can fail-fast on a script.generate wiring gap
// without leaking the Dispatcher type into the API surface.
//
// The query is branch-free -- the Dispatcher.AllHandlers() map is
// the canonical record. Returns false when:
//
//   - the receiver is nil (defensive guard)
//   - the dispatcher is nil (composition bug)
//   - no handler is registered for jobType
//
// Nil-tolerant: this method never panics; nil-receiver callers get
// false (so composition-root code can pass s.Service==nil through
// the validateScriptGenerateWiring helper without pre-checking).
func (s *Service) HasHandler(jobType string) bool {
	if s == nil {
		return false
	}
	if s.dispatcher == nil {
		return false
	}
	if jobType == "" {
		return false
	}
	_, ok := s.dispatcher.AllHandlers()[jobType]
	return ok
}

// ValidateHandlerCompleteness checks that every job type registered in
// the canonical Registry has a handler bound to the Dispatcher. Returns
// nil when every job type is consumable; returns an error listing the
// first missing handler when a registration gap is detected.
//
// §15.9 (July 2026): the voiceover parent-child fan-out pair is the
// canonical trigger — when voiceover.generate_item has no handler, the
// server MUST NOT start because the parent's fan-out creates child jobs
// that can never be executed. ValidateHandlerCompleteness is the gate
// the composition root calls before Freeze().
//
// Nil-tolerant: nil receiver, nil dispatcher, and nil registry all
// return nil (the belt-and-suspenders check runs later, after the
// composition root has wired both).
func (s *Service) ValidateHandlerCompleteness(reg *Registry) error {
	if s == nil || s.dispatcher == nil || reg == nil {
		return nil
	}
	handlers := s.dispatcher.AllHandlers()
	for _, jobType := range reg.AllTypes() {
		if _, ok := handlers[jobType]; !ok {
			return fmt.Errorf("job.Service.ValidateHandlerCompleteness: job type %q is registered in the canonical Registry but has NO handler bound to the Dispatcher — the server MUST NOT start with a consumable-type-without-handler gap (§15.9 registrazione incompleta)", jobType)
		}
	}
	return nil
}
