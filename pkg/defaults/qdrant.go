// Package defaults — qdrant.go: legacy Qdrant ANN index constants
// (Wave YY, image ingest drift-fix, July 2026).
//
// The SigLIP model identity facts (id, revision, dimensions) that this
// file used to declare moved to the canonical model registry
// (internal/kernel/models, godlike/06 SSOT). Consumers MUST reference
// internal/kernel/embedding.VisualEmbeddingModelVersion (which aliases
// models.CanonicalVisualModelRevision) instead of redeclaring a literal
// here — pkg/defaults is leaf-only and cannot import internal packages,
// so a literal in this package would silently drift from the registry.
//
// This file is retained so pkg/defaults/qdrant.go stays in the
// component-coverage golden set; it intentionally declares no model
// identity constants.
package defaults
