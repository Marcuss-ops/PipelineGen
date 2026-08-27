package overlays

import (
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strings"
)

const (
	SchemaVersionPlan   = "renderinggen.overlay-plan.v1"
	SchemaVersionResult = "renderinggen.overlay-result.v1"
	JobTypePrepare      = "overlay.prepare"
	JobTypeRender       = "overlay.render"
	// OverlayStatusReady is the canonical render-worker certification state.
	// It is stamped only after the artifact has been rendered, probed,
	// contract-validated and hashed — never from the renderer's exit code
	// alone. Drive upload + persistence complete on the Sender side (the
	// manifest's drive_file_id/drive_link slots + the asset's
	// lifecycle_state=PUBLISHED); together they are the full "READY".
	OverlayStatusReady = "ready"
)

type OverlayPlan struct {
	SchemaVersion   string `json:"schema_version"`
	PlanID          string `json:"plan_id"`
	VideoID         string `json:"video_id"`
	ProjectID       string `json:"project_id,omitempty"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	FPSNum          int    `json:"fps_num"`
	FPSDen          int    `json:"fps_den"`
	RendererVersion string `json:"renderer_version,omitempty"`
	// MediaContract is the ID of the OverlayMediaContract the renderer must
	// honor (container/codec/pixel format, audio_streams==0, alpha policy).
	// Empty means "renderer default". When set, it MUST resolve through
	// ResolveMediaContract; the compiled chronon output derives its
	// container/codec/pixel format from the resolved contract.
	MediaContract string `json:"media_contract,omitempty"`
	// Background is an optional full-canvas layer rendered below every
	// semantic overlay. When omitted, the source video remains visible below
	// the overlays (the legacy behaviour).
	Background  *OverlayBackground `json:"background,omitempty"`
	Items       []OverlayItem      `json:"items"`
	Fingerprint string             `json:"fingerprint,omitempty"`
}

// OverlayBackground is the payload contract for an optional full-canvas
// background. Kind is "color", "image" or "video". Image/video backgrounds
// use the same content-addressed asset refs as overlay items, so the worker
// materializes them without a second asset protocol.
type OverlayBackground struct {
	Kind      string            `json:"kind"`
	Color     []float64         `json:"color,omitempty"`
	AssetRefs []OverlayAssetRef `json:"asset_refs,omitempty"`
	Fit       string            `json:"fit,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	Loop      bool              `json:"loop,omitempty"`
	Style     map[string]any    `json:"style,omitempty"`
}

type OverlayItem struct {
	ID      string `json:"id"`
	SceneID string `json:"scene_id,omitempty"`
	// EntityID and Kind tag overlay items driven by an entity occurrence
	// (kind "entity_card"). They are optional: phrase/keyword/background
	// items never carry them, so the golden document shape is unchanged.
	EntityID string `json:"entity_id,omitempty"`
	Kind     string `json:"kind,omitempty"`
	StartMs  int64  `json:"start_ms"`
	EndMs    int64  `json:"end_ms"`
	// StartUS / DurationUS carry the canonical integer-microsecond timing
	// (start_us / duration_us) derived from the frozen CanonicalTimeline and
	// certified speech timing. They are the authoritative timing; StartMs /
	// EndMs are the millisecond projection (floor start, ceil end). A zero
	// DurationUS means the item was authored on the legacy millisecond path
	// (golden fixtures, planner tests), in which case the compiler falls back
	// to StartMs/EndMs.
	StartUS    int64 `json:"start_us,omitempty"`
	DurationUS int64 `json:"duration_us,omitempty"`
	// TemplateID is the semantic template (e.g. "person_default" for an
	// entity card, "IMPORTANT_PHRASE" for a phrase).
	TemplateID string `json:"template_id"`
	// PresetID is the semantic visual preset selected by PipelineGen for
	// this item (e.g. "phrase_focus_v1", "entity_card_v1"). It is the
	// contract slot of the plan's preset resolver: the value space is owned
	// by the preset registry, never invented here. Empty means "no preset
	// selected" — the item compiles through the semantic_role → Chronon
	// preset table as today. Because it is omitempty, plans authored without
	// a preset keep their exact fingerprint/render key.
	PresetID string `json:"preset_id,omitempty"`
	// EntityRef carries the content-addressed entity identity this item
	// renders (entity_id + type + canonical name + surface text). It is the
	// plan's entity_ref: the resolver always emits it for entity-driven
	// items so RenderingGen receives WHO the overlay is about — never a bare
	// name or URL. Omitempty: non-entity items keep the legacy shape.
	EntityRef *OverlayEntityRef `json:"entity_ref,omitempty"`
	Text      string            `json:"text,omitempty"`
	AssetRefs []OverlayAssetRef `json:"asset_refs,omitempty"`
	Params    map[string]any    `json:"params,omitempty"`
	RenderKey string            `json:"render_key,omitempty"`
}

// OverlayEntityRef is the content-addressed entity identity of an overlay
// item (the plan's entity_ref): the stable entity id, the canonical type,
// the canonical name and the surface text actually spoken. It is pure
// identity metadata — the visual rendering is driven by TemplateID/PresetID/
// Text/AssetRefs, never by this ref.
type OverlayEntityRef struct {
	EntityID string `json:"entity_id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	// SurfaceText is the verbatim mention the voiceover actually spoke
	// (may differ from the canonical name, e.g. "Cook" vs "Tim Cook").
	SurfaceText string `json:"surface_text,omitempty"`
	// CanonicalEntityID is the stable canonical identity of the entity in
	// the entities-package spelling (e.g. "person:floyd-mayweather") — the
	// join key the media index / EntityMediaResolver used to select the
	// item's asset. RenderingGen receives WHO the overlay is about under the
	// same id the resolver chose, never a re-derived spelling.
	CanonicalEntityID string `json:"canonical_entity_id,omitempty"`
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
	FPSNum          int    `json:"fps_num"`
	FPSDen          int    `json:"fps_den"`
	DurationMs      int64  `json:"duration_ms"`
	HasAlpha        bool   `json:"has_alpha"`
	RendererVersion string `json:"renderer_version"`
	// SceneID and TemplateID tag the overlay's semantic origin (which scene
	// and which resolved template) so the persisted artifact is traceable
	// back to its OverlayIntent.
	SceneID    string `json:"scene_id,omitempty"`
	TemplateID string `json:"template_id,omitempty"`
	// MediaContract is the ID of the OverlayMediaContract the artifact was
	// certified against. Container / Codec / PixelFormat / AudioStreams are
	// the probed facts from the canonical media probe that passed Validate().
	MediaContract string `json:"media_contract,omitempty"`
	Container     string `json:"container,omitempty"`
	Codec         string `json:"codec,omitempty"`
	PixelFormat   string `json:"pixel_format,omitempty"`
	AudioStreams  int    `json:"audio_streams"`
	// Status is the canonical render-worker certification state
	// (OverlayStatusReady). It is only ever OverlayStatusReady because the
	// handler returns before building a RenderResult when probe or contract
	// validation fails.
	Status string `json:"status"`
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

// StartUSValue returns the item's start in integer microseconds (canonical
// timing), falling back to the millisecond projection for legacy items.
func (i OverlayItem) StartUSValue() int64 {
	if i.DurationUS > 0 {
		return i.StartUS
	}
	return i.StartMs * 1000
}

// EndUSValue returns the item's end in integer microseconds (canonical
// timing), falling back to the millisecond projection for legacy items.
func (i OverlayItem) EndUSValue() int64 {
	if i.DurationUS > 0 {
		return i.StartUS + i.DurationUS
	}
	return i.EndMs * 1000
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
	if p.Width <= 0 || p.Height <= 0 || p.FPSNum <= 0 || p.FPSDen <= 0 {
		return fmt.Errorf("overlay plan: width, height and frame rate must be positive")
	}
	if strings.TrimSpace(p.MediaContract) != "" {
		if _, err := ResolveMediaContract(p.MediaContract); err != nil {
			return fmt.Errorf("overlay plan: %w", err)
		}
	}
	if bg := p.Background; bg != nil {
		switch strings.ToLower(strings.TrimSpace(bg.Kind)) {
		case "color":
			if len(bg.Color) != 4 {
				return fmt.Errorf("overlay plan: background color requires RGBA[4]")
			}
			for _, component := range bg.Color {
				if component < 0 || component > 1 {
					return fmt.Errorf("overlay plan: background color components must be in [0,1]")
				}
			}
			if len(bg.AssetRefs) != 0 {
				return fmt.Errorf("overlay plan: color background cannot carry asset_refs")
			}
		case "image", "video":
			if len(bg.AssetRefs) == 0 || (strings.TrimSpace(bg.AssetRefs[0].URL) == "" && strings.TrimSpace(bg.AssetRefs[0].SHA256) == "") {
				return fmt.Errorf("overlay plan: %s background requires a resolvable asset", bg.Kind)
			}
		default:
			return fmt.Errorf("overlay plan: unsupported background kind %q", bg.Kind)
		}
		for index, ref := range bg.AssetRefs {
			if strings.TrimSpace(ref.AssetID) == "" {
				return fmt.Errorf("overlay plan: background asset[%d] requires asset_id", index)
			}
		}
	}
	seenIDs := make(map[string]struct{}, len(p.Items))
	for i := range p.Items {
		item := p.Items[i]
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.TemplateID) == "" {
			return fmt.Errorf("overlay plan: item[%d] requires id and template_id", i)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return fmt.Errorf("overlay plan: duplicate item id %q", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
		if item.StartMs < 0 || item.EndMs <= item.StartMs {
			return fmt.Errorf("overlay plan: item %q has invalid time range", item.ID)
		}
		// Integer-microsecond timing, when present, must be internally valid
		// and consistent with the millisecond projection (floor start, ceil
		// end) so the two representations never drift.
		if item.DurationUS < 0 || item.StartUS < 0 {
			return fmt.Errorf("overlay plan: item %q has invalid microsecond timing", item.ID)
		}
		if item.DurationUS > 0 {
			if wantStart := item.StartUS / 1000; item.StartMs != wantStart {
				return fmt.Errorf("overlay plan: item %q start_ms %d diverges from start_us %d", item.ID, item.StartMs, item.StartUS)
			}
			if wantEnd := (item.StartUS + item.DurationUS + 999) / 1000; item.EndMs != wantEnd {
				return fmt.Errorf("overlay plan: item %q end_ms %d diverges from start_us+duration_us %d", item.ID, item.EndMs, item.StartUS+item.DurationUS)
			}
		}
		// Preset contract: a preset_id is an opaque id resolved by the preset
		// registry downstream — never validated against a local table here. It
		// must only be non-empty when it carries a real value.
		if strings.TrimSpace(item.PresetID) != item.PresetID {
			return fmt.Errorf("overlay plan: item %q preset_id must not be blank-padded", item.ID)
		}
		if ref := item.EntityRef; ref != nil {
			if strings.TrimSpace(ref.EntityID) == "" || strings.TrimSpace(ref.Type) == "" || strings.TrimSpace(ref.Name) == "" {
				return fmt.Errorf("overlay plan: item %q entity_ref requires entity_id, type and name", item.ID)
			}
		}
		for assetIndex, ref := range item.AssetRefs {
			if strings.TrimSpace(ref.AssetID) == "" {
				return fmt.Errorf("overlay plan: item %q asset[%d] requires asset_id", item.ID, assetIndex)
			}
		}
		if item.RenderKey == "" {
			key := ComputeRenderKey(*p, item)
			p.Items[i] = OverlayItem{ID: item.ID, SceneID: item.SceneID, EntityID: item.EntityID, Kind: item.Kind, StartMs: item.StartMs, EndMs: item.EndMs, StartUS: item.StartUS, DurationUS: item.DurationUS, TemplateID: item.TemplateID, PresetID: item.PresetID, EntityRef: item.EntityRef, Text: item.Text, AssetRefs: item.AssetRefs, Params: item.Params, RenderKey: key}
		}
	}
	if p.Fingerprint == "" {
		p.Fingerprint = p.FingerprintValue()
	} else if p.Fingerprint != p.FingerprintValue() {
		return fmt.Errorf("overlay plan: fingerprint does not match plan contents")
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
	h := digest.SHA256Bytes(b)
	return h
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
		Width, Height, FPSNum, FPSDen    int
		StartMs, EndMs                   int64
		StartUS, DurationUS              int64
		PresetID                         string `json:"preset_id,omitempty"`
	}{
		item.TemplateID, item.Text, string(params), renderer, assetHashes, p.Width, p.Height, p.FPSNum, p.FPSDen, item.StartMs, item.EndMs, item.StartUS, item.DurationUS,
		item.PresetID,
	}
	b, _ := json.Marshal(input)
	h := digest.SHA256Bytes(b)
	return h
}

// RenderKey is kept as the concise public spelling used by planners.
func RenderKey(p OverlayPlan, item OverlayItem) string { return ComputeRenderKey(p, item) }
