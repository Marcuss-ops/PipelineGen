package model

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
	SchemaVersion string `json:"schema_version"`
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	ProjectID     string `json:"project_id,omitempty"`
	// EntityVariationSeed salts the worker's "random" entity-style sampling
	// (renderinggen.overlay-plan.v1 `entity_variation_seed`). Empty keeps the
	// historical plan-identity sampling; a per-render value resamples every
	// entity card. It is render content and participates in the fingerprint.
	EntityVariationSeed string `json:"entity_variation_seed,omitempty"`
	// ScriptName and Language identify freshly rendered overlay artifacts in
	// delivery metadata. The configured Drive root is selected by the
	// composition root; these fields never cause a caller-side upload.
	ScriptName string `json:"script_name,omitempty"`
	Language   string `json:"language,omitempty"`
	// DriveFolderID is the job-selected Drive parent for the overlay output.
	// It is routing metadata, not render content: the publisher creates the
	// deterministic overlay child below it and falls back to its configured
	// root only when this value is empty.
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	// DriveJobID is the canonical external generation-job identity used only
	// by PipelineGen's publication boundary. It is intentionally excluded from
	// the RenderingGen wire contract and from the semantic fingerprint: the
	// queue renders the plan content, while PipelineGen owns Drive routing.
	DriveJobID string `json:"-"`
	// ResultJobID is the broker job that owns the persisted job result.
	// It is carried only across PipelineGen's publication boundary.
	ResultJobID string `json:"-"`
	// RequireDriveBeforeReturn is an application-only delivery constraint for
	// final-job overlays: their Drive identities are embedded in PREPARE and
	// must be available before that request is sent. It is not render content.
	RequireDriveBeforeReturn bool `json:"-"`
	Width                    int  `json:"width"`
	Height                   int  `json:"height"`
	FPSNum                   int  `json:"fps_num"`
	FPSDen                   int  `json:"fps_den"`
	// DurationMS is the canonical master-audio/timeline duration projected
	// onto the overlay plan. It is a floor for the Chronon canvas duration:
	// overlay items may extend it, but they can never truncate the master
	// voiceover when the last semantic item ends earlier.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// ForegroundScalePercent scales the source video on the full canvas.
	// Zero/100 preserve the legacy full-canvas behaviour.
	ForegroundScalePercent int    `json:"foreground_scale_percent,omitempty"`
	RendererVersion        string `json:"renderer_version,omitempty"`
	// MediaContract is the ID of the OverlayMediaContract the renderer must
	// honor (container/codec/pixel format, audio_streams==0, alpha policy).
	// Empty means "renderer default". When set, it MUST resolve through
	// ResolveMediaContract; the compiled chronon output derives its
	// container/codec/pixel format from the resolved contract.
	MediaContract string `json:"media_contract,omitempty"`
	// Source is the concrete video layer for opaque final-job compositions.
	// LocalPath is producer-only; Path is the RenderingGen workspace path.
	Source *OverlaySource `json:"source,omitempty"`
	// Background is an optional full-canvas layer rendered below every
	// semantic overlay. When omitted, the source video remains visible below
	// the overlays (the legacy behaviour).
	Background        *OverlayBackground `json:"background,omitempty"`
	AnimationPolicies []AnimationPolicy  `json:"animation_policies,omitempty"`
	Items             []OverlayItem      `json:"items"`
	Fingerprint       string             `json:"fingerprint,omitempty"`
	// RecapPlacements counts the recap slots BuildPlan attempted; RecapCards
	// counts recap items surviving in Items after overlap degradation.
	// Application-only observability (like DriveJobID below): excluded from
	// the wire contract and the semantic fingerprint, so they never alter
	// render keys or cache identity.
	RecapPlacements int `json:"-"`
	RecapCards      int `json:"-"`
	// ChartPlacements counts the chart slots BuildPlan attempted;
	// ChartCards counts chart items surviving in Items after the emission
	// budget gate. Same exclusion rules as the counters above.
	ChartPlacements int `json:"-"`
	ChartCards      int `json:"-"`
	// TimedScenes counts the scenes that carried certified word timing at
	// compile time (the recap placement denominator). Same exclusion rules
	// as the counters above.
	TimedScenes int `json:"-"`
	// Intake explains the compiled plan against the run's requested budgets:
	// how many items each stage admitted and why the rest were dropped. It is
	// application-only observability (json:"-"), so it never enters the
	// renderinggen.overlay-plan.v1 wire contract or the semantic fingerprint.
	Intake *OverlayPlanIntake `json:"-"`
}

// OverlayPlanIntake is result telemetry: it records the requested run budgets,
// the candidates each producer supplied, the items that survived, and the
// per-reason drop counts between stages. It answers "why does a payload that
// asks for 100 overlays compile to fewer" without changing plan content.
type OverlayPlanIntake struct {
	Scenes        int `json:"scenes"`
	TimedScenes   int `json:"timed_scenes"`
	UntimedScenes int `json:"untimed_scenes"`
	// Budget is what the canvas allowed before any candidate was derived.
	Budget OverlayIntakeBudget `json:"budget"`
	// Candidates counts the items produced by the planner before the
	// admission filters (by kind and in total).
	Candidates       int            `json:"candidates"`
	CandidatesByKind map[string]int `json:"candidates_by_kind,omitempty"`
	// Planned counts the sealed plan (by kind and in total).
	Planned       int            `json:"planned"`
	PlannedByKind map[string]int `json:"planned_by_kind,omitempty"`
	// DroppedByReason is the stage-attributed admission loss; the sum of its
	// values is Candidates - Planned for additive filters, except composed
	// items, which merge and therefore report as composition_merged.
	DroppedByReason map[string]int `json:"dropped_by_reason,omitempty"`
}

// OverlayIntakeBudget is the run-level admission budget the canvas selected.
type OverlayIntakeBudget struct {
	PhraseOverlays int `json:"phrase_overlays"`
	ImageOverlays  int `json:"image_overlays"`
	MapOverlays    int `json:"map_overlays"`
	MaxPerScene    int `json:"max_overlays_per_scene"`
}

// Admission stages that can drop a candidate item. They are stable strings so
// run reports and the golden-case audit can compare across builds.
const (
	OverlayDropSceneWithoutTiming = "scene_without_timing"
	OverlayDropUnmaterialized     = "unmaterialized_asset"
	OverlayDropEntityImageBudget  = "entity_image_budget"
	OverlayDropMapsOnly           = "maps_only"
	OverlayDropCategoryDisabled   = "category_disabled"
	OverlayDropGroupLimit         = "group_limit"
	OverlayDropEditorialBudget    = "editorial_budget"
	OverlayDropPerSceneCap        = "per_scene_cap"
	OverlayDropCompositionMerged  = "composition_merged"
)

// countOverlayItemsByKind groups items by their rendering kind. An empty kind
// is counted as "unknown" so no candidate silently disappears from the counts.
func countOverlayItemsByKind(items []OverlayItem) map[string]int {
	if len(items) == 0 {
		return nil
	}
	counts := make(map[string]int, len(items))
	for _, item := range items {
		kind := item.Kind
		if kind == "" {
			kind = "unknown"
		}
		counts[kind]++
	}
	return counts
}

// RecordStage attributes the loss between two admission stages. Filters and
// composition both hand the survivor count, so a caller never has to keep a
// stale slice alive across an in-place filter.
func (i *OverlayPlanIntake) RecordStage(reason string, before, after int) {
	if i == nil {
		return
	}
	lost := before - after
	if lost <= 0 {
		return
	}
	if i.DroppedByReason == nil {
		i.DroppedByReason = make(map[string]int, 4)
	}
	i.DroppedByReason[reason] += lost
}

// Finish seals the counts for the final admitted item set.
func (i *OverlayPlanIntake) Finish(items []OverlayItem) {
	if i == nil {
		return
	}
	i.Planned = len(items)
	i.PlannedByKind = countOverlayItemsByKind(items)
}

// AnimationPolicy is a per-group semantic motion choice forwarded unchanged
// to RenderingGen's animation_policies contract.
type AnimationPolicy struct {
	Target       string         `json:"target"`
	GroupID      string         `json:"group_id"`
	SubgroupID   string         `json:"subgroup_id,omitempty"`
	MotionID     string         `json:"motion_id,omitempty"`
	MotionParams map[string]any `json:"motion_params,omitempty"`
}

type OverlaySource struct {
	AssetID   string `json:"asset_id"`
	Path      string `json:"path,omitempty"`
	SHA256    string `json:"sha256"`
	LocalPath string `json:"-"`
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
	ID         string `json:"id"`
	SceneID    string `json:"scene_id,omitempty"`
	GroupID    string `json:"group_id,omitempty"`
	SubgroupID string `json:"subgroup_id,omitempty"`
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
	// this item (e.g. "phrase_default", "image_fade_in"). It is the
	// contract slot of the plan's preset resolver: the value space is owned
	// by the preset registry, never invented here. Empty means "no preset
	// selected" — the item compiles through the semantic_role → Chronon
	// preset table as today. Because it is omitempty, plans authored without
	// a preset keep their exact fingerprint/render key.
	PresetID string `json:"preset_id,omitempty"`
	// ImagePresetID is the independent image treatment for entity cards. The
	// name preset belongs to the text layer; PipelineGen selects both values.
	ImagePresetID string `json:"image_preset_id,omitempty"`
	// MotionID is the optional Chronon-owned text/image motion selected for
	// this semantic item. It is intentionally separate from PresetID: several
	// phrases can use the same phrase_default preset while carrying different
	// motions.
	MotionID string `json:"motion_id,omitempty"`
	// CameraMotionID selects one of the subtle photo-documentary camera
	// recipes applied to this visual item or shared image sequence.
	CameraMotionID string `json:"camera_motion_id,omitempty"`
	// EntityStyleID is the RenderingGen entity-card composition selector. The
	// certified generated selector is "random" (resolved by RenderingGen as
	// a tag query over the full 25 Apple Spatial entity styles with badge
	// color alternation); "badge", "camera", "side", "typewriter",
	// "testo_sotto", "premium_random_v1" and the 01..25 variants are also
	// valid. Empty keeps the legacy behavior (no entity style card).
	EntityStyleID string `json:"entity_style_id,omitempty"`
	// MotionParams carries optional parameters for MotionID. PipelineGen keeps
	// these opaque and RenderingGen validates them against Chronon's catalog.
	MotionParams map[string]any `json:"motion_params,omitempty"`
	// EntityRef carries the content-addressed entity identity this item was
	// resolved from (entity_id + type + canonical name + surface text). It is
	// PipelineGen-INTERNAL identity only: the worker renders from
	// kind/template_id/preset_id/text, so the ref is never serialized onto the
	// overlay-plan.v1 wire (`json:"-"`). The document/editing projections that
	// need WHO an overlay is about read it from the in-memory plan.
	EntityRef *OverlayEntityRef `json:"-"`
	Text      string            `json:"text,omitempty"`
	// EntityCaption is the visible name label for a single entity image.
	// Composite image items keep their labels on ImageLayers instead.
	EntityCaption       string              `json:"entity_caption,omitempty"`
	CaptionMotionID     string              `json:"caption_motion_id,omitempty"`
	CaptionMotionParams map[string]any      `json:"caption_motion_params,omitempty"`
	CaptionFontFamily   string              `json:"caption_font_family,omitempty"`
	CaptionLayout       string              `json:"caption_layout,omitempty"`
	CaptionColor        string              `json:"caption_color,omitempty"`
	Style               map[string]any      `json:"style,omitempty"`
	AssetRefs           []OverlayAssetRef   `json:"asset_refs,omitempty"`
	SoundEffect         *OverlaySoundEffect `json:"sound_effect,omitempty"`
	ImageLayers         []OverlayImageLayer `json:"image_layers,omitempty"`
	// GroupMotionID is the single choreography of a composite image group
	// (image_layers). It is chosen once by PipelineGen from the certified
	// image.group.count_N candidates and must not be rewritten downstream.
	GroupMotionID string `json:"group_motion_id,omitempty"`
	// Frame carries image-card border, shadow, stroke and clipping radius to
	// RenderingGen. It is empty for text and structural items.
	Frame *OverlayItemFrame `json:"frame,omitempty"`
	// Map is the validated geospatial declaration of a kind="map" item: the
	// georeference, pins, motion and provider attribution drawn over the
	// content-addressed basemap in AssetRefs. It rides the overlay-plan.v1
	// wire (the worker's schema owns the map block) and participates in the
	// plan fingerprint; nil on every non-map kind.
	Map       *MapOverlay    `json:"map,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	RenderKey string         `json:"render_key,omitempty"`
}

type OverlayItemFrame struct {
	Border       *OverlayItemFrameBorder `json:"border,omitempty"`
	Shadow       *OverlayItemFrameShadow `json:"shadow,omitempty"`
	Stroke       *OverlayItemFrameStroke `json:"stroke,omitempty"`
	ClipRadiusPX float64                 `json:"clip_radius_px,omitempty"`
}

type OverlayItemFrameBorder struct {
	WidthPX  float64 `json:"width_px"`
	Color    string  `json:"color"`
	RadiusPX float64 `json:"radius_px,omitempty"`
}

type OverlayItemFrameShadow struct {
	Color    string  `json:"color"`
	Opacity  float64 `json:"opacity,omitempty"`
	BlurPX   float64 `json:"blur_px,omitempty"`
	OffsetXP float64 `json:"offset_x_px,omitempty"`
	OffsetYP float64 `json:"offset_y_px,omitempty"`
}

type OverlayItemFrameStroke struct {
	WidthPX float64 `json:"width_px"`
	Color   string  `json:"color"`
}

// OverlayImageLayer is one independently timed and animated image within a
// composite entity-image overlay. Times are relative to the parent item so a
// single queued render can reveal nearby entities at their own spoken anchors.
// OverlaySoundEffect is a producer-selected cue bound to this visual item.
// LocalPath is process-local queue staging data and is never serialized.
// OverlayEntityRef is the content-addressed entity identity of an overlay item:
// item: the stable entity id, the canonical type, the canonical name and the
// surface text actually spoken. It is pure identity metadata — the visual
// rendering is driven by TemplateID/PresetID/Text/AssetRefs, never by this
// ref — and it is PipelineGen-internal: it is deliberately NOT part of the
// overlay-plan.v1 wire contract the worker receives.
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

// OverlayAssetRef is the semantic layer's PROJECTION of the canonical
// kernel/asset.Ref identity: which asset a plan item is about (AssetID), which
// bytes stand for it (SHA256) and what they are (MediaType). Ref() and
// NewOverlayAssetRef() are the only sanctioned bridges to that type, so a
// change in how identity is spelled has one place to land.
//
// It also still carries two LOCATION-adjacent fields, which is a known, tracked
// debt rather than a design choice:
//
//   - URL is a fetchable reference, not a second identity — a reference that
//     cannot be fetched is useless, which is why the media identity gate
//     deliberately does not ban it.
//   - LocalPath (`json:"-"`) is a producer-process runtime cache value. It
//     cannot be removed from here yet: the planner populates it and the
//     prefetch bridge consumes it to stage ALREADY VERIFIED bytes instead of
//     re-downloading them, and that behaviour is pinned by
//     TestAttachEntityCardAssetCarriesVerifiedLocalPathWithoutSerializingIt.
//     Its owner is the canonical AssetMaterializer, which is the next step of
//     the media-identity programme; removing the field before that lands would
//     be deleting a tested feature, not removing a duplicate.
//
// See cmd/archcheck/scan/governance/percheck_media_identity_no_location_fields.go:
// this struct is counted there, and it is counted on purpose.
type OverlayAssetRef struct {
	AssetID   string `json:"asset_id"`
	URL       string `json:"url,omitempty"`
	LocalPath string `json:"-"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
}

// Ref projects the semantic projection onto the canonical identity. The logical
// asset id is this layer's own AssetID (which may be a stable canonical id such
// as "person:michael-jordan" rather than a path), and the content address is
// the digest of the bytes the ref points at.
func (r OverlayAssetRef) Ref() asset.Ref {
	return asset.Ref{AssetID: r.AssetID, SHA256: r.SHA256, MediaType: r.MediaType}.Canonical()
}

// NewOverlayAssetRef builds the projection from the canonical identity. The
// location-adjacent fields are set explicitly by the caller because they are
// exactly the fields the canonical type refuses to carry.
func NewOverlayAssetRef(ref asset.Ref, url, localPath string) OverlayAssetRef {
	canonical := ref.Canonical()
	return OverlayAssetRef{
		AssetID:   canonical.AssetID,
		SHA256:    canonical.SHA256,
		MediaType: canonical.MediaType,
		URL:       url,
		LocalPath: localPath,
	}
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
	if p.DurationMS < 0 {
		return fmt.Errorf("overlay plan: duration_ms must be non-negative")
	}
	if strings.TrimSpace(p.MediaContract) != "" {
		if _, err := ResolveMediaContract(p.MediaContract); err != nil {
			return fmt.Errorf("overlay plan: %w", err)
		}
	}
	if source := p.Source; source != nil {
		if strings.TrimSpace(source.AssetID) == "" || strings.TrimSpace(source.SHA256) == "" || (strings.TrimSpace(source.Path) == "" && strings.TrimSpace(source.LocalPath) == "") {
			return fmt.Errorf("overlay plan: source requires asset_id, sha256 and a path")
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
		if err := validateAnimationReferences(item); err != nil {
			return fmt.Errorf("overlay plan: item %q: %w", item.ID, err)
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
		assetIDs := make(map[string]bool, len(item.AssetRefs))
		for assetIndex, ref := range item.AssetRefs {
			if strings.TrimSpace(ref.AssetID) == "" {
				return fmt.Errorf("overlay plan: item %q asset[%d] requires asset_id", item.ID, assetIndex)
			}
			assetIDs[ref.AssetID] = true
		}
		if sfx := item.SoundEffect; sfx != nil {
			kind := strings.ToLower(strings.TrimSpace(item.Kind))
			if kind != "image" && kind != "entity_image" && kind != "image_popup" && kind != "product" && kind != "logo" {
				return fmt.Errorf("overlay plan: item %q sound_effect requires an image overlay", item.ID)
			}
			if strings.TrimSpace(sfx.AssetRef.AssetID) == "" || len(sfx.AssetRef.SHA256) != 64 || strings.Trim(sfx.AssetRef.SHA256, "0123456789abcdefABCDEF") != "" ||
				strings.TrimSpace(sfx.AssetRef.URL) == "" || !strings.EqualFold(strings.TrimSpace(sfx.AssetRef.MediaType), "audio/mp4") ||
				sfx.StartOffsetMS < 0 || sfx.DurationMS <= 0 || sfx.GainDB != sfx.GainDB || sfx.GainDB > 24 || sfx.GainDB < -96 ||
				sfx.StartOffsetMS > item.EndMs-item.StartMs || sfx.DurationMS > item.EndMs-item.StartMs-sfx.StartOffsetMS {
				return fmt.Errorf("overlay plan: item %q sound_effect requires valid audio identity, visible timing, positive duration, and gain in [-96,24] dB", item.ID)
			}
			visibleStartMS := int64(0)
			visibleEndMS := item.EndMs - item.StartMs
			if len(item.ImageLayers) > 0 {
				firstLayer := item.ImageLayers[0]
				for _, layer := range item.ImageLayers[1:] {
					if layer.StartMS < firstLayer.StartMS {
						firstLayer = layer
					}
				}
				visibleStartMS, visibleEndMS = firstLayer.StartMS, firstLayer.EndMS
				if visibleEndMS > item.EndMs-item.StartMs {
					visibleEndMS = item.EndMs - item.StartMs
				}
			}
			if sfx.StartOffsetMS != visibleStartMS || sfx.DurationMS > visibleEndMS-visibleStartMS {
				return fmt.Errorf("overlay plan: item %q sound_effect must start with its first visible image and fit that image window", item.ID)
			}
		}
		imageLayerIDs := make(map[string]struct{}, len(item.ImageLayers))
		for layerIndex, layer := range item.ImageLayers {
			if strings.TrimSpace(layer.ID) == "" || !assetIDs[layer.AssetID] {
				return fmt.Errorf("overlay plan: item %q image_layers[%d] requires id and an asset_id declared by the item", item.ID, layerIndex)
			}
			if _, exists := imageLayerIDs[layer.ID]; exists {
				return fmt.Errorf("overlay plan: item %q has duplicate image layer id %q", item.ID, layer.ID)
			}
			imageLayerIDs[layer.ID] = struct{}{}
			if layer.StartMS < 0 || layer.EndMS <= layer.StartMS || layer.EndMS > item.EndMs-item.StartMs {
				return fmt.Errorf("overlay plan: item %q image_layers[%d] has invalid relative time range", item.ID, layerIndex)
			}
		}
		if len(item.ImageLayers) > 0 && (item.Kind != string(KindEntityImage) || len(item.ImageLayers) < 2 || len(item.AssetRefs) < 2) {
			return fmt.Errorf("overlay plan: item %q composite images require an entity_image item with at least two image layers and assets", item.ID)
		}
		// Map contract: a map item MUST carry exactly one validated geospatial
		// declaration over its single basemap asset, and no other kind may
		// carry one. The declaration validates against the plan canvas so a
		// plate authored for another size can never enter the wire.
		if item.Kind == string(KindMap) {
			if item.Map == nil {
				return fmt.Errorf("overlay plan: map item %q requires a map declaration", item.ID)
			}
			if err := item.Map.Validate(p.Width, p.Height, item.AssetRefs); err != nil {
				return fmt.Errorf("overlay plan: map item %q: %w", item.ID, err)
			}
		} else if item.Map != nil {
			return fmt.Errorf("overlay plan: item %q must not carry a map declaration", item.ID)
		}
		if err := validateChartItem(item); err != nil {
			return err
		}
		if err := ValidateChainItem(item); err != nil {
			return err
		}
		if sfx := item.SoundEffect; sfx != nil {
			if strings.TrimSpace(sfx.AssetRef.AssetID) == "" {
				return fmt.Errorf("overlay plan: item %q sound_effect requires asset_ref.asset_id", item.ID)
			}
			assetIDs[sfx.AssetRef.AssetID] = true
		}
		key := ComputeRenderKey(*p, item)
		if item.RenderKey != "" && item.RenderKey != key {
			return fmt.Errorf("overlay plan: item %q render_key does not match render inputs", item.ID)
		}
		if item.RenderKey == "" {
			item.RenderKey = key
			p.Items[i] = item
		}
	}
	if p.Fingerprint == "" {
		p.Fingerprint = p.FingerprintValue()
	} else if p.Fingerprint != p.FingerprintValue() {
		return fmt.Errorf("overlay plan: fingerprint does not match plan contents")
	}
	return nil
}
