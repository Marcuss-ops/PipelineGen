// Package job — registry aliases (PR-KERNEL-JOB-POPULATE, July 2026).
//
// The canonical registry types now live in internal/kernel/job.
// This file re-exports them as transparent aliases so existing
// callers that import internal/domain/job continue to compile
// during the Wave 5 contraction window.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Registry interfaces and constructor ───────────────────────────────

type (
	// MutableJobRegistry is the writable registry view.
	MutableJobRegistry = kerneljob.MutableJobRegistry

	// CompiledJobRegistry is the read-only post-freeze registry view.
	CompiledJobRegistry = kerneljob.CompiledJobRegistry

	// JobHandlerFunc is the canonical handler signature bound by the registry.
	JobHandlerFunc = kerneljob.JobHandlerFunc
)

// NewMutableJobRegistry returns a fresh, non-frozen MutableJobRegistry.
var NewMutableJobRegistry = kerneljob.NewMutableJobRegistry

// ── Sentinel errors ──────────────────────────────────────────────────

var (
	ErrRegistryFrozen   = kerneljob.ErrRegistryFrozen
	ErrDuplicateType    = kerneljob.ErrDuplicateType
	ErrUnknownJobType   = kerneljob.ErrUnknownJobType
	ErrDuplicateHandler = kerneljob.ErrDuplicateHandler
	ErrInvalidJob       = kerneljob.ErrInvalidJob
	ErrSchemaVersionEmpty = kerneljob.ErrSchemaVersionEmpty
)
