// Package defaults — qdrant.go: canonical Qdrant ANN index
// constants (Wave YY, image ingest drift-fix, July 2026).
//
// Per godlike/06 SSOT (one canonical owner per fact): the
// cross-layer constants for the Qdrant ANN index live here in
// pkg/defaults because BOTH the application layer (image ingest
// JSON metadata stamping) and the infrastructure layer (the v3
// schema spec, embedding-version assertion in infra tests) need
// to anchor to the exact same value. Co-locating the constant
// in the application or infrastructure layer would create a
// dependency cycle; pkg/defaults is leaf-only and imports no
// internal packages (AGENTS.md "pkg is leaf-only" rule).
//
// Direction-of-import: pkg/defaults (no internal imports) ←
// internal/platform/qdrant/schema (re-exports via
// `const VisualEmbeddingModelVersion = defaults.VisualEmbeddingModelVersion`)
// AND ← internal/capabilities/images/workflow (consumes directly).
//
// The re-export pattern in schema.go is the godlike/07
// fail-closed-availability contract for backward compat: any
// existing infra-layer consumer (e.g.
// internal/platform/qdrant/search/embedders_dim_test.go)
// that imports the const from `schema` continues to compile
// unchanged. The canonical declaration lives here; the
// re-export is a thin alias that makes `schema.VisualEmbeddingModelVersion`
// a derived symbol.
package defaults

// Qdrant-specific defaults — minimal surface, just the constants
// both the application image-ingest path AND the infrastructure
// schema spec need to agree on. Adding a new constant here
// requires: (1) declare here, (2) optionally re-export from
// schema.go if infra-layer consumers need it, (3) update
// architecture/deprecations.yaml if the constant replaces a
// deprecation-registered one.

// VisualEmbeddingModelVersion pins the SigLIP model version
// baked into media_assets.metadata_json.embedding_version_visual
// AND schema.IndexSchema.DenseVectors[visual].ModelVersion.
//
// Updating this value is a schema migration that changes the
// Qdrant collection shape (named vector "visual" + payload
// field embedding_version_visual). Don't bump without planning
// a v4 (see QDRANT-003 schema migration notes).
const VisualEmbeddingModelVersion = "2026-06-16-v1"
