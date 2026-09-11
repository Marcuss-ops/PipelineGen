package asset

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// Canonical asset job type constants.
//
// The SHARED wire strings are OWNED by internal/kernel/job (godlike/06 one
// owner per fact); the entries below are compile-time re-exports so the
// composition root and this domain can never drift.
const (
	// TypeResolve is the canonical job type for semantic asset resolution.
	TypeResolve = job.TypeAssetsResolve

	// TypeTextMaterialize is the canonical job type for the text-track
	// materialization pipeline.
	TypeTextMaterialize = job.TypeAssetTextMaterialize
)
