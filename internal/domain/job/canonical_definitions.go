// Package job — canonical_definitions.go (back-compat alias layer, P0 Commit 3, July 2026).
//
// Per PR-KERNEL-JOB-POPULATE (commit 9, July 2026), the canonical
// JobDefinition literals physically live in internal/kernel/job/. This
// file re-exports them as zero-cost aliases so legacy callers
// (internal/application/documents, internal/application/images, the
// test file in this directory) continue to compile without
// import-path churn.
//
// godlike/06 SSOT (one canonical owner per fact): kernel/job owns
// the literals; this file is the alias layer that keeps the
// composition root wiring stable per the documented design in
// job_definition_test.go (lines 79-90).
//
// Update discipline: any new canonical family added to
// internal/kernel/job/canonical_definitions.go MUST be re-aliased
// here. The compile-time chain (go build ./...) catches the
// missing alias for production callers; the test file in this
// directory (job_definition_test.go) catches missing aliases for
// the in-package test path.
//
// Layering: this file is part of the domain/job back-compat layer
// (alongside finalize_aliases.go, errors.go, registry.go, handler.go).
// New application code SHOULD import internal/kernel/job directly;
// these aliases exist for the migration window ONLY.

package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// CanonicalScriptGenerate is the back-compat alias for the canonical
// script.generate JobDefinition. Production-visible: composition
// root (internal/app/registry.go::WireRegistry) references this at
// startup wiring time; _test.go files in this package reference it
// as the "anchor" for the script.generate literal.
var CanonicalScriptGenerate = kerneljob.CanonicalScriptGenerate

// CanonicalImagesGenerate is the back-compat alias for the canonical
// images.generate JobDefinition — heavy queue, multi-image
// artifacts, capacity-2 concurrency.
var CanonicalImagesGenerate = kerneljob.CanonicalImagesGenerate

// CanonicalDocumentGenerate is the back-compat alias for the canonical
// document.generate JobDefinition — default queue, single-DOCX
// artifact.
var CanonicalDocumentGenerate = kerneljob.CanonicalDocumentGenerate

// CanonicalAssetsResolve is the back-compat alias for the canonical
// assets.resolve JobDefinition — pure-data job, zero ArtifactPolicy.
var CanonicalAssetsResolve = kerneljob.CanonicalAssetsResolve

// CanonicalClipRegister is the back-compat alias for the canonical
// media.clip JobDefinition — async batch-register endpoint.
var CanonicalClipRegister = kerneljob.CanonicalClipRegister

// CanonicalJobDefinitions is the back-compat alias for the canonical
// JobDefinition slice (all 5 families) used by composition-root
// startup wiring to register everything in a single loop.
var CanonicalJobDefinitions = kerneljob.CanonicalJobDefinitions
