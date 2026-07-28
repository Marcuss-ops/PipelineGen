// Package script — response.go defines the canonical async wire
// shape for POST /api/script/generate (PR-morti-sync, July 2026).
//
// godlike/07 NO-FAKE-AVAILABILITY: the response struct only carries
// the 6 fields the async path actually populates. Any future sync
// helper (syncSingle, syncMulti, ...) would need to be deliberately
// re-introduced with a wire-shape audit; response_test.go locks the
// field count to exactly 6 as a forward-prevention gate.
//
// Wire shape produced by POST /api/script/generate (today):
//
//	{
//	  "ok": true,
//	  "job_id": "<uuid>",
//	  "status": "QUEUED",
//	  "status_url": "/api/jobs/<uuid>/full",
//	  "current_stage": "NORMALIZING"
//	}
//
// doc_title is included when non-empty; omitempty drops empty strings.
// current_stage is populated by the pipeline-run creation step
// (scriptgeneration.StartAndSubmit) so the client immediately knows
// the workflow phase (verdetto § "La POST deve creare il run prima
// di qualsiasi I/O").
// The 14 fields removed in PR-morti-sync (Script / WordCount / Title /
// Language / Model / CacheStatus / CacheHit / Count / Total / Results +
// EntitiesJSON / DocLink / DocID / Warnings) had ZERO production callers
// verified pre-PR via cross-package rg audit. Re-introducing them requires
// (a) sync-path revival with a typed-error contract and (b) wire-shape
// audit on the new field set.
package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/primitives"
)

// GenerateResponse is the canonical typed output shape that the
// HTTP handler serialises to JSON for POST /api/script/generate.
//
// Six-field async wire shape (5 base async fields + current_stage).
type GenerateResponse struct {
	OK bool `json:"ok"`

	// Async fields — populated by the async() helper below.
	// PR-DOMAIN-PRIMITIVES-NOMINAL (July 2026): JobID is the canonical
	// nominal type (zero-cost on the wire — Go's `type X string` emits
	// the underlying string in JSON unchanged). Boundary code (HTTP
	// handler) wraps raw input via primitives.NewJobID; the helper
	// methods below accept `string` for backward-compat and wrap
	// internally so callers don't need to change.
	JobID        primitives.JobID `json:"job_id,omitempty"`
	Status       string           `json:"status,omitempty"`
	StatusURL    string           `json:"status_url,omitempty"`
	DocTitle     string           `json:"doc_title,omitempty"`
	CurrentStage string           `json:"current_stage,omitempty"`
}

// async populates the response for the async (job-enqueued) branch,
// which is the SOLE live path for POST /api/script/generate. Callers
// MUST route through this helper rather than writing fields directly
// so the wire-shape contract stays self-documenting (the helper
// signature IS the contract — every field has exactly one source).
func (r *GenerateResponse) async(jobID, status, statusURL, docTitle string) {
	r.OK = true
	r.JobID = primitives.NewJobID(jobID)
	r.Status = status
	r.StatusURL = statusURL
	r.DocTitle = docTitle
}

// asyncWithStage populates the response including the current pipeline
// stage. Called when the handler creates a GenerationRun before
// submission (verdetto § "Creare il pipeline_run prima di ogni
// chiamata esterna"). Delegates to async() for the common fields.
func (r *GenerateResponse) asyncWithStage(jobID, status, statusURL, docTitle, currentStage string) {
	r.async(jobID, status, statusURL, docTitle)
	r.CurrentStage = currentStage
}
