package voiceover

import (
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	domainvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
)

// CriticalHandler is the bind specification consumed by the application
// composition root's fail-closed startup validator.
type CriticalHandler struct {
	Name string
	Bind func(*appjobs.Service) error
}

type HandlerRegistrar interface {
	Register(*appjobs.Service) error
}

// CriticalHandlers builds the voiceover registrations that must be rechecked
// before workers start. The generate binding preserves the existing
// HasHandler-gated idempotency behavior.
func CriticalHandlers(service *appjobs.Service, generate, generateItem HandlerRegistrar) []CriticalHandler {
	if service == nil {
		return nil
	}
	var handlers []CriticalHandler
	if generate != nil {
		handler := generate
		handlers = append(handlers, CriticalHandler{
			Name: "voiceover.generate",
			Bind: func(svc *appjobs.Service) error {
				if svc.HasHandler(domainvoiceover.TypeGenerate) {
					return nil
				}
				return handler.Register(svc)
			},
		})
	}
	if generateItem != nil {
		handler := generateItem
		handlers = append(handlers, CriticalHandler{
			Name: "voiceover.generate_item",
			Bind: handler.Register,
		})
	}
	return handlers
}
