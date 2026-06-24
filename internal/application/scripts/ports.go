// Package scripts — ports.go canonicalises the cross-application port
// declarations that the script-feature handlers consume.
//
// AGENT-2 (June 2026): replaces the structural `jobRegistrar` that lived
// inline in pipeline_usecase.go with the typed Broker port below. The
// port uses the canonical `appjobs.HandlerFunc` shape from
// `internal/application/jobs` so consumer and producer share a single
// typed handler contract; the structural widening of `RegisterJobs'
// parameter was a temporary workaround to bridge the cross-package
// types, lifted permanently here.
package scripts

import (
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// Broker is the canonical port that PipelineUseCase.RegisterJobs consumes.
//
// Producers (`*jobs.Service`) satisfy the interface structurally — the
// shape is identical to `*jobs.Service.RegisterHandler(jobType string,
// handler appjobs.HandlerFunc) error`. Tests may use a stub that mimics
// the same signature via the lightweight interface below.
//
// Cross-package coupling: scripts → application/jobs for the canonical
// HandlerFunc type alias. AGENTS.md permits application-sibling imports
// for typed shims; the alternative (duplicating the handler signature)
// defeated static typing and degraded diff quality across cycles.
type Broker interface {
	RegisterHandler(jobType string, handler appjobs.HandlerFunc) error
}

// Compile-time assertion that *jobs.Service (the canonical producer)
// implements Broker. Catches signature drift between consumer-side
// port and producer-side implementation at build time rather than at
// first integration test.
var _ Broker = (*appjobs.Service)(nil)
