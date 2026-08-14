package overlays

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	SchemaVersionPlan   = "renderinggen.overlay-plan.v1"
	SchemaVersionResult = "renderinggen.overlay-result.v1"
	JobTypePrepare      = "overlay.prepare"
	JobTypeRender       = "overlay.render"
)

type OverlayPlan struct {
	SchemaVersion   string        `json:"schema_version"`
	PlanID          string        `json:"plan_id"`
	VideoID         string        `json:"video_id"`
	ProjectID       string        `json:"project_id,omitempty"`
	Width           int           `json:"width"`
	Height          int           `json:"height"`
	FPS             int           `json:"fps"`
	RendererVersion string        `json:"renderer_version,omitempty"`
	Items           []OverlayItem `json:"items"`
	Fingerprint     string        `json:"fingerprint,omitempty"`
}

type OverlayItem struct {
	ID         string            `json:"id"`
	SceneID    string            `json:"scene_id,omitempty"`
	StartMs    int64             `json:"start_ms"`
	EndMs      int64             `json:"end_ms"`
	TemplateID string            `json:"template_id"`
	Text       string            `json:"text,omitempty"`
	AssetRefs  []OverlayAssetRef `json:"asset_refs,omitempty"`
	Params     map[string]any    `json:"params,omitempty"`
	RenderKey  string            `json:"render_key,omitempty"`
}

type OverlayAssetRef struct {
	AssetID   string `json:"asset_id"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
}

type RenderRequest struct {
	Plan        OverlayPlan `json:"plan"`
	OverlayID   string      `json:"overlay_id"`
	Speculative bool        `json:"speculative,omitempty"`
}

type RenderResult struct {
	SchemaVersion   string `json:"schema_version"`
	OverlayID       string `json:"overlay_id"`
	PlanID          string `json:"plan_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	RenderKey       string `json:"render_key"`
	ArtifactID      string `json:"artifact_id"`
	Filename        string `json:"filename"`
	LocalPath       string `json:"local_path,omitempty"`
	SHA256          string `json:"sha256"`
	SizeBytes       int64  `json:"size_bytes"`
	MIMEType        string `json:"mime_type"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	FPS             int    `json:"fps"`
	DurationMs      int64  `json:"duration_ms"`
	HasAlpha        bool   `json:"has_alpha"`
	RendererVersion string `json:"renderer_version"`
}

func ValidateResultForPlan(plan OverlayPlan, result RenderResult) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if result.SchemaVersion != SchemaVersionResult {
		return fmt.Errorf("overlay result: unsupported schema version %q", result.SchemaVersion)
	}
	if result.PlanID != plan.PlanID || result.PlanFingerprint != plan.Fingerprint {
		return fmt.Errorf("overlay result: stale plan fingerprint")
	}
	for _, item := range plan.Items {
		if item.ID == result.OverlayID && item.RenderKey == result.RenderKey {
			return nil
		}
	}
	return fmt.Errorf("overlay result: render key does not belong to current plan")
}

func (p *OverlayPlan) Validate() error {
	if p == nil {
		return fmt.Errorf("overlay plan: nil")
	}
	if p.SchemaVersion != SchemaVersionPlan {
		return fmt.Errorf("overlay plan: unsupported schema version %q", p.SchemaVersion)
	}
	if strings.TrimSpace(p.PlanID) == "" || strings.TrimSpace(p.VideoID) == "" {
		return fmt.Errorf("overlay plan: plan_id and video_id are required")
	}
	if p.Width <= 0 || p.Height <= 0 || p.FPS <= 0 {
		return fmt.Errorf("overlay plan: width, height and fps must be positive")
	}
	for i := range p.Items {
		item := p.Items[i]
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.TemplateID) == "" {
			return fmt.Errorf("overlay plan: item[%d] requires id and template_id", i)
		}
		if item.StartMs < 0 || item.EndMs <= item.StartMs {
			return fmt.Errorf("overlay plan: item %q has invalid time range", item.ID)
		}
		if item.RenderKey == "" {
			key := ComputeRenderKey(*p, item)
			p.Items[i] = OverlayItem{ID: item.ID, SceneID: item.SceneID, StartMs: item.StartMs, EndMs: item.EndMs, TemplateID: item.TemplateID, Text: item.Text, AssetRefs: item.AssetRefs, Params: item.Params, RenderKey: key}
		}
	}
	if p.Fingerprint == "" {
		p.Fingerprint = p.FingerprintValue()
	}
	return nil
}

func (p OverlayPlan) FingerprintValue() string {
	copyPlan := p
	copyPlan.Items = append([]OverlayItem(nil), p.Items...)
	copyPlan.Fingerprint = ""
	for i := range copyPlan.Items {
		copyPlan.Items[i].RenderKey = ""
	}
	b, _ := json.Marshal(copyPlan)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func ComputeRenderKey(p OverlayPlan, item OverlayItem) string {
	assetHashes := make([]string, 0, len(item.AssetRefs))
	for _, ref := range item.AssetRefs {
		assetHashes = append(assetHashes, strings.ToLower(strings.TrimSpace(ref.SHA256)))
	}
	sort.Strings(assetHashes)
	params, _ := json.Marshal(item.Params)
	renderer := p.RendererVersion
	if renderer == "" {
		renderer = "chronon"
	}
	input := struct {
		Template, Text, Params, Renderer string
		Assets                           []string
		Width, Height, FPS               int
		StartMs, EndMs                   int64
	}{
		item.TemplateID, item.Text, string(params), renderer, assetHashes, p.Width, p.Height, p.FPS, item.StartMs, item.EndMs,
	}
	b, _ := json.Marshal(input)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// RenderKey is kept as the concise public spelling used by planners.
func RenderKey(p OverlayPlan, item OverlayItem) string { return ComputeRenderKey(p, item) }
