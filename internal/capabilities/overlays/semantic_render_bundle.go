package overlays

import (
	"encoding/hex"
	"fmt"
	"regexp"
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
	EntityID string `json:"entity_id"`
	// OccurrenceID is scene-scoped identity for a repeated canonical entity.
	// Empty preserves the legacy one-card-per-canonical-entity projection.
	OccurrenceID  string  `json:"occurrence_id,omitempty"`
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
	EntityID     string `json:"entity_id"`
	OccurrenceID string `json:"occurrence_id,omitempty"`
	AssetID      string `json:"asset_id"`
	ContentHash  string `json:"content_hash"`
	LocalPath    string `json:"local_path,omitempty"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	Verified     bool   `json:"verified"`
}

type TimelineEvent struct {
	EntityID     string `json:"entity_id"`
	OccurrenceID string `json:"occurrence_id,omitempty"`
	StartMs      int64  `json:"start_ms"`
	EndMs        int64  `json:"end_ms"`
	PresetID     string `json:"preset_id"`
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
		entities[semanticOccurrenceKey(e.EntityID, e.OccurrenceID)] = e
	}
	for _, a := range b.Assets {
		if a.EntityID == "" || a.AssetID == "" || !contentHashPattern.MatchString(strings.ToLower(a.ContentHash)) || !a.Verified {
			return fmt.Errorf("semantic render bundle: asset %q is not verified/content-addressed", a.AssetID)
		}
		if _, err := hex.DecodeString(a.ContentHash); err != nil {
			return fmt.Errorf("semantic render bundle: asset %q has invalid content hash", a.AssetID)
		}
	}
	for _, ev := range b.Timeline {
		if _, ok := entities[semanticOccurrenceKey(ev.EntityID, ev.OccurrenceID)]; !ok || ev.StartMs < 0 || ev.EndMs <= ev.StartMs || ev.PresetID == "" {
			return fmt.Errorf("semantic render bundle: invalid timeline event for %q", ev.EntityID)
		}
	}
	// An asset that names an entity the bundle does not carry is an
	// unjoinable provenance record. Fail closed so downstream consumers cannot
	// silently downgrade the associated entity to text-only. A
	// verified, content-addressed asset must always join to a real bundle
	// entity. Asset-less text overlays remain valid (image selection is
	// explicit), but an asset with a dangling entity id is a contract break.
	for _, a := range b.Assets {
		if _, exists := entities[semanticOccurrenceKey(a.EntityID, a.OccurrenceID)]; !exists {
			return fmt.Errorf("semantic render bundle: asset %q joins entity %q which is not in the bundle", a.AssetID, a.EntityID)
		}
	}
	return nil
}

func semanticOccurrenceKey(entityID, occurrenceID string) string {
	if strings.TrimSpace(occurrenceID) != "" {
		return occurrenceID
	}
	return entityID
}
