// Package indexing — vlm_client.go owns the [Embed] layer of the
// VLM VisualSummary pipeline: HTTP transport to the canonical
// Python /vlm/visual-tag sidecar and the response envelope DTO.
//
// Split rationale (cx0030 build / cx0031 embed / cx0032 render):
//   - frame_sampler.go  : [Build]   — FFmpeg frame extraction.
//   - vlm_client.go     : [Embed]   — THIS FILE. VLMClient port
//   - HTTPVLMClient concrete + the
//     VLMInferenceResponse envelope.
//   - vlm_aggregator.go : [Render]  — deterministic dedup + cap.
//   - visual_summary.go : Orchestrator.
//
// godlike/07 transport error taxonomy: the VLM client uses its own
// sentinel strings (status + body preview in the wrap); the
// qdrant-side transport.ErrAliasSwitchNotReady / etc. taxonomy is
// reserved for the canonical qdrant transport surface.
package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VLMClient is the port for the Python /vlm/visual-tag endpoint.
// Decouples the service from any specific HTTP/gRPC transport.
type VLMClient interface {
	Infer(ctx context.Context, imagePath string) (*VLMInferenceResponse, error)
}

// VLMInferenceResponse is the JSON envelope returned by the Python
// /vlm/visual-tag endpoint. It matches the response shape produced
// by scripts/bridges/semantic_tagger/vlm.py::call_vlm_visual in
// production deployments.
//
// Field meaning (per the canonical Python bridge):
//   - SceneType: best-fit scene classification
//     (e.g. "boxing_match", "outdoor_talk_show")
//   - VisualObjects: action verbs / objects visible in the frame
//   - Mood: emotional tags (e.g. "intense", "calm")
//   - TextOnScreen: OCR-extracted text strings
//   - Composition / Lighting: framing metadata
//   - RawDescription: the longest-form narrative caption from the VLM
type VLMInferenceResponse struct {
	SceneType      string   `json:"scene_type"`
	VisualObjects  []string `json:"visual_objects"`
	Mood           []string `json:"mood"`
	TextOnScreen   []string `json:"text_on_screen"`
	Composition    string   `json:"composition"`
	Lighting       string   `json:"lighting"`
	RawDescription string   `json:"raw_description"`
}

// HTTPVLMClient is the production concrete that POSTs to the
// Python /vlm/visual-tag endpoint at the canonical DefaultVLMHTTP
// Endpoint (or operator-overridden URL).
//
// godlike/07: HTTP errors surface wrapped with status code + body
// preview; non-200 surfaces typed error; partial success does not
// exist (the Python sidecar either returns a complete response or
// an error envelope).
type HTTPVLMClient struct {
	endpoint string
	timeout  time.Duration
	http     *http.Client
}

// NewHTTPVLMClient wires the client. endpoint="" → uses
// DefaultVLMHTTPEndpoint; timeout <= 0 → uses
// DefaultVLMHTTPTimeoutSeconds.
func NewHTTPVLMClient(endpoint string, timeout time.Duration) *HTTPVLMClient {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultVLMHTTPEndpoint
	}
	if timeout <= 0 {
		timeout = DefaultVLMHTTPTimeoutSeconds * time.Second
	}
	return &HTTPVLMClient{
		endpoint: endpoint,
		timeout:  timeout,
		http:     &http.Client{Timeout: timeout},
	}
}

// Infer POSTs to {endpoint}/vlm/visual-tag with a JSON body
// {"image_path": "..."} and decodes the canonical response
// envelope.
//
// godlike/07: HTTP timeout is per-request; non-2xx status surfaces
// typed error; JSON decode failure surfaces typed error. The
// underlying transport.ErrAliasSwitchNotReady / etc. error taxonomy
// is NOT used here (transport errors reserved for the canonical
// qdrant transport surface); the VLM client uses sentinel strings.
func (c *HTTPVLMClient) Infer(ctx context.Context, imagePath string) (*VLMInferenceResponse, error) {
	if strings.TrimSpace(imagePath) == "" {
		return nil, ErrVLMJobConfigLocalPathRequired
	}
	body, _ := json.Marshal(map[string]string{"image_path": imagePath})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/vlm/visual-tag", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vlm_client.NewRequest(endpoint=%q): %w", c.endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vlm_client.Do(endpoint=%q): %w", c.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf(
			"vlm_client: non-2xx status=%d body=%q endpoint=%q",
			resp.StatusCode, string(b), c.endpoint)
	}
	var out VLMInferenceResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("vlm_client.Decode(endpoint=%q): %w", c.endpoint, err)
	}
	return &out, nil
}
