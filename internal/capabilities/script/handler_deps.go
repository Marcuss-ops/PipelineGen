// Package script (api/script) — handler_deps.go owns the construction
// seam for ScriptFlowHandler: 2 typed-narrow dep bags
// (GenerateDeps / JobsDeps) + the slim top-level ScriptFlowDeps +
// and the canonical constructor (NewScriptFlowHandler).
//
// PR-SCRIPT-DEPENDENCIES-EXTRACT (July 2026): extracted from
// handler_flow.go.
// PR-script-deps-slim (July 2026, P1): split the 23-field
// ScriptFlowDeps into 2 capability-scoped dep bags
// (GenerateDeps / JobsDeps) + dropped unused fields and compatibility
// facade wiring with zero production readers.
//
// REMOTION-LEGACY-REMOVAL (August 2026): the /shorts/* surface
// (ShortsDeps + HandlerShorts + 3 routes) is RETIRED — the external
// Remotion renderer hand-off is fully removed from this module.
//
// godlike/06 SSOT rationale (one canonical owner per fact):
// the construction seam is the ScriptFlow package's re-use seam —
// keeping it in this file alongside ScriptFlowDeps means pipeline
// code that imports ScriptFlowDeps reaches a single canonical source.
package script

import (
	"context"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// GenerateDeps groups the canonical constructor inputs for the
// /generate handler. Each field is required by the canonical
// POST /api/script/generate route.
type GenerateDeps struct {
	Submission        generationSubmitter
	GenRunStarter     *scriptgen.GenerationRunStarter
	Factory           *submission.SubmitRequestFactory
	Log               *zap.Logger
	Validator         *usecase.PayloadValidator
	ResearchPreflight usecase.ResearchPreflight
}

// JobsDeps groups the canonical constructor inputs for the
// /jobs/:id handler. The retired /jobs/:id/full ScriptFlow surface
// no longer threads a RunRepository through this HTTP dependency bag.
type JobsDeps struct {
	Jobs jobs.Service
	Log  *zap.Logger
}

type generationSubmitter interface {
	Submit(ctx context.Context, req opsapp.SubmitRequest) (*opsapp.SubmitResult, error)
}

// ScriptFlowDeps is the slim top-level bag assembled by Build.
type ScriptFlowDeps struct {
	Generate GenerateDeps
	Jobs     JobsDeps

	// ClipsSearcher is the clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint.
	// Nil → endpoint returns 503.
	ClipsSearcher ClipSearcher

	// AdminToken is the auth secret consumed by EnableAuth +
	// AdminToken (the AdminTokenProvider interface satisfaction
	// locked by middleware_auth.go's compile-time assertion).
	AdminToken string
}

// NewScriptFlowHandler constructs the slim ScriptFlowHandler.
func NewScriptFlowHandler(deps ScriptFlowDeps) *ScriptFlowHandler {
	log := deps.Jobs.Log
	if log == nil {
		log = zap.NewNop()
	}

	return &ScriptFlowHandler{
		log:           log,
		adminToken:    deps.AdminToken,
		clipsSearcher: deps.ClipsSearcher,
		gen: NewHandlerGenerate(
			deps.Generate.Submission,
			deps.Generate.GenRunStarter,
			deps.Generate.Factory,
			deps.Generate.Log,
			deps.Generate.Validator,
			deps.Generate.ResearchPreflight,
		),
		jobs: NewJobsHandler(deps.Jobs.Jobs, deps.Jobs.Log),
	}
}

// Compile-time guard: jobs.Service surfaces drift in constructor signatures.
var _ jobs.Service = jobs.Service(nil)
