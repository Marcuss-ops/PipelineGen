// Package apiutil — canonical API envelope surface for internal/capabilities/scripts.
//
// Wave 0 of the audit 2026-07-03 P0 #3 (Single canonical scripting framework).
//
// canonical home for the Response[T] shape that the script/books/lessons
// endpoints share. The legacy internal/capabilities/generation package has
// been the historical carrier of this surface; this package is the SSOT
// under godlike/06 "one canonical owner per fact". The legacy surface is
// on the deprecation path:
//
//   - CUTOVER wave (PR-GENERATION-FACADE-REMOVE / commit 7, July 2026):
//     the legacy facade at `internal/capabilities/generation/` (which
//     historically carried Response/Sync/Async) was PHYSICALLY git-rm'd
//     along with the entire generic generation facade package; the
//     canonical proprietary APIs for book/lesson/script/batch were not
//     real on disk (internal/api/content/ is a doc-only shell) so the
//     facade removal is acceptable per user spec. The forward-prevention
//     gate at cmd/archcheck/scan/percheck_no_generic_generation_facade.go
//     bans any re-introduction of the facade package.
//   - CONTRACT wave: physical git-rm of the legacy symbols closed in
//     commit 7. The three remaining call sites (internal/capabilities/
//     books/process_usecase.go, internal/capabilities/books/process_drive_usecase.go,
//     internal/capabilities/lessons/generate_usecase.go) and internal/app/
//     registry_public_modules.go had already migrated to this Response[T]
//     surface, satisfying the migration prerequisite.
//
// This file is the additive EXPAND step. The Mode constants, the
// Response[T] struct, and the Sync[T]/Async[T] constructors mirror the
// legacy surface byte-identically (JSON tags, omitempty rules, field
// order) so callers can be migrated to this package with zero behavioural
// change. CUTOVER/CONTRACT will retire the legacy symbols without
// affecting any non-deprecated call site.
package apiutil

// Mode identifies whether a generation request completed synchronously
// or was queued for background processing.
type Mode string

const (
	ModeSync  Mode = "sync"
	ModeAsync Mode = "async"
)

// Response is the shared wire envelope for all text-generation flows.
// The result payload is attached only on the synchronous branch.
//
// The envelope stays intentionally small:
//   - ok/kind/mode are always present
//   - job_id/status/job_type are async-only
//   - result is sync-only
//
// This gives the script, books, and lessons endpoints the same top-level
// shape while still allowing each endpoint to expose its own result DTO.
type Response[T any] struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`
	Mode    Mode   `json:"mode"`
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status,omitempty"`
	JobType string `json:"job_type,omitempty"`
	Result  *T     `json:"result,omitempty"`
}

// Sync builds a successful synchronous response envelope.
func Sync[T any](kind string, result T) Response[T] {
	return Response[T]{
		OK:     true,
		Kind:   kind,
		Mode:   ModeSync,
		Result: &result,
	}
}

// Async builds a successful async acknowledgment envelope.
func Async[T any](kind, jobID, status, jobType string) Response[T] {
	return Response[T]{
		OK:      true,
		Kind:    kind,
		Mode:    ModeAsync,
		JobID:   jobID,
		Status:  status,
		JobType: jobType,
	}
}
