package overlays

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

var contentHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// SemanticRenderBundleVersion is the single cross-stage contract version.
const SemanticRenderBundleVersion = "semantic-render-bundle.v1"

// SceneIR is the immutable source identity shared by every semantic stage.
type SceneIR struct {
	SegmentID      string                 `json:"segment_id"`
	Position       int                    `json:"position"`
	SourceText     string                 `json:"source_text"`
	SourceTextHash string                 `json:"source_text_hash"`
	NarrationText  string                 `json:"narration_text,omitempty"`
	Profile        SegmentSemanticProfile `json:"profile"`
}

type SegmentSemanticProfile struct {
	Subject     string   `json:"subject,omitempty"`
	VisualTerms []string `json:"visual_terms,omitempty"`
}

// ResolvedEntity is source-grounded typed entity identity. Text is never
// replaced by a normalized spelling in the evidence fields.
type ResolvedEntity struct {
	EntityID      string  `json:"entity_id"`
	Type          string  `json:"type"`
	Text          string  `json:"text"`
	CanonicalText string  `json:"canonical_text"`
	Evidence      string  `json:"evidence"`
	Start         int     `json:"start"`
	End           int     `json:"end"`
	Confidence    float64 `json:"confidence"`
	SceneID       string  `json:"scene_id"`
}

// BoundAsset is the only asset shape allowed to cross into rendering.
type BoundAsset struct {
	EntityID    string `json:"entity_id"`
	AssetID     string `json:"asset_id"`
	ContentHash string `json:"content_hash"`
	LocalPath   string `json:"local_path,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	Verified    bool   `json:"verified"`
}

type TimelineEvent struct {
	EntityID string `json:"entity_id"`
	StartMs  int64  `json:"start_ms"`
	EndMs    int64  `json:"end_ms"`
	PresetID string `json:"preset_id"`
}

type SemanticRenderBundleV1 struct {
	Version         string           `json:"version"`
	RunID           string           `json:"run_id"`
	Scene           SceneIR          `json:"scene"`
	Entities        []ResolvedEntity `json:"entities"`
	OverlayIntents  []OverlayIntent  `json:"overlay_intents"`
	Timeline        []TimelineEvent  `json:"timeline"`
	Assets          []BoundAsset     `json:"assets"`
	OverlayPlanHash string           `json:"overlay_plan_hash,omitempty"`
}

func NewSceneIR(segmentID string, position int, sourceText, narrationText string, profile SegmentSemanticProfile) SceneIR {
	return SceneIR{SegmentID: segmentID, Position: position, SourceText: sourceText,
		SourceTextHash: digest.SHA256String(sourceText), NarrationText: narrationText, Profile: profile}
}

func (s SceneIR) Validate() error {
	if strings.TrimSpace(s.SegmentID) == "" || s.Position < 0 || s.SourceText == "" {
		return fmt.Errorf("scene ir: missing immutable identity")
	}
	if s.SourceTextHash != digest.SHA256String(s.SourceText) {
		return fmt.Errorf("scene ir: source_text_hash does not match source_text")
	}
	return nil
}

func (b SemanticRenderBundleV1) Validate() error {
	if b.Version != SemanticRenderBundleVersion || strings.TrimSpace(b.RunID) == "" {
		return fmt.Errorf("semantic render bundle: invalid version or run_id")
	}
	if err := b.Scene.Validate(); err != nil {
		return err
	}
	entities := make(map[string]ResolvedEntity, len(b.Entities))
	for _, e := range b.Entities {
		if strings.TrimSpace(e.EntityID) == "" || strings.TrimSpace(e.Type) == "" || e.Start < 0 || e.End <= e.Start {
			return fmt.Errorf("semantic render bundle: invalid entity %q", e.EntityID)
		}
		if e.Evidence != e.Text || e.Start >= len(b.Scene.SourceText) || e.End > len(b.Scene.SourceText) || b.Scene.SourceText[e.Start:e.End] != e.Text {
			return fmt.Errorf("semantic render bundle: entity %q is not source-grounded", e.EntityID)
		}
		entities[e.EntityID] = e
	}
	assets := make(map[string]BoundAsset, len(b.Assets))
	for _, a := range b.Assets {
		if a.EntityID == "" || a.AssetID == "" || !contentHashPattern.MatchString(strings.ToLower(a.ContentHash)) || !a.Verified {
			return fmt.Errorf("semantic render bundle: asset %q is not verified/content-addressed", a.AssetID)
		}
		if _, err := hex.DecodeString(a.ContentHash); err != nil {
			return fmt.Errorf("semantic render bundle: asset %q has invalid content hash", a.AssetID)
		}
		assets[a.EntityID] = a
	}
	for _, ev := range b.Timeline {
		if _, ok := entities[ev.EntityID]; !ok || ev.StartMs < 0 || ev.EndMs <= ev.StartMs || ev.PresetID == "" {
			return fmt.Errorf("semantic render bundle: invalid timeline event for %q", ev.EntityID)
		}
	}
	_ = assets // asset-less text overlays are valid; image selection is explicit.
	return nil
}

// EntityTiming is the minimal timing input accepted by TimelinePlanner.
type EntityTiming struct {
	EntityID       string
	StartMs, EndMs int64
}

// TimelinePlanner creates deterministic bounded windows around certified word
// timings. It never invents an entity or lets an event leave the scene.
type TimelinePlanner struct{ LeadInMs, MinDurationMs, MaxDurationMs int64 }

func (p TimelinePlanner) Plan(sceneDurationMs int64, entities []ResolvedEntity, timings map[string]EntityTiming, presets map[string]string) ([]TimelineEvent, error) {
	lead, minDur, maxDur := p.LeadInMs, p.MinDurationMs, p.MaxDurationMs
	if lead <= 0 {
		lead = 250
	}
	if minDur <= 0 {
		minDur = 2500
	}
	if maxDur <= 0 {
		maxDur = 5000
	}
	if sceneDurationMs <= 0 || maxDur < minDur {
		return nil, fmt.Errorf("timeline planner: invalid duration policy")
	}
	out := make([]TimelineEvent, 0, len(entities))
	for _, e := range entities {
		t, ok := timings[e.EntityID]
		if !ok || t.EndMs <= t.StartMs {
			return nil, fmt.Errorf("timeline planner: missing timing for %q", e.EntityID)
		}
		start := t.StartMs - lead
		if start < 0 {
			start = 0
		}
		end := t.EndMs + lead
		if end-start < minDur {
			end = start + minDur
		}
		if end-start > maxDur {
			end = start + maxDur
		}
		if end > sceneDurationMs {
			end = sceneDurationMs
		}
		if end <= start {
			return nil, fmt.Errorf("timeline planner: entity %q has no room", e.EntityID)
		}
		preset := strings.TrimSpace(presets[e.EntityID])
		if preset == "" {
			return nil, fmt.Errorf("timeline planner: missing preset for %q", e.EntityID)
		}
		out = append(out, TimelineEvent{EntityID: e.EntityID, StartMs: start, EndMs: end, PresetID: preset})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].EntityID < out[j].EntityID
	})
	return out, nil
}

// BuildOverlayPlan is the sole bundle→overlay-plan projection. It uses the
// existing canonical template/preset registries and leaves Chronon lowering
// to CompileChrononPlan.
func BuildOverlayPlan(b SemanticRenderBundleV1, videoID, projectID string, width, height, fpsNum, fpsDen int) (OverlayPlan, error) {
	if err := b.Validate(); err != nil {
		return OverlayPlan{}, err
	}
	if width <= 0 {
		width = 1920
	}
	if height <= 0 {
		height = 1080
	}
	if fpsNum <= 0 {
		fpsNum = 24
	}
	if fpsDen <= 0 {
		fpsDen = 1
	}
	assets := make(map[string]BoundAsset, len(b.Assets))
	for _, a := range b.Assets {
		assets[a.EntityID] = a
	}
	events := make(map[string]TimelineEvent, len(b.Timeline))
	for _, e := range b.Timeline {
		events[e.EntityID] = e
	}
	items := make([]OverlayItem, 0, len(b.Entities))
	for _, e := range b.Entities {
		ev, ok := events[e.EntityID]
		if !ok {
			return OverlayPlan{}, fmt.Errorf("bundle: missing timeline for %q", e.EntityID)
		}
		templateID := "concept_default"
		kind := "entity_card"
		if e.Type == "PERSON" {
			templateID = "person_default"
		} else if e.Type == "LOCATION" {
			templateID = "gpe_default"
		} else if e.Type == "ORGANIZATION" {
			templateID = "org_default"
		}
		item := OverlayItem{ID: e.EntityID, SceneID: b.Scene.SegmentID, EntityID: e.EntityID, Kind: kind, StartMs: ev.StartMs, EndMs: ev.EndMs, TemplateID: templateID, PresetID: ev.PresetID, Text: e.Text,
			EntityRef: &OverlayEntityRef{EntityID: e.EntityID, Type: e.Type, Name: e.CanonicalText, SurfaceText: e.Text}}
		if a, ok := assets[e.EntityID]; ok {
			// An image is a capability choice, not merely an extra field on a
			// text card. The canonical image_popup template/preset owns the
			// geometry and keeps the entity asset on the direct-YUV path.
			item.Kind = string(KindEntityImage)
			item.TemplateID = "image_popup"
			item.PresetID = string(PresetModernImage)
			item.Text = ""
			item.AssetRefs = []OverlayAssetRef{{AssetID: a.AssetID, URL: a.SourceURL, SHA256: a.ContentHash, MediaType: "image/jpeg"}}
		}
		items = append(items, item)
	}
	plan := OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: b.RunID, VideoID: videoID, ProjectID: projectID, Width: width, Height: height, FPSNum: fpsNum, FPSDen: fpsDen, Items: items}
	if err := plan.Validate(); err != nil {
		return OverlayPlan{}, err
	}
	b.OverlayPlanHash = plan.Fingerprint
	return plan, nil
}
