// Package transport — HTTP handler for the /models endpoint (Task 10, July 2026).
//
// ModelsHandler probes the Python embedding sidecar to verify that the
// canonical embedding models are loaded, produce correct dimensions, and
// are capable of inference. The two canonical models are:
//
//   - E5 text model via /embed
//   - SigLIP visual model via /embed_visual
//
// Each probe sends a short health-check text to the sidecar and validates:
//   - HTTP 200 response
//   - Non-empty embedding vector
//   - Correct dimension from the canonical model registry (768 for both E5 and SigLIP)
//   - No sidecar error
//
// The sidecar URL is configured via ServerDeps.ModelsSidecarURL (defaults
// to the clipindexer ServerURL). When empty, /models returns {"ok":false}
// with "models sidecar not configured".
//
// Contract: GET /models → JSON array of per-model probe results.
// This runs at request time (not cached) so operators see the live state
// of the Python sidecar.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// ModelsHandler exposes the /models endpoint.
type ModelsHandler struct {
	sidecarURL string
	httpClient *http.Client
}

// NewModelsHandler constructs the handler. sidecarURL is the Python
// embedding server URL (e.g. "http://127.0.0.1:8001"). Empty URL means
// the sidecar is not configured — probe returns applicable=false.
func NewModelsHandler(sidecarURL string) *ModelsHandler {
	return &ModelsHandler{
		sidecarURL: sidecarURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// ModelProbeResult is a single model's health probe result.
type modelProbeResult struct {
	Model      string `json:"model"`
	OK         bool   `json:"ok"`
	Dimensions int    `json:"dimensions,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// modelsResponse is the top-level /models payload.
type modelsResponse struct {
	OK     bool               `json:"ok"`
	Models []modelProbeResult `json:"models"`
}

// Models serves GET /models.
//
// Probes the Python embedding sidecar for E5 (text) and SigLIP (visual)
// model health. Each probe is independent — one model failing does not
// block the other. Returns HTTP 200 even when a model fails (the per-model
// ok field carries the failure; operators read the JSON, not the status code).
func (h *ModelsHandler) Models(c *gin.Context) {
	if h.sidecarURL == "" {
		c.JSON(http.StatusOK, modelsResponse{
			OK: false,
			Models: []modelProbeResult{
				{Model: models.E5.ID, OK: false, Error: "models sidecar not configured"},
				{Model: models.SigLIP.ID, OK: false, Error: "models sidecar not configured"},
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	e5Result := h.probeE5(ctx)
	siglipResult := h.probeSigLIP(ctx)

	allOK := e5Result.OK && siglipResult.OK
	c.JSON(http.StatusOK, modelsResponse{
		OK:     allOK,
		Models: []modelProbeResult{e5Result, siglipResult},
	})
}

// probeModel is the shared probe logic used by both E5 and SigLIP checks.
// endpoint is the sidecar path (e.g. "/embed", "/embed_visual_from_text").
// payload is the JSON body to POST. SigLIP-specific: HTTP 501 (NotImplemented)
// is treated as "model not loaded" rather than a generic HTTP error.
func (h *ModelsHandler) probeModel(ctx context.Context, endpoint, modelName, expectedRevision string, expectedDim int, payload map[string]string, siglip501 bool) modelProbeResult {
	result := modelProbeResult{Model: modelName, Dimensions: expectedDim}
	started := time.Now()

	body, err := json.Marshal(payload)
	if err != nil {
		result.OK = false
		result.Error = fmt.Sprintf("marshal probe payload: %v", err)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	resp, err := h.httpClient.Post(
		h.sidecarURL+endpoint,
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		result.OK = false
		result.Error = fmt.Sprintf("HTTP probe failed: %v", err)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if siglip501 && resp.StatusCode == http.StatusNotImplemented {
		result.OK = false
		result.Error = "siglip model not loaded (HTTP 501)"
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}
	if resp.StatusCode != http.StatusOK {
		result.OK = false
		result.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	var envelope struct {
		Embedding    []float64 `json:"embedding"`
		Dimensions   int       `json:"dimensions"`
		Model        string    `json:"model"`
		ModelVersion string    `json:"model_version"`
		Error        string    `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		result.OK = false
		result.Error = fmt.Sprintf("decode response: %v", err)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	if envelope.Error != "" {
		result.OK = false
		result.Error = fmt.Sprintf("sidecar error: %s", envelope.Error)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	if len(envelope.Embedding) == 0 {
		result.OK = false
		result.Error = "empty embedding vector"
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	if envelope.Model != modelName || envelope.ModelVersion != expectedRevision {
		result.OK = false
		result.Error = fmt.Sprintf("model identity mismatch: got %q revision %q, want %q revision %q", envelope.Model, envelope.ModelVersion, modelName, expectedRevision)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	if len(envelope.Embedding) != expectedDim {
		result.OK = false
		result.Dimensions = len(envelope.Embedding)
		result.Error = fmt.Sprintf("dimension mismatch: got %d, want %d", len(envelope.Embedding), expectedDim)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	if envelope.Dimensions > 0 && envelope.Dimensions != expectedDim {
		result.OK = false
		result.Dimensions = envelope.Dimensions
		result.Error = fmt.Sprintf("declared dimension mismatch: declared %d, want %d", envelope.Dimensions, expectedDim)
		result.DurationMs = time.Since(started).Milliseconds()
		return result
	}

	result.OK = true
	result.Dimensions = expectedDim
	result.DurationMs = time.Since(started).Milliseconds()
	return result
}

// probeE5 sends a health-check text to the sidecar's /embed endpoint
// and validates the E5 (multilingual-e5-base) model response.
func (h *ModelsHandler) probeE5(ctx context.Context) modelProbeResult {
	return h.probeModel(ctx, "/embed", models.E5.ID, models.E5.Revision, models.E5.Dimensions,
		map[string]string{"text": "__health_check__", "type": "query"},
		false)
}

// probeSigLIP sends a health-check text to the sidecar's
// /embed_visual endpoint and validates the SigLIP
// (siglip-so400m-patch14-384) model response.
func (h *ModelsHandler) probeSigLIP(ctx context.Context) modelProbeResult {
	return h.probeModel(ctx, "/embed_visual", models.SigLIP.ID, models.SigLIP.Revision, models.SigLIP.Dimensions,
		map[string]string{"text": "__health_check__", "model": models.SigLIP.ID},
		true)
}
