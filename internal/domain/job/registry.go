// Package job — registry alias layer (PR-KERNEL-JOB-POPULATE, July 2026).
//
// The canonical MutableJobRegistry + CompiledJobRegistry + JobHandlerFunc
// now live in internal/kernel/job/ (the kernel subzone is the SOLE owner
// of cross-cutting job mechanism contracts per godlike/06 SSOT).
//
// This file is a back-compat alias layer preserving the in-tree reference
// sites that imported these types from internal/domain/job. Go type aliases
// are transparent at the package boundary.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job registry types ─────────────

type (
	// MutableJobRegistry is the writable build-up view of the job registry.
	MutableJobRegistry = kerneljob.MutableJobRegistry

	// CompiledJobRegistry is the read-only post-Freeze view.
	CompiledJobRegistry = kerneljob.CompiledJobRegistry

	// JobHandlerFunc is the canonical handler signature bound to each
	// JobDefinition.
	JobHandlerFunc = kerneljob.JobHandlerFunc
)

// ── Sentinel error aliases ──────────────────────────────────────────
var (
	ErrRegistryFrozen   = kerneljob.ErrRegistryFrozen
	ErrDuplicateType    = kerneljob.ErrDuplicateType
	ErrUnknownJobType   = kerneljob.ErrUnknownJobType
	ErrDuplicateHandler = kerneljob.ErrDuplicateHandler
	ErrInvalidJob       = kerneljob.ErrInvalidJob
	ErrSchemaVersionEmpty = kerneljob.ErrSchemaVersionEmpty
)

// NewMutableJobRegistry is re-exported so existing callers that import
// internal/domain/job continue to compile. The canonical implementation
// lives in internal/kernel/job/registry.go.
var NewMutableJobRegistry = kerneljob.NewMutableJobRegistry
