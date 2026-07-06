// Package qdrant — embedding adapter implementations.
//
// Provides concrete adapters that wrap the existing embedding infrastructure
// (HTTPTextEmbedder from internal/infrastructure/embeddings, plus HTTP clients
// for the Python sidecar's visual/audio endpoints) behind the canonical
// TextEmbedder / ImageEmbedder interfaces declared in searcher.go.
// (AudioEmbedder is commented out — YAGNI, July 2026).
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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

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

// EmbedImages calls /embed_visual for each image path and returns the
// generated embeddings. Returns transport.ErrChannelUnavailable when no sidecar
// URL is configured.
func (a *imageEmbedderAdapter) EmbedImages(ctx context.Context, imagePaths []string) ([][]float32, error) {
	if a.serverURL == "" {
		return nil, &transport.ErrChannelUnavailable{Channel: "visual"}
	}

	out := make([][]float32, 0, len(imagePaths))
	for _, path := range imagePaths {
		vec, err := a.embedSingle(ctx, path)
		if err != nil {
			return out, err
		}
		out = append(out, vec)
	}
	return out, nil
}

func (a *imageEmbedderAdapter) embedSingle(ctx context.Context, imagePath string) ([]float32, error) {
	// QDRANT-003: call /embed_visual_from_image which accepts {"image_path": "..."}
	// and uses SigLIP image encoder (768d).
	// QDRANT-001: validate canonical sidecar envelope (model, model_version,
	// dimensions) — reject incomplete or inconsistent responses.
	payload, err := json.Marshal(map[string]string{"image_path": imagePath})
	if err != nil {
		return nil, fmt.Errorf("marshal visual embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.serverURL+"/embed_visual_from_image", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create visual embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("visual embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotImplemented {
		// 501 = SigLIP model not loaded — not an error, channel unavailable.
		return nil, &transport.ErrChannelUnavailable{Channel: "visual"}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("visual embed HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Embedding    []float64 `json:"embedding"`
		Dimensions   int       `json:"dimensions"`
		Model        string    `json:"model"`
		ModelVersion string    `json:"model_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode visual embed response: %w", err)
	}

	// QDRANT-001: validate canonical sidecar envelope.
	if parsed.Model == "" {
		return nil, fmt.Errorf("visual embed: missing 'model' in sidecar response")
	}
	if parsed.ModelVersion == "" {
		return nil, fmt.Errorf("visual embed: missing 'model_version' in sidecar response")
	}
	if parsed.Dimensions <= 0 {
		return nil, fmt.Errorf("visual embed: missing or invalid 'dimensions' in sidecar response")
	}
	if parsed.Dimensions != len(parsed.Embedding) {
		return nil, fmt.Errorf("visual embed: dimensions=%d but embedding length=%d", parsed.Dimensions, len(parsed.Embedding))
	}

	// QDRANT-001: validate model identity against the schema.IndexSchema manifest.
	// Skip when schema is nil (test-only path).
	if a.schema != nil {
		if spec := a.schema.GetDense("visual"); spec != nil {
			if !modelNameMatches(parsed.Model, spec.Model) {
				return nil, fmt.Errorf("visual embed: model identity mismatch: sidecar returned %q, schema expects %q",
					parsed.Model, spec.Model)
			}
			if parsed.Dimensions != spec.Dimensions {
				return nil, fmt.Errorf("visual embed: dimension mismatch: sidecar returned %d, schema expects %d",
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

// ── AudioEmbedder adapter ─────────────────────────────────────────────

// YAGNI (July 2026): AudioEmbedder interface is commented out
// (searcher.go). Uncomment when audio embedding is wired.
// type AudioEmbedderConfig struct {
// 	// ServerURL is the base URL of the Python embedding sidecar.
// 	ServerURL string
// 	// Timeout is the HTTP client timeout. Zero means 30s.
// 	Timeout time.Duration
// }

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

// NewAudioEmbedderAdapter creates an AudioEmbedder pointing at a sidecar.
// Pass serverURL="" to signal that audio embeddings are unavailable.
// schema may be nil in tests where schema.IndexSchema validation is not needed.
// YAGNI (July 2026): AudioEmbedder interface is commented out
// (searcher.go). Uncomment when audio embedding is wired.
// func NewAudioEmbedderAdapter(cfg AudioEmbedderConfig, schema *schema.IndexSchema, log *zap.Logger) AudioEmbedder {
// 	timeout := cfg.Timeout
// 	if timeout <= 0 {
// 		timeout = 30 * time.Second
// 	}
// 	return &audioEmbedderAdapter{
// 		serverURL:  cfg.ServerURL,
// 		httpClient: &http.Client{Timeout: timeout},
// 		log:        log,
// 		schema:     schema,
// 	}
// }

// Compile-time assertion.
// YAGNI (July 2026): AudioEmbedder interface is commented out
// (searcher.go). Uncomment when audio embedding is wired.
// var _ AudioEmbedder = (*audioEmbedderAdapter)(nil)

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
