// Package models is the canonical Single Source Of Truth (SSOT) for the
// ML model identities used by PipelineGen.
//
// Policy (godlike/06): a small canonical set of models, one per
// responsibility, versioned and interchangeable through this registry.
// The registry carries identity facts only — ID, revision, dimension,
// license, role, checksum, enabled. Model WEIGHTS are never committed to
// Git; they are downloaded and cached on the server and verified against
// Checksum at fetch time.
//
//	CORE     — text (E5), visual (SigLIP), reranker (BGE), BM25 (built-in)
//	OPTIONAL — audio (CLAP), ASR (Whisper)
//
// Every component that names a model (Qdrant schema, embedding sidecar,
// query embedder, reranker server, transcription bridge, health
// handshake, diagnostics, downloader) MUST anchor to the entries here.
// Do NOT add model identity constants in other packages.
package models

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Role classifies how a model is used in the pipeline.
type Role string

const (
	// RoleTextEmbedding marks dense text-representation models (E5).
	RoleTextEmbedding Role = "text_embedding"
	// RoleVisualEmbedding marks image/text vision models for the
	// "visual" Qdrant channel (SigLIP).
	RoleVisualEmbedding Role = "visual_embedding"
	// RoleReranker marks cross-encoder relevance models (BGE reranker).
	RoleReranker Role = "reranker"
	// RoleAudioEmbedding marks audio-representation models (CLAP).
	RoleAudioEmbedding Role = "audio_embedding"
	// RoleTranscription marks ASR models producing text transcripts
	// (Whisper) — upstream of indexing, never directly into Qdrant.
	RoleTranscription Role = "transcription"
)

// Model is one canonical model identity.
//
// Revision is the pinned model release reported by the loader (empty when
// the upstream model id is the only pin available). Dimensions is the
// vector length for embedding models; cross-encoders and ASR models emit
// no vector space and carry 0.
type Model struct {
	// ID is the canonical model identifier (Hugging Face id).
	ID string
	// Revision pins the model release (e.g. "2026-06-16-v1"). Empty when
	// the model id is the only pin available in the codebase.
	Revision string
	// Dimensions is the output vector length (0 for non-vector models).
	Dimensions int
	// License is the upstream model license (SPDX-ish identifier).
	License string
	// Role is the model's responsibility in the pipeline.
	Role Role
	// Checksum is the SHA-256 hex of the model weights. It is empty until
	// the first download verifies against the upstream hub; once set, it
	// is validated by digest.IsSHA256. Weights bytes are never in Git.
	Checksum string
	// Enabled reports whether the model is part of the canonical CORE
	// production set. OPTIONAL models (CLAP, Whisper) are false. A true
	// value is the registry target — a runtime feature flag may still lag
	// behind (see Reranker below).
	Enabled bool
}

// Canonical model identity facts. These constants are the only literal
// owner for model IDs, revisions, and vector dimensions. Consumers should
// use the Model entries below (or these constants when a compile-time value
// is required), never redeclare them.
const (
	CanonicalTextModelID         = "intfloat/multilingual-e5-base"
	CanonicalTextModelRevision   = "2026-06-26-v1"
	CanonicalTextModelDimensions = 768

	CanonicalVisualModelID         = "google/siglip-so400m-patch14-384"
	CanonicalVisualModelRevision   = "2026-06-16-v1"
	CanonicalVisualModelDimensions = 768
)

// Canonical model set — one per responsibility, stable order.
var (
	// E5 is the canonical multilingual text embedding model (CORE).
	E5 = Model{
		ID:         CanonicalTextModelID,
		Revision:   CanonicalTextModelRevision,
		Dimensions: CanonicalTextModelDimensions,
		License:    "MIT",
		Role:       RoleTextEmbedding,
		Enabled:    true,
	}

	// SigLIP is the canonical visual embedding model (CORE), active in the
	// DefaultV3Schema "visual" channel and loaded by the embedding sidecar.
	SigLIP = Model{
		ID:         CanonicalVisualModelID,
		Revision:   CanonicalVisualModelRevision,
		Dimensions: CanonicalVisualModelDimensions,
		License:    "Apache-2.0",
		Role:       RoleVisualEmbedding,
		Enabled:    true,
	}

	// Reranker is the canonical cross-encoder relevance model (CORE per
	// policy). No revision is pinned upstream: the model id is the only
	// pin. Dimensions is 0 — a cross-encoder emits relevance scores, not a
	// vector space. NOTE: the runtime flag (internal/platform/ai/reranker
	// Config.Enabled) still defaults to false; activating the reranker is
	// a deliberate rollout, not a registry change.
	Reranker = Model{
		ID:         "BAAI/bge-reranker-v2-m3",
		Revision:   "",
		Dimensions: 0,
		License:    "Apache-2.0",
		Role:       RoleReranker,
		Enabled:    true,
	}

	// CLAP is the canonical audio embedding model (OPTIONAL). The
	// embedding sidecar already loads it, but the DefaultV3Schema "audio"
	// channel is commented out — enabling CLAP is a v4 migration
	// (512d audio channel + reindex + alias switch), never a silent change.
	CLAP = Model{
		ID:         "laion/clap-htsat-fused",
		Revision:   "2026-06-26-v1",
		Dimensions: 512,
		License:    "Apache-2.0",
		Role:       RoleAudioEmbedding,
		Enabled:    false,
	}

	// Whisper is the canonical ASR model (OPTIONAL, upstream of indexing:
	// video → audio → Whisper → transcript → E5 → Qdrant). The
	// transcription bridge (scripts/bridges/whisper_transcriber.py)
	// defaults to this model and permits an explicit VELOX_WHISPER_MODEL
	// override. No revision is pinned.
	Whisper = Model{
		ID:         "openai/whisper-large-v3-turbo",
		Revision:   "",
		Dimensions: 0,
		License:    "MIT",
		Role:       RoleTranscription,
		Enabled:    false,
	}
)

// canonicalOrder is the stable registry order (declaration order).
var canonicalOrder = [...]Model{E5, SigLIP, Reranker, CLAP, Whisper}

// Canonical returns the full registry in stable order.
func Canonical() []Model {
	out := make([]Model, 0, len(canonicalOrder))
	out = append(out, canonicalOrder[:]...)
	return out
}

// ByID looks up a model by its canonical id.
func ByID(id string) (Model, bool) {
	for _, m := range canonicalOrder {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Identity returns the immutable identity fingerprint "id|revision". Any
// consumer that names a model (collection naming, contract hashes, cache
// keys) MUST compose the identity through this method, never ad-hoc
// strings.
func (m Model) Identity() string {
	return fmt.Sprintf("%s|%s", m.ID, m.Revision)
}

// HasVectorSpace reports whether the model emits vectors (embedding
// models only). Cross-encoders and ASR models report false.
func (m Model) HasVectorSpace() bool {
	return m.Dimensions > 0
}

// Validate reports whether the entry is internally consistent:
// a non-empty checksum must be a valid SHA-256 hex, vector-space models
// must carry a positive dimension, and every model must have an id.
func (m Model) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("models: %s has empty id", m.Role)
	}
	if m.Role == "" {
		return fmt.Errorf("models: %s has empty role", m.ID)
	}
	if m.License == "" {
		return fmt.Errorf("models: %s has empty license", m.ID)
	}
	if m.Dimensions < 0 {
		return fmt.Errorf("models: %s has negative dimensions=%d", m.ID, m.Dimensions)
	}
	if m.Checksum != "" && !digest.IsSHA256(m.Checksum) {
		return fmt.Errorf("models: %s checksum is not SHA-256 hex", m.ID)
	}
	if m.HasVectorSpace() && m.Dimensions <= 0 {
		return fmt.Errorf("models: %s claims a vector space with dimensions=%d", m.ID, m.Dimensions)
	}
	return nil
}
