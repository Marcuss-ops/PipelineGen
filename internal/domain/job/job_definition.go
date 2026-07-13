// Package job — job_definition.go alias layer (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
//
// The canonical JobDefinition + ExecutionClass + Capability +
// ArtifactPolicy + CodecDescriptor now live in
// internal/kernel/job/job_definition.go (the kernel subzone is the
// SOLE owner of cross-cutting contracts per godlike/06 SSOT).
//
// This file is a back-compat alias layer preserving the in-tree
// reference sites that declared `type Def = domainjob.JobDefinition`
// or referenced `domainjob.ArtifactPolicy.Validate()` directly.
// Go type aliases are transparent: `domainjob.JobDefinition` and
// `kerneljob.JobDefinition` are the same type as far as the compiler
// and runtime are concerned.
//
// Future code SHOULD import internal/kernel/job directly. The aliases
// here are scheduled for cutover in the CONTRACT phase (deadline
// 2026-10-01) per the migration schedule preserved in the previous
// version of this file (GODLIKE-07-EXPAND-BACKFILL-CUTOVER-CONTRACT).
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

// IsValid is a method-on-typealias issue: since ExecutionClass is a
// type alias of kerneljob.ExecutionClass, calling
// `domainjob.ExecutionClass.IsValid()` resolves to
// `kerneljob.ExecutionClass.IsValid()` (Go promotes methods across
// type-alias boundaries). Re-exported for documentation only — the
// canonical implementation lives at
// internal/kernel/job/job_definition.go::IsValid.
func (e ExecutionClass) IsValid() bool {
	return kerneljob.ExecutionClass(e).IsValid()
}

// String is likewise method-promoted via the type alias to the
// kernel-side String() method. Re-declared here for clarity.
func (e ExecutionClass) String() string {
	return kerneljob.ExecutionClass(e).String()
}

// Validate is method-promoted via the type alias to the kernel-side
// Validate() method on JobDefinition. Re-declared for clarity.
func (d JobDefinition) Validate() error {
	return kerneljob.JobDefinition(d).Validate()
}

// Validate is method-promoted via the type alias to the kernel-side
// Validate() method on ArtifactPolicy. Re-declared for clarity.
func (p ArtifactPolicy) Validate() error {
	return kerneljob.ArtifactPolicy(p).Validate()
}

// NewCodecDescriptorMarker is re-exported so existing
// `domainjob.NewCodecDescriptorMarker(...)` references in
// registry_codec_completeness_test.go + canonical_definitions.go
// continue to compile against the domain-job import path.
// Composition is the canonical constructor lives at
// internal/kernel/job/codec.go::NewCodecDescriptorMarker.
func NewCodecDescriptorMarker(codecMarker, jobType string) CodecDescriptor {
	return kerneljob.NewCodecDescriptorMarker(codecMarker, jobType)
}
