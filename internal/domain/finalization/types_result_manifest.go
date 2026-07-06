// types/types_result_manifest.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

import "encoding/json"

// ResultManifest is the envelope for a job's result. It carries the
// schema version, job identity, attempt counter, and capability-
// specific result data.
type ResultManifest struct {
	// SchemaVersion identifies the manifest schema (e.g. "v1").
	SchemaVersion string `json:"schema_version"`

	// JobID is the canonical job identifier.
	JobID string `json:"job_id"`

	// WorkflowID is the optional parent workflow identifier.
	WorkflowID string `json:"workflow_id,omitempty"`

	// Attempt is the job attempt counter. Used for stale-attempt
	// detection.
	Attempt int `json:"attempt"`

	// Data is the capability-specific result payload. Opaque to the
	// finalizer; stored verbatim.
	Data json.RawMessage `json:"data"`
}
