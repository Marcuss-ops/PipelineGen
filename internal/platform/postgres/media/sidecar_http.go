// Package media — sidecar_http.go: the narrow HTTP + decode helpers for
// the sidecar-backed enrichment ports (visual embedder, face detector).
// Kept in ONE file so the media package stays leaf-only (no imports of
// qdrant/indexing packages) and the sidecar contract has one owner.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sidecarResponse is one raw HTTP response.
type sidecarResponse struct {
	status int
	body   []byte
}

// postJSON performs the canonical sidecar POST (JSON in, JSON out).
func postJSON(ctx context.Context, client *http.Client, url string, payload any) (*sidecarResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sidecar marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("sidecar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar transport: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("sidecar read: %w", err)
	}
	return &sidecarResponse{status: resp.StatusCode, body: body}, nil
}

// visualBatchEnvelope is the canonical sidecar batch response
// (QDRANT-001 envelope parity: model, model_version, dimensions, count).
type visualBatchEnvelope struct {
	Embeddings   [][]float64 `json:"embeddings"`
	Dimensions   int         `json:"dimensions"`
	Count        int         `json:"count"`
	Model        string      `json:"model"`
	ModelVersion string      `json:"model_version"`
}

// decodeVisualBatch validates the sidecar envelope against the registry
// spec and converts the embeddings to float32 slices (order-preserved).
func decodeVisualBatch(body []byte, spec VisualModelSpec) ([][]float32, error) {
	var parsed visualBatchEnvelope
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("visual embedder: decode: %w", err)
	}
	if parsed.Model == "" {
		return nil, fmt.Errorf("visual embedder: missing 'model' in sidecar response")
	}
	if parsed.Dimensions <= 0 {
		return nil, fmt.Errorf("visual embedder: missing or invalid 'dimensions' in sidecar response")
	}
	if parsed.Count != len(parsed.Embeddings) {
		return nil, fmt.Errorf("visual embedder: count=%d but len(embeddings)=%d", parsed.Count, len(parsed.Embeddings))
	}
	if !visualModelNameMatches(parsed.Model, spec.ModelID) {
		return nil, fmt.Errorf("%w: sidecar returned %q, registry pins %q", ErrVisualModelIdentityMismatch, parsed.Model, spec.ModelID)
	}
	if parsed.Dimensions != spec.Dim {
		return nil, fmt.Errorf("%w: sidecar returned %dd, registry pins %dd", ErrVisualDimMismatch, parsed.Dimensions, spec.Dim)
	}
	out := make([][]float32, 0, len(parsed.Embeddings))
	for i, vec := range parsed.Embeddings {
		if len(vec) != spec.Dim {
			return nil, fmt.Errorf("%w: embeddings[%d] is %dd", ErrVisualDimMismatch, i, len(vec))
		}
		v32 := make([]float32, len(vec))
		for j, v := range vec {
			v32[j] = float32(v)
		}
		out = append(out, v32)
	}
	if len(out) == 0 {
		return nil, ErrVisualEmptyResponse
	}
	return out, nil
}

// visualModelNameMatches compares a sidecar-returned model name against
// the registry identity, accepting an upstream vendor prefix on either
// side (e.g. "google/siglip-so400m-patch14-384" vs
// "siglip-so400m-patch14-384").
func visualModelNameMatches(sidecarModel, registryModel string) bool {
	return modelBaseNameOf(sidecarModel) == modelBaseNameOf(registryModel)
}

func modelBaseNameOf(modelID string) string {
	for i := len(modelID) - 1; i >= 0; i-- {
		if modelID[i] == '/' {
			return modelID[i+1:]
		}
	}
	return modelID
}

func truncateForLog(b []byte) string {
	const max = 4096
	if len(b) > max {
		return string(b[:max])
	}
	return string(b)
}
