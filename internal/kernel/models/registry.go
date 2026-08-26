// Package models is the canonical Single Source Of Truth (SSOT) for the ML
// model identity facts used across PipelineGen: which model serves each role
// (text/visual/audio embedding, reranking, transcription, sparse BM25), its
// pinned revision, output dimensions, license, and whether it is part of the
// canonical active stack.
//
// godlike/06 SSOT: the model IDs, revisions, and dimensions declared here are
// the single owner of those facts. Do NOT declare model literals in other
// packages — reference the canonical instances (CanonicalText, CanonicalVisual,
// ...) or the exported constants below instead.
//
// The text-embedding entry deliberately REFERENCES internal/kernel/embedding
// (the EmbeddingContract SSOT) rather than re-declaring the E5 identity facts,
// so a bump of one can never silently desync the other. The percheck gate
// `percheck_embedding_constants_ssot` fails the build if a NEW package
// re-declares the E5 model id as a constant/variable.
//
// Canonical stack (August 2026):
//
//	CORE       text      intfloat/multilingual-e5-base     MIT        768d
//	           visual    google/siglip-so400m-patch14-384  Apache-2.0  768d
//	           reranker  BAAI/bge-reranker-v2-m3           Apache-2.0  (scores)
//	           bm25      qdrant/bm25                       Apache-2.0  (sparse)
//	OPTIONAL   audio     laion/clap-htsat-fused            Apache-2.0  512d
//	           asr       openai/whisper-large-v3-turbo     MIT         (text)
//
// Weights are never stored in Git: the registry carries identity facts only;
// the model files are downloaded/cached on the server via the central
// downloader, keyed by the ID + revision below.
package models

import (
	"fmt"
	"strings"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Role identifies the functional role a model serves in the pipeline.
type Role string

const (
	// RoleTextEmbedding is the dense text/transcript vector channel.
	RoleTextEmbedding Role = "embedding"
	// RoleVisualEmbedding is the dense visual vector channel.
	RoleVisualEmbedding Role = "visual_embedding"
	// RoleAudioEmbedding is the dense audio vector channel (optional).
	RoleAudioEmbedding Role = "audio_embedding"
	// RoleReranker is the cross-encoder relevance re-scoring layer.
	RoleReranker Role = "reranker"
	// RoleTranscription is the ASR (audio → text) upstream of embedding.
	RoleTranscription Role = "transcription"
	// RoleSparse is the server-side sparse vector channel (BM25).
	RoleSparse Role = "sparse"
)

// Model is the canonical identity record of a single ML model. It carries
// identity facts only — never weights, which live on the server cache.
type Model struct {
	// ID is the canonical model id as served by Hugging Face / the sidecar
	// (e.g. "intfloat/multilingual-e5-base").
	ID string
	// Revision is the pinned model release/revision label. Empty when the
	// model has no pinned revision label yet (not in production).
	Revision string
	// Dimensions is the output vector length. 0 for non-vector models
	// (reranker emits relevance scores, ASR emits text, BM25 is sparse).
	Dimensions int
	// License is the model's license identifier (e.g. "MIT", "Apache-2.0").
	License string
	// Role is the functional role this model serves.
	Role Role
	// Enabled reports whether the model is part of the canonical active
	// stack. Optional models (audio, asr) are declared but disabled until
	// their controlled migration lands.
	Enabled bool
}

// ── Visual model identity ────────────────────────────────────────────────
// The visual revision below is the runtime truth reported by the embedding
// sidecar (scripts/services/embedding_server/__init__.py: VISUAL_MODEL_VERSION).
//
// KNOWN DRIFT (August 2026): pkg/defaults.VisualEmbeddingModelVersion and
// schema.IndexSchema DenseVectors[visual].ModelVersion still pin the stale
// "2026-06-16-v1". This registry is the new owner; the drift-fix task points
// schema + sidecar wiring at the constants here so the two strings can never
// diverge again.

// VisualModelID is the canonical SigLIP model id (full HF id used by the sidecar).
const VisualModelID = "google/siglip-so400m-patch14-384"

// VisualModelRevision is the pinned SigLIP release label (runtime truth).
const VisualModelRevision = "2026-06-26-v1"

// VisualModelDim is the SigLIP so400m-patch14-384 embedding dimensionality.
const VisualModelDim = 768

// ── Audio model identity (CLAP) ──────────────────────────────────────────

// AudioModelID is the canonical CLAP audio-embedding model id.
const AudioModelID = "laion/clap-htsat-fused"

// AudioModelRevision is the pinned CLAP release label (sidecar runtime truth).
const AudioModelRevision = "2026-06-26-v1"

// AudioModelDim is the CLAP audio embedding dimensionality.
const AudioModelDim = 512

// ── Reranker model identity ──────────────────────────────────────────────

// RerankerModelID is the canonical multilingual reranker model id.
const RerankerModelID = "BAAI/bge-reranker-v2-m3"

// ── ASR model identity ───────────────────────────────────────────────────

// ASRModelID is the canonical transcription (ASR) model id.
const ASRModelID = "openai/whisper-large-v3-turbo"

// ── BM25 sparse model identity ───────────────────────────────────────────

// BM25ModelID is the canonical server-side sparse inference model (Qdrant
// built-in; no weights are downloaded for it).
const BM25ModelID = "qdrant/bm25"

// Canonical instances — the single owner of every model identity fact.

// CanonicalText is the canonical dense text/transcript embedding model. The
// identity facts are REFERENCES to internal/kernel/embedding (the
// EmbeddingContract SSOT) so both surfaces stay in lockstep by construction.
var CanonicalText = Model{
	ID:         coreembedding.ModelIDMultilingualE5,
	Revision:   coreembedding.ModelRevisionMultilingualE5,
	Dimensions: coreembedding.DimensionText,
	License:    "MIT",
	Role:       RoleTextEmbedding,
	Enabled:    true,
}

// CanonicalVisual is the canonical dense visual embedding model (SigLIP).
var CanonicalVisual = Model{
	ID:         VisualModelID,
	Revision:   VisualModelRevision,
	Dimensions: VisualModelDim,
	License:    "Apache-2.0",
	Role:       RoleVisualEmbedding,
	Enabled:    true,
}

// CanonicalAudio is the optional dense audio embedding model (CLAP).
// Declared but disabled: the v3 Qdrant schema has the audio channel commented
// out, so the runtime service is not part of the canonical stack yet. Enabled
// flips only via the controlled v4 migration (new collection → reindex →
// alias switch).
var CanonicalAudio = Model{
	ID:         AudioModelID,
	Revision:   AudioModelRevision,
	Dimensions: AudioModelDim,
	License:    "Apache-2.0",
	Role:       RoleAudioEmbedding,
	Enabled:    false,
}

// CanonicalReranker is the canonical cross-encoder reranker. Part of the
// CORE stack per the August 2026 model review; the runtime config flag
// (RerankerConfig.Enabled) currently defaults to false and is flipped by the
// activation task.
var CanonicalReranker = Model{
	ID:      RerankerModelID,
	License: "Apache-2.0",
	Role:    RoleReranker,
	Enabled: true,
}

// CanonicalASR is the optional transcription model (Whisper large-v3-turbo).
// It is upstream of the indexing path (audio → transcript → E5 → Qdrant), not
// an embedding model itself. Declared canonical when transcripts are needed.
var CanonicalASR = Model{
	ID:      ASRModelID,
	License: "MIT",
	Role:    RoleTranscription,
	Enabled: false,
}

// CanonicalBM25 is the canonical sparse channel model. Qdrant performs the
// BM25 inference server-side (idf modifier), so no weights are downloaded.
var CanonicalBM25 = Model{
	ID:      BM25ModelID,
	License: "Apache-2.0",
	Role:    RoleSparse,
	Enabled: true,
}

// All returns the canonical registry in stable order (declaration order).
func All() []Model {
	return []Model{
		CanonicalText,
		CanonicalVisual,
		CanonicalAudio,
		CanonicalReranker,
		CanonicalASR,
		CanonicalBM25,
	}
}

// Lookup returns the canonical model with the given id (exact match on ID).
func Lookup(id string) (Model, bool) {
	for _, m := range All() {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// ByRole returns the canonical model serving the given role.
func ByRole(role Role) (Model, bool) {
	for _, m := range All() {
		if m.Role == role {
			return m, true
		}
	}
	return Model{}, false
}

// Enabled returns the subset of the canonical stack that is active.
func Enabled() []Model {
	var out []Model
	for _, m := range All() {
		if m.Enabled {
			out = append(out, m)
		}
	}
	return out
}

// Validate checks the registry invariants: every entry must have a non-empty
// id, license, and role; ids and roles must be unique; embedding roles must
// declare positive dimensions. Returns nil when the registry is safe to
// consume at runtime.
func Validate() error {
	var errs []string
	ids := make(map[string]bool)
	roles := make(map[Role]bool)

	for _, m := range All() {
		if strings.TrimSpace(m.ID) == "" {
			errs = append(errs, fmt.Sprintf("model entry with role %q: id must not be empty", m.Role))
		}
		if strings.TrimSpace(m.License) == "" {
			errs = append(errs, fmt.Sprintf("model %q: license must not be empty", m.ID))
		}
		if m.Role == "" {
			errs = append(errs, fmt.Sprintf("model %q: role must not be empty", m.ID))
		}
		if ids[m.ID] {
			errs = append(errs, fmt.Sprintf("duplicate model id %q", m.ID))
		}
		ids[m.ID] = true
		if roles[m.Role] {
			errs = append(errs, fmt.Sprintf("duplicate role %q", m.Role))
		}
		roles[m.Role] = true

		switch m.Role {
		case RoleTextEmbedding, RoleVisualEmbedding, RoleAudioEmbedding:
			if m.Dimensions <= 0 {
				errs = append(errs, fmt.Sprintf("model %q (role %q): embedding role must declare positive dimensions, got %d", m.ID, m.Role, m.Dimensions))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("internal/kernel/models: %d registry validation failure(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// Hash returns the deterministic registry fingerprint (SHA-256 hex). It is
// the drift-detection digest: any change to a model identity fact changes
// the fingerprint, so boot-time diagnostics / health handshakes can compare
// the canonical registry against the runtime sidecar and Qdrant metadata.
func Hash() string {
	var parts []string
	for _, m := range All() {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%d|%s|%t",
			m.ID, m.Revision, m.License, m.Dimensions, m.Role, m.Enabled))
	}
	return digest.SHA256String(strings.Join(parts, "\n"))
}
