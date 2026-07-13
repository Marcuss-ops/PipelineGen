// Package job — job_definition.go alias layer (PR-KERNEL-JOB-POPULATE, commit 9.1, July 2026).
//
// The canonical JobDefinition + ExecutionClass + Capability +
// ArtifactPolicy + CodecDescriptor + CodecDescriptorMarker now
// live in internal/kernel/job/ (the kernel subzone is the
// SOLE owner of cross-cutting contracts per godlike/06 SSOT).
//
// This file is a back-compat alias layer preserving the in-tree
// reference sites that declared `type Def = domainjob.JobDefinition`
// or referenced `domainjob.ArtifactPolicy.Validate()` directly.
// Go type aliases are transparent at the package boundary:
// `domainjob.JobDefinition` and `kerneljob.JobDefinition` are the
// same type as far as the compiler and runtime are concerned.
//
// Methods on the underlying kernel types (ExecutionClass.IsValid /
// .String, JobDefinition.Validate, ArtifactPolicy.Validate) are
// promoted across the type-alias boundary automatically — Go does
// NOT permit redeclaring them on the alias (compile error:
// "method redeclared in this block"). The aliases here are
// scheduled for cutover in the CONTRACT phase (deadline 2026-10-01)
// per the GODLIKE-07-EXPAND-BACKFILL-CUTOVER-CONTRACT discipline.
//
// Future code SHOULD import internal/kernel/job directly.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2 → commit 9) ──────────

type (
	// ExecutionClass is the canonical 3-value enum
	// (sender_only / creator_allowed / creator_only).
	ExecutionClass = kerneljob.ExecutionClass

	// ArtifactPolicy declares whether / how a job must produce
	// Artifacts (Files + Manifest + bounds).
	ArtifactPolicy = kerneljob.ArtifactPolicy

	// Capability is the canonical typed-string label for
	// worker-advertised / definition-required capabilities.
	Capability = kerneljob.Capability

	// CodecDescriptor is the canonical metadata-only marker
	// interface (SchemaVersion + JobType). Body-bearing
	// child interfaces (PayloadCodec + ResultCodec) live in
	// internal/kernel/job/codec.go.
	CodecDescriptor = kerneljob.CodecDescriptor

	// JobDefinition is the canonical registry-roster struct.
	JobDefinition = kerneljob.JobDefinition

	// CodecDescriptorMarker is the canonical metadata-only
	// marker struct (SchemaVersion + JobType + identity body
	// methods). Re-aliased here so existing
	// `domainjob.CodecDescriptorMarker` references compile.
	CodecDescriptorMarker = kerneljob.CodecDescriptorMarker
)

// ExecutionClass constants are re-exported so existing
// `domainjob.ExecutionCreatorAllowed` references continue to
// compile against the domain-job import path. The values match
// kerneljob exactly (Go const-aliases share the underlying
// string value).
const (
	ExecutionSenderOnly     = kerneljob.ExecutionSenderOnly
	ExecutionCreatorAllowed = kerneljob.ExecutionCreatorAllowed
	ExecutionCreatorOnly    = kerneljob.ExecutionCreatorOnly
)

