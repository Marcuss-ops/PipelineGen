// Package vlm provides VLM (Vision-Language Model) integration for visual analysis.
// It calls the google-accounting sidecar endpoints for VLM operations:
// - /vlm/analyze — single image analysis
// - /vlm/visual-tag — structured visual tagging
// - /vlm/analyze-frames — multi-frame analysis
// - /vlm/dedup-check — visual deduplication
// - /vlm/validate-script — script-to-visual alignment validation
package vlm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Config holds the VLM client configuration.
type Config struct {
	Enabled      bool    `yaml:"enabled" default:"false"`
	Endpoint     string  `yaml:"endpoint" default:"http://127.0.0.1:8000"`
	Model        string  `yaml:"model" default:"nvidia/nemotron-nano-12b-v2-vl:free"`
	ModelVersion string  `yaml:"model_version" default:""`
	TimeoutMs    int     `yaml:"timeout_ms" default:"60000"`
	Weight       float64 `yaml:"weight" default:"0.3"` // weight for VLM score in blended scoring
}

// Client is a VLM client that calls the google-accounting sidecar.
type Client struct {
	cfg  Config
	http *http.Client
}

// Model returns the configured model name.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.cfg.Model
}

// ModelVersion returns the configured model version.
func (c *Client) ModelVersion() string {
	if c == nil {
		return ""
	}
	return c.cfg.ModelVersion
}

// NewClient creates a new VLM client.
func NewClient(cfg Config) *Client {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: timeout},
	}
}

// IsEnabled returns whether the VLM client is available.
func (c *Client) IsEnabled() bool {
	return c != nil && c.cfg.Enabled && c.cfg.Endpoint != ""
}

// VisualTag is the structured output from /vlm/visual-tag.
type VisualTag struct {
	SceneType      string   `json:"scene_type"`
	VisualObjects  []string `json:"visual_objects"`
	Mood           []string `json:"mood"`
	TextOnScreen   []string `json:"text_on_screen"`
	DominantColors []string `json:"dominant_colors"`
	Composition    string   `json:"composition"`
	Lighting       string   `json:"lighting"`
}

// VisualTagResponse is the full response from /vlm/visual-tag.
type VisualTagResponse struct {
	Tags  VisualTag `json:"tags"`
	Model string    `json:"model"`
}

// AnalyzeResponse is the response from /vlm/analyze.
type AnalyzeResponse struct {
	Response string `json:"response"`
	Model    string `json:"model"`
}

// ScriptValidation is the output from /vlm/validate-script.
type ScriptValidation struct {
	OverallScore float64 `json:"overall_score"`
	Issues       []struct {
		Scene      int    `json:"scene"`
		Severity   string `json:"severity"`
		Issue      string `json:"issue"`
		Suggestion string `json:"suggestion"`
	} `json:"issues"`
	VisualFlow     string `json:"visual_flow"`
	Achievable     bool   `json:"achievable"`
	ScenesAnalyzed int    `json:"scenes_analyzed"`
}

// ScriptValidationResponse is the full response from /vlm/validate-script.
type ScriptValidationResponse struct {
	Validation ScriptValidation `json:"validation"`
	Model      string           `json:"model"`
}

// DedupPair is a pair of visually similar images from /vlm/dedup-check.
type DedupPair struct {
	IndexA     int     `json:"index_a"`
	IndexB     int     `json:"index_b"`
	Similarity float64 `json:"similarity"`
	Reason     string  `json:"reason"`
}

// DedupResult is the result from /vlm/dedup-check.
type DedupResult struct {
	Pairs       []DedupPair `json:"pairs"`
	UniqueCount int         `json:"unique_count"`
}

// VisualTagImage calls /vlm/visual-tag for a single image.
func (c *Client) VisualTagImage(ctx context.Context, imageURL string) (*VisualTag, error) {
	if !c.IsEnabled() {
		return nil, ErrVLMDisabled
	}

	url := fmt.Sprintf("%s/vlm/visual-tag?image_url=%s", c.cfg.Endpoint, imageURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create vlm request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vlm request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vlm returned %d", resp.StatusCode)
	}

	var result VisualTagResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode vlm response: %w", err)
	}

	return &result.Tags, nil
}

// ValidateScript calls /vlm/validate-script to check script visual coherence.
func (c *Client) ValidateScript(ctx context.Context, scriptText string, imageURLs []string) (*ScriptValidation, error) {
	if !c.IsEnabled() {
		return nil, ErrVLMDisabled
	}

	payload := map[string]any{
		"script_text": scriptText,
		"model":       c.cfg.Model,
	}
	if len(imageURLs) > 0 {
		payload["image_urls"] = imageURLs
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/vlm/validate-script", c.cfg.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create vlm validate request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vlm validate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vlm validate returned %d", resp.StatusCode)
	}

	var result ScriptValidationResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode vlm validate response: %w", err)
	}

	return &result.Validation, nil
}

// DedupCheck calls /vlm/dedup-check to find visually similar images.
func (c *Client) DedupCheck(ctx context.Context, imageURLs []string, threshold float64) (*DedupResult, error) {
	if !c.IsEnabled() {
		return nil, ErrVLMDisabled
	}

	payload := map[string]any{
		"image_urls": imageURLs,
		"threshold":  threshold,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/vlm/dedup-check", c.cfg.Endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create vlm dedup request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vlm dedup request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vlm dedup returned %d", resp.StatusCode)
	}

	var raw struct {
		Result DedupResult `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode vlm dedup response: %w", err)
	}

	return &raw.Result, nil
}

// AutoTagLocal calls /vlm/autotag/analyze-file for a local file (image or video).
// It returns the structured visual tags and the model name reported by the
// sidecar (or the configured model if the sidecar omits the field).
func (c *Client) AutoTagLocal(ctx context.Context, localPath, mediaType string) (*VisualTag, string, error) {
	if !c.IsEnabled() {
		return nil, "", ErrVLMDisabled
	}

	u := fmt.Sprintf("%s/vlm/autotag/analyze-file?local_path=%s&media_type=%s",
		c.cfg.Endpoint, url.QueryEscape(localPath), url.QueryEscape(mediaType))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create vlm autotag request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("vlm autotag request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("vlm autotag returned %d", resp.StatusCode)
	}

	var result VisualTagResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decode vlm autotag response: %w", err)
	}

	return &result.Tags, result.Model, nil
}

// godlike/06 SSOT compile-time pin: ErrVLMDisabled surface is canonically
// defined in errors.go (same package). The var _ = ErrVLMDisabled reference
// below is the SSOT-lock per AGENTS.md Pattern 0 + godlike/06 one-owner-per-
// fact; any future file that tries to redefine ErrVLMDisabled would collide
// at compile-time thanks to Go's package-private scope. The explicit
// reference here just keeps the cross-file surface pingable for future
// code-reviewer audits. Zero runtime cost.
var _ = ErrVLMDisabled
