// Package script — response.go defines the canonical async wire
// shape for POST /api/script/generate (PR-morti-sync, July 2026).
//
// godlike/07 NO-FAKE-AVAILABILITY: the response struct only carries
// the 5 fields the async path actually populates. Any future sync
// helper (syncSingle, syncMulti, ...) would need to be deliberately
// re-introduced with a wire-shape audit; response_test.go locks the
// field count to exactly 5 as a forward-prevention gate.
//
// Wire shape produced by POST /api/script/generate (today):
//
//	{
//	  "ok": true,
//	  "job_id": "<uuid>",
//	  "status": "QUEUED",
//	  "status_url": "/api/jobs/<uuid>/full"
//	}
//
// doc_title is included when non-empty; omitempty drops empty strings.
// The 14 fields removed in PR-morti-sync (Script / WordCount / Title /
// Language / Model / CacheStatus / CacheHit / Count / Total / Results +
// EntitiesJSON / DocLink / DocID / Warnings) had ZERO production callers
// verified pre-PR via cross-package rg audit. Re-introducing them requires
// (a) sync-path revival with a typed-error contract and (b) wire-shape
// audit on the new field set.
package script

// GenerateResponse is the canonical typed output shape that the
// HTTP handler serialises to JSON for POST /api/script/generate.
//
// Five-field async wire shape; the field count is locked to 5 by
// internal/api/script/response_test.go as a forward-prevention gate.
type GenerateResponse struct {
	OK bool `json:"ok"`

	// Async fields — populated by the async() helper below.
	JobID     string `json:"job_id,omitempty"`
	Status    string `json:"status,omitempty"`
	StatusURL string `json:"status_url,omitempty"`
	DocTitle  string `json:"doc_title,omitempty"`
}

// async populates the response for the async (job-enqueued) branch,
// which is the SOLE live path for POST /api/script/generate. Callers
// MUST route through this helper rather than writing fields directly
// so the wire-shape contract stays self-documenting (the helper
// signature IS the contract — every field has exactly one source).
func (r *GenerateResponse) async(jobID, status, statusURL, docTitle string) {
	r.OK = true
	r.JobID = jobID
	r.Status = status
	r.StatusURL = statusURL
	r.DocTitle = docTitle
}
