// Package qdrant — embedding adapter implementations.
//
// Provides concrete adapters that wrap the existing embedding infrastructure
// (HTTPTextEmbedder from internal/infrastructure/embeddings, plus HTTP clients
// for the Python sidecar's visual/audio endpoints) behind the canonical
// TextEmbedder / ImageEmbedder interfaces declared in searcher.go.
//
// QDRANT-003 compliance:
//   - Every embedding is produced by a real model, never synthesized.
//   - Visual: SigLIP so400m patch14-384 (768d) via /embed_visual_from_image.
//   - Audio: CLAP HTSAT (512d) via /embed_audio_from_file.
//   - Model identity and version are declared in the schema.IndexSchema manifest.
//   - Audio is optional — returns transport.ErrChannelUnavailable when the sidecar
//     reports the model not loaded (HTTP 501).
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// Godlike/06 SSOT: canonical 768d visual embedding guard.
//
// The Python sidecar produces a 768d SigLIP vector. Any other
// dimensionality indicates a model-rotation drift or a buggy sidecar
// build. We fail closed with a typed sentinel rather than silently
// upserting a wrong-shape vector to Qdrant (which would corrupt the
// server-side payload index + downstream RRF fusion).
var ErrInvalidVisualEmbeddingDim = errors.New(
	"visual embed: embedding vector dimension does not match canonical SigLIP 768d shape",
)

// validateVisualEmbeddingDim is the single canonical guard for visual
// embedding dimensionality. Exposed (lowercase, package-private) so
// embedders_dim_test.go can exercise it without HTTP/sidecar mocking,
// and so embedSingle can call it as the post-conversion bottleneck.
func validateVisualEmbeddingDim(vec []float32) error {
	if len(vec) != schema.VisualEmbeddingDim {
		return fmt.Errorf("%w: got %d, expected %d",
			ErrInvalidVisualEmbeddingDim, len(vec), schema.VisualEmbeddingDim)
	}
	return nil
}

// ── TextEmbedder adapter ──────────────────────────────────────────────

// textEmbedderAdapter wraps any asset.Embedder (the canonical domain-level
// text-embedding contract) as a qdrant.TextEmbedder. The concrete
// implementation is typically *embeddings.HTTPTextEmbedder or
// *embeddings.PythonScriptEmbedder — both satisfy asset.Embedder.
type textEmbedderAdapter struct {
	inner asset.Embedder
}

// NewTextEmbedderAdapter creates a TextEmbedder from any asset.Embedder.
func NewTextEmbedderAdapter(inner asset.Embedder) TextEmbedder {
	return &textEmbedderAdapter{inner: inner}
}

// Compile-time assertion.
var _ TextEmbedder = (*textEmbedderAdapter)(nil)

func (a *textEmbedderAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	result, err := a.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	return result.Vector, nil
}

// ── ImageEmbedder adapter ─────────────────────────────────────────────

// ImageEmbedderConfig holds the sidecar URL and timeout for the visual
// embedding server (scripts/services/embedding_server/visual.py).
type ImageEmbedderConfig struct {
	// ServerURL is the base URL of the Python embedding sidecar
	// (e.g. "http://127.0.0.1:8001").
	ServerURL string
	// Timeout is the HTTP client timeout. Zero means 30s.
	Timeout time.Duration
}

// imageEmbedderAdapter calls the Python sidecar's /embed_visual_from_image endpoint
// to generate real SigLIP (768d) embeddings from image files. Returns typed errors on HTTP
// failures or dimension mismatches.
type imageEmbedderAdapter struct {
	serverURL  string
	httpClient *http.Client
	log        *zap.Logger
	schema     *schema.IndexSchema // canonical model identity for the "visual" channel
}

// NewImageEmbedderAdapter creates an ImageEmbedder pointing at a sidecar.
// Pass serverURL="" to signal that visual embeddings are unavailable
// (EmbedImages will return transport.ErrChannelUnavailable). schema may be nil in
// tests where schema.IndexSchema validation is not needed.
func NewImageEmbedderAdapter(cfg ImageEmbedderConfig, schema *schema.IndexSchema, log *zap.Logger) ImageEmbedder {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &imageEmbedderAdapter{
		serverURL:  cfg.ServerURL,
		httpClient: &http.Client{Timeout: timeout},
		log:        log,
		schema:     schema,
	}
}

// Compile-time assertion.
var _ ImageEmbedder = (*imageEmbedderAdapter)(nil)

// EmbedImages calls /embed_visual_from_images (the canonical batch endpoint)
// once and returns the generated embeddings. The sidecar accepts N image
// paths in a single request, runs one batched siglip_model.encode pass under
// a single _inference_sem acquisition, and returns an order-preserved array
// of N×768d vectors.
//
// N images require 1 HTTP round-trip — replaces the prior N-round-trip
// per-image loop. Port interface signature unchanged; caller-facing
// semantics unchanged. Returns transport.ErrChannelUnavailable when no
// sidecar URL is configured (model wiring absent).
func (a *imageEmbedderAdapter) EmbedImages(ctx context.Context, imagePaths []string) ([][]float32, error) {
	if a.serverURL == "" {
		return nil, &transport.ErrChannelUnavailable{Channel: "visual"}
	}
	if len(imagePaths) == 0 {
		return [][]float32{}, nil
	}
	return a.embedBatch(ctx, imagePaths)
}

// embedBatch calls /embed_visual_from_images which accepts
// {"image_paths": [...]} and returns one batched vector per input path.
// Single HTTP round-trip for the whole batch.
//
// godlike/07 fail-closed batch semantics:
//   - any non-200 ⇒ propagate the error (do NOT silently drop items).
//   - response.count != len(imagePaths) ⇒ fail-closed mismatch error.
//   - per-vector dimension drift ⇒ fail-closed (godlike/06).
//   - HTTP 501 (model not loaded) ⇒ transport.ErrChannelUnavailable (same
//     sentinel as the legacy per-image path so callers short-circuit
//     identically).
func (a *imageEmbedderAdapter) embedBatch(ctx context.Context, imagePaths []string) ([][]float32, error) {
	// QDRANT-003: call /embed_visual_from_images which accepts
	// {"image_paths": [...]} and uses SigLIP image encoder (768d) in a single
	// batched forward pass.
	// QDRANT-001: validate canonical sidecar envelope (model, model_version,
	// dimensions, count) — reject incomplete or inconsistent responses.
	payload, err := json.Marshal(map[string][]string{"image_paths": imagePaths})
	if err != nil {
		return nil, fmt.Errorf("marshal visual batch embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.serverURL+"/embed_visual_from_images", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create visual batch embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("visual batch embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotImplemented {
		// 501 = SigLIP model not loaded — not an error, channel unavailable.
		return nil, &transport.ErrChannelUnavailable{Channel: "visual"}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("visual batch embed HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Embeddings   [][]float64 `json:"embeddings"`
		Dimensions   int         `json:"dimensions"`
		Count        int         `json:"count"`
		Model        string      `json:"model"`
		ModelVersion string      `json:"model_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode visual batch embed response: %w", err)
	}

	// QDRANT-001: validate canonical sidecar envelope.
	if parsed.Model == "" {
		return nil, fmt.Errorf("visual batch embed: missing 'model' in sidecar response")
	}
	if parsed.ModelVersion == "" {
		return nil, fmt.Errorf("visual batch embed: missing 'model_version' in sidecar response")
	}
	if parsed.Dimensions <= 0 {
		return nil, fmt.Errorf("visual batch embed: missing or invalid 'dimensions' in sidecar response")
	}
	if parsed.Count != len(imagePaths) {
		return nil, fmt.Errorf("visual batch embed: count=%d but requested %d", parsed.Count, len(imagePaths))
	}
	if len(parsed.Embeddings) != parsed.Count {
		return nil, fmt.Errorf("visual batch embed: len(embeddings)=%d but count=%d", len(parsed.Embeddings), parsed.Count)
	}

	// Per-vector dimension check before model-identity (drift surfaces first).
	for i, vec := range parsed.Embeddings {
		if len(vec) != parsed.Dimensions {
			return nil, fmt.Errorf("visual batch embed: embeddings[%d] length=%d but dimensions=%d",
				i, len(vec), parsed.Dimensions)
		}
	}

	// QDRANT-001: validate model identity against the schema.IndexSchema manifest.
	// Skip when schema is nil (test-only path).
	if a.schema != nil {
		if spec := a.schema.GetDense("visual"); spec != nil {
			if !modelNameMatches(parsed.Model, spec.Model) {
				return nil, fmt.Errorf("visual batch embed: model identity mismatch: sidecar returned %q, schema expects %q",
					parsed.Model, spec.Model)
			}
			if parsed.Dimensions != spec.Dimensions {
				return nil, fmt.Errorf("visual batch embed: dimension mismatch: sidecar returned %d, schema expects %d",
					parsed.Dimensions, spec.Dimensions)
			}
		}
	}

	out := make([][]float32, len(parsed.Embeddings))
	for i, vec := range parsed.Embeddings {
		v32 := make([]float32, len(vec))
		for j, v := range vec {
			v32[j] = float32(v)
		}
		// Post-conversion canonical 768d guard. The pre-conversion
		// spec.Dimensions check above only fires when a.serverURL is wired
		// through NewImageEmbedderAdapter with a non-nil schema (test paths
		// sometimes pass schema=nil). This guard is the universal
		// bottleneck so dim drift never reaches Qdrant (godlike/06).
		if err := validateVisualEmbeddingDim(v32); err != nil {
			return nil, err
		}
		out[i] = v32
	}
	return out, nil
}

// ── AudioEmbedder adapter ─────────────────────────────────────────────

// audioEmbedderAdapter calls the Python sidecar's /embed_audio_from_file endpoint
// to generate real CLAP embeddings from audio files. Audio is optional — when the sidecar
// is unavailable (URL empty or CLAP model not loaded), EmbedAudio returns
// transport.ErrChannelUnavailable so the caller can proceed without audio vectors.
type audioEmbedderAdapter struct {
	serverURL  string
	httpClient *http.Client
	log        *zap.Logger
	schema     *schema.IndexSchema // canonical model identity for the "audio" channel
}

// EmbedAudio calls /embed_audio for each audio path and returns the
// generated embeddings. Returns transport.ErrChannelUnavailable when no sidecar
// URL is configured or the CLAP model failed to load.
func (a *audioEmbedderAdapter) EmbedAudio(ctx context.Context, audioPaths []string) ([][]float32, error) {
	if a.serverURL == "" {
		return nil, &transport.ErrChannelUnavailable{Channel: "audio"}
	}

	out := make([][]float32, 0, len(audioPaths))
	for _, path := range audioPaths {
		vec, err := a.embedSingle(ctx, path)
		if err != nil {
			return out, err
		}
		out = append(out, vec)
	}
	return out, nil
}

// ── Model name matching ──────────────────────────────────────────────

// modelNameMatches compares a sidecar-returned model name (which may include
// a vendor prefix like "google/siglip-..." or "laion/clap-...") against the
// shorter canonical form stored in schema.IndexSchema (e.g. "siglip-...").
//
// The comparison is:
//  1. Exact match (sidecar model == schema model).
//  2. Last-component match: everything after the final "/" is compared
//     against the schema model name. This handles the common case where
//     the Python sidecar uses "google/siglip-so400m-patch14-384" while
//     schema.IndexSchema stores "siglip-so400m-patch14-384".
func modelNameMatches(sidecarModel, schemaModel string) bool {
	if sidecarModel == schemaModel {
		return true
	}
	if idx := strings.LastIndex(sidecarModel, "/"); idx >= 0 {
		return sidecarModel[idx+1:] == schemaModel
	}
	return false
}

func (a *audioEmbedderAdapter) embedSingle(ctx context.Context, audioPath string) ([]float32, error) {
	// QDRANT-003: call /embed_audio_from_file which accepts {"audio_path": "..."}
	// and uses CLAP audio encoder (512d).
	// QDRANT-001: validate canonical sidecar envelope (model, model_version,
	// dimensions) — reject incomplete or inconsistent responses.
	payload, err := json.Marshal(map[string]string{"audio_path": audioPath})
	if err != nil {
		return nil, fmt.Errorf("marshal audio embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.serverURL+"/embed_audio_from_file", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create audio embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audio embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotImplemented {
		// 501 = CLAP model not loaded — not an error, channel unavailable.
		return nil, &transport.ErrChannelUnavailable{Channel: "audio"}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("audio embed HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Embedding    []float64 `json:"embedding"`
		Dimensions   int       `json:"dimensions"`
		Model        string    `json:"model"`
		ModelVersion string    `json:"model_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode audio embed response: %w", err)
	}

	// QDRANT-001: validate canonical sidecar envelope.
	if parsed.Model == "" {
		return nil, fmt.Errorf("audio embed: missing 'model' in sidecar response")
	}
	if parsed.ModelVersion == "" {
		return nil, fmt.Errorf("audio embed: missing 'model_version' in sidecar response")
	}
	if parsed.Dimensions <= 0 {
		return nil, fmt.Errorf("audio embed: missing or invalid 'dimensions' in sidecar response")
	}
	if parsed.Dimensions != len(parsed.Embedding) {
		return nil, fmt.Errorf("audio embed: dimensions=%d but embedding length=%d", parsed.Dimensions, len(parsed.Embedding))
	}

	// QDRANT-001: validate model identity against the schema.IndexSchema manifest.
	// Skip when schema is nil (test-only path).
	if a.schema != nil {
		if spec := a.schema.GetDense("audio"); spec != nil {
			if !modelNameMatches(parsed.Model, spec.Model) {
				return nil, fmt.Errorf("audio embed: model identity mismatch: sidecar returned %q, schema expects %q",
					parsed.Model, spec.Model)
			}
			if parsed.Dimensions != spec.Dimensions {
				return nil, fmt.Errorf("audio embed: dimension mismatch: sidecar returned %d, schema expects %d",
					parsed.Dimensions, spec.Dimensions)
			}
		}
	}

	out := make([]float32, len(parsed.Embedding))
	for i, v := range parsed.Embedding {
		out[i] = float32(v)
	}
	return out, nil
}
