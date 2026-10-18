// Package script — generation_envelope.go defines the canonical
// top-level request envelope for all script generation. It replaces
// the fragmented per-endpoint request types with a single contract.
//
//	GenerationEnvelopeV2 → [{GenerationItemV2 → SourceSpec + ScriptSpec + OutputSpec}]
//
// A single-item envelope maps to the unified /generate flow.
// A multi-item envelope maps to batch generation.
//
// No durable field uses any, any, or map[string]any.
package script

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptdetail "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/detail"
)

// GenerationEnvelopeV2 is the canonical top-level request for all
// script generation. The worker unpacks the envelope, normalizes
// each item via the shared precedence chain, and executes them
// through the unified pipeline:
//
//	normalize → validate → resolve source → build plan → generate → postprocess → result
type GenerationEnvelopeV2 struct {
	// Version is the envelope schema version. Always 2 for V2.
	Version int `json:"version"`

	// Preset records the endpoint variant for default application:
	//   "custom"       — caller filled every flag explicitly
	//   "with_images"  — force scene_images+voiceover ON, entities+metadata OFF
	//   "batch"        — batch generation (multiple items)
	Preset Preset `json:"preset"`

	// CorrelationID is an optional tracing identifier propagated
	// through logs and job metadata.
	CorrelationID string `json:"correlation_id,omitempty"`

	// ForceRefresh bypasses the idempotency store and active-key
	// dedup for this envelope. When true, a brand-new script.generate
	// job is always enqueued, even if the same Idempotency-Key has
	// an active or completed record.
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// FinalJob marks this generation as requesting the remote final-render
	// handoff. False preserves the existing local-only workflow.
	FinalJob bool `json:"final_job,omitempty"`

	// Items is the list of generation items. Must contain at least
	// one entry. For single-item generation, use one item. For
	// batch generation, use multiple items — each is independently
	// normalized, resolved, generated, and postprocessed.
	Items []GenerationItemV2 `json:"items"`
}

// GenerationItemV2 is a single generation request within an envelope.
// Each item declares its source, script parameters, output options,
// and identity independently. This independence allows a batch
// envelope to mix text-only, clip-based, catalog, and search items
// in a single request.
type GenerationItemV2 struct {
	// ID is an optional caller-assigned identifier for correlating
	// results. When non-empty, the matching GenerationResult
	// carries the same ID.
	ID string `json:"id,omitempty"`
	// OverlayCategories is the item-level default for scene category toggles.
	// Per-segment settings inherit these values unless explicitly overridden.
	OverlayCategories map[OverlayCategory]Toggle `json:"overlay_categories,omitempty"`

	// ── Identity ──────────────────────────────────────────────────────
	Title string `json:"title,omitempty"`
	// Project is the explicit artifact-routing namespace. It is resolved at
	// ingress and propagated unchanged to every published artifact.
	Project string `json:"project,omitempty"`
	// ChannelID is the editorial channel this item is produced FOR (the
	// canonical YouTube channel id, or any operator-defined channel key).
	//
	// It is optional and selection-only: when present it names a channel
	// profile (config/channel_profiles.yaml, loaded at composition time) that
	// fills the subtitle, watermark, overlay-style, SFX and phrase-motion
	// choices the caller left BLANK. An explicit caller value always wins, an
	// empty ChannelID selects no profile, and a channel without a profile is
	// not an error — channels are open-ended, profiles are curated.
	ChannelID string `json:"channel_id,omitempty"`
	Language  string `json:"language,omitempty"`
	Tone      string `json:"tone,omitempty"`
	Style     string `json:"style,omitempty"`
	Model     string `json:"model,omitempty"`
	ModelAuto bool   `json:"-"`

	// MediaMode explicitly selects the media contract for this item.
	// Mixed media is never inferred from the presence of references.
	MediaMode MediaMode `json:"media_mode,omitempty"`

	// ── Source ────────────────────────────────────────────────────────
	// Source declares where the generation input comes from.
	// Must have a valid Type and the corresponding fields populated.
	Source SourceSpec `json:"source"`

	// ── Script parameters ─────────────────────────────────────────────
	// ScriptParams controls HOW the script is generated (sizing,
	// prompt versioning). The field is named ScriptParams, not Script,
	// to avoid shadowing the package name (a Go footgun — any method
	// added to GenerationItemV2 wouldn't be able to reference
	// script.SomeType).
	//
	// All fields are optional; the normalizer fills in defaults.
	ScriptParams ScriptSpec `json:"script_params,omitempty"`

	// ── Output options ────────────────────────────────────────────────
	// Output declares WHAT post-generation artifacts to produce.
	// Every postprocessor is opt-in.
	Output OutputSpec `json:"output,omitempty"`

	// OverlayBackground optionally selects the full-canvas background used by
	// the Chronon overlay render. It is deliberately separate from audio
	// background_music: this is a visual pixel-layer choice.
	OverlayBackground *OverlayBackgroundSpec `json:"overlay_background,omitempty"`
	// OverlayStyle applies the same explicit visual overrides to generated
	// phrase/word/image overlays. It is transport-only; Chronon's preset
	// registry remains the source of defaults.
	OverlayStyle *OverlayStyleSpec `json:"overlay_style,omitempty"`
	// MapType chooses the single basemap treatment used by runtime maps:
	// satellite (default, the classic Python map), roadmap, terrain, or
	// random (classic 3-style palette only). Empty means satellite.
	// MapCameraAnimation currently allows only slow_approach for map arrival;
	// empty/random resolves to that same registered runtime choice.
	// MapLabelAnimation is fixed to none: the retained map compositions
	// own their text; standalone map-label animation styles are retired.
	// MapAnimation selects one of the nine retained map compositions.
	MapType            string `json:"map_type,omitempty"`
	MapCameraAnimation string `json:"map_camera_animation,omitempty"`
	MapLabelAnimation  string `json:"map_label_animation,omitempty"`
	MapAnimation       string `json:"map_animation,omitempty"`
	// MapPresentation is the ONE map configuration contract: minimal intent in,
	// fully resolved presentation out (the registry and the animation selector
	// resolve the rest). When present it replaces the four legacy map_* fields
	// above — mixing both spellings fails closed instead of guessing a winner.
	MapPresentation *MapPresentationSpec `json:"map_presentation,omitempty"`
	// Motion pools are optional caller choices. Empty pools use the runtime's
	// seeded random rotation across the complete certified catalogs.
	PhraseMotions      []string `json:"phrase_motions,omitempty"`
	ImageMotions       []string `json:"image_motions,omitempty"`
	EntityImageMotions []string `json:"entity_image_motions,omitempty"`
	// EntityCaptionMotions is the optional run-level caption pool for
	// automatic entity-caption assignment. Empty keeps the certified
	// camera-safe automatic pool. Entries must name automatic-pool motions.
	EntityCaptionMotions []string `json:"entity_caption_motions,omitempty"`
	// AnimationPolicies explicitly selects one catalog motion per semantic
	// animation group. group_id can match a producer group or scene_id; an
	// optional subgroup_id narrows the selection to one item/layer. Omit
	// motion_id to request one stable random choice for that group/subgroup.
	AnimationPolicies []AnimationPolicy `json:"animation_policies,omitempty"`
	// AnimationDirectives è il contratto canonico tipizzato (P2-1, v2).
	// Precedenza: exact elemento > exact scena > prefer richiesta > profilo
	// canale > auto. I vecchi campi restano accettati e normalizzati una
	// sola volta in direttive; exact incompatibile = errore, mai fallback.
	AnimationDirectives []AnimationDirective `json:"animation_directives,omitempty"`
	// AnimationContractVersion pinna il contratto (P2-6): 0/1 = legacy
	// dual-read, 2 = canonico risolto prima del seal con provenance.
	AnimationContractVersion int `json:"animation_contract_version,omitempty"`
	// AnimationCatalogDigest pinna la vista capacità runtime versionata (P0-3).
	AnimationCatalogDigest string `json:"animation_catalog_digest,omitempty"`
	// RandomLightLeakOnClipStart emits a short, deterministic LIGHT_LEAK overlay
	// at the beginning of each real video clip segment. Image-overlay light leaks
	// can be selected through animation_policies target=image. Pair with
	// audio.random_sfx_on_clip_start or explicit audio.sound_effects for sound.
	RandomLightLeakOnClipStart bool `json:"random_light_leak_on_clip_start,omitempty"`
	// RandomLightLeakOnOverlayStart emits a deterministic short leak at each
	// finalized visual overlay's start.
	RandomLightLeakOnOverlayStart bool `json:"random_light_leak_on_overlay_start,omitempty"`
	// PhraseMotionFamily optionally restricts phrase_motions to a certified
	// family. With no explicit pool or family, the runtime rotates through its
	// seeded random sequence over the full certified animation catalog.
	PhraseMotionFamily string `json:"phrase_motion_family,omitempty"`
	// MotionSeed is the per-render entropy for automatic motion rotation
	// (phrases, short phrases, images, captions, entity styles). Empty keeps
	// the historical deterministic behavior derived from plan/video/project
	// identity, so identical inputs compile byte-identical plans and retries
	// stay bit-identical. Pass a fresh value per render (e.g. the job UUID)
	// to rotate the certified catalogs instead of replaying the same
	// prefix; retries of the same render must reuse the same seed.
	MotionSeed string `json:"motion_seed,omitempty"`
	// CoherenceSeed picks the run's dominant horizontal direction (left or
	// right) so every lane leans the same way. Empty derives it from MotionSeed.
	CoherenceSeed string `json:"coherence_seed,omitempty"`
	// MaxPhraseOverlays sets the requested run-level ceiling for grounded
	// phrase overlays. A positive value may lower or raise the default, but
	// runtime caps it at overlay.MaxPhraseOverlaysHardLimit; zero or omitted
	// keeps overlay.MaxPhraseOverlaysPerRun. Dedup and semantic ranking still
	// run first, and the effective limit is reported in the phrase budget.
	MaxPhraseOverlays int `json:"max_phrase_overlays,omitempty"`
	// MaxOverlaysPerScene caps the rendered overlay items in each scene.
	// Zero retains the established scene budget.
	MaxOverlaysPerScene int `json:"max_overlays_per_scene,omitempty"`
	// MapsOnly keeps runtime-resolved map overlays and removes all image,
	// phrase, number, and other editorial overlay items from the render plan.
	MapsOnly bool `json:"maps_only,omitempty"`
	// MaxMapOverlays sets the run-level ceiling for runtime map overlays.
	// Zero keeps the certified default.
	MaxMapOverlays int `json:"max_map_overlays,omitempty"`
	// MaxImageOverlays sets the run-level ceiling for image overlays. A
	// positive value is honored; zero keeps the certified default.
	MaxImageOverlays int `json:"max_image_overlays,omitempty"`
	// OverlayGroupLimits sets a per-group maximum of rendered overlays. Keys:
	// maps, short_phrases, long_phrases, entities_with_text, images. A key
	// present with 0 disables that group; a positive value caps it (and may
	// raise the maps/images/phrase budgets above the certified defaults); an
	// absent key keeps the full certified behaviour for that group.
	OverlayGroupLimits map[string]int `json:"overlay_group_limits,omitempty"`
	// AnimationCounts sets the maximum distinct styles rotated within each
	// overlay subfamily. Keys match runtime use-case IDs (images, entities,
	// entity_caption, important_phrase, short_important_phrase, one_map,
	// two_maps, metric_stat, timeline_date, or a specific image composition).
	// Omitted subfamilies rotate the FULL certified catalog (every entrance
	// stays available, so long runs never collapse onto the same handful of
	// motions); positive values cap the rotating window to the available
	// catalog. Chain tuning rides here too: chain_gap_ms / chain_enabled opt
	// into fusing nearby overlays into single multi-animation chain items,
	// chain_max_segments bounds them, chain_disabled forces legacy items.
	AnimationCounts map[string]int `json:"animation_counts,omitempty"`
	// EntityStyleID pins the RenderingGen entity-card composition family for
	// every generated entity card in this item: "random" (default, full 25
	// Apple Spatial registry), a tag selector (badge, camera, side,
	// typewriter, testo_sotto, premium_random_v1) or one 01..25 variant.
	// Invalid selectors fail closed at plan compilation.
	EntityStyleID         string `json:"entity_style_id,omitempty"`
	DisableNumberOverlays bool   `json:"disable_number_overlays,omitempty"`
	DisableRecapOverlays  bool   `json:"disable_recap_overlays,omitempty"`
	DisableChartOverlays  bool   `json:"disable_chart_overlays,omitempty"`
	// DisableEditorialOverlays skips the automatic scene_title/scene_bullet
	// projection so operator-supplied grounded phrases keep the run budget
	// (motion-catalog runs). False by default.
	DisableEditorialOverlays bool `json:"disable_editorial_overlays,omitempty"`
	// EnableCharts is the explicit opt-in for chart overlays. Charts render
	// only when this is true and DisableChartOverlays is false; the default
	// (false) means no chart is ever planned.
	EnableCharts bool `json:"enable_charts,omitempty"`
	// HeavyPhrasePriority overrides the editorial-weight threshold that splits
	// the phrase lane: items at or above it take the prominent-motion lane
	// instead of the caller-pool rotation. Zero keeps the certified default.
	// Motion-catalog runs pin it unreachable so every phrase rotates the
	// requested pool exactly (a heavy-boosted phrase would otherwise leave
	// the pool and the no-repeat guard would substitute an outside motion).
	HeavyPhrasePriority float64 `json:"heavy_phrase_priority,omitempty"`

	// Audio configures the audio execution mode (audio.mode) plus the
	// editorial audio intent block (mix_policy, background_music,
	// sound_effects) for this generate item. Batch items may choose
	// independently.
	Audio AudioOutputConfig `json:"audio,omitempty"`

	// Docs explicitly requests publication of one Google Doc per language.
	// It is kept separate from Output so document creation is never inferred
	// from unrelated output options.
	Docs DocumentsSpec `json:"docs,omitempty"`

	// ── Media plan ────────────────────────────────────────────────────
	// MediaPlan declares which visual media should accompany the
	// generated script. It is separate from SourceSpec because it
	// describes media assets, not narrative content.
	MediaPlan media.MediaPlanSpec `json:"media_plan,omitempty"`

	// VideoMetadata contains caller-provided YouTube metadata.
	// When present, these values are used directly and the metadata
	// generator must not be called.
	VideoMetadata *VideoMetadata `json:"video_metadata,omitempty"`

	// Intro is an optional protected fixed-media section prepended verbatim.
	// It is not sent to the LLM, translated, synthesized, or replaced by
	// generated media. Clip IDs and playback policy are authoritative.
	Intro *FixedSection `json:"intro,omitempty"`

	// Outro is an optional protected fixed-media section appended verbatim.
	// It follows the same authoritative playback contract as Intro.
	Outro *FixedSection `json:"outro,omitempty"`
}

// AnimationPolicy selects a renderer-catalog motion for a semantic group.
// Target is important_phrase, image, caption, date, or metric, matching
// RenderingGen's overlay-plan.v1 contract. An empty MotionID requests a stable random choice
// shared by every compatible item in the selected group/subgroup.
type AnimationPolicy struct {
	Target       string         `json:"target"`
	GroupID      string         `json:"group_id"`
	SubgroupID   string         `json:"subgroup_id,omitempty"`
	MotionID     string         `json:"motion_id,omitempty"`
	MotionParams map[string]any `json:"motion_params,omitempty"`
}

// AnimationDirectiveMode è il contratto canonico auto/prefer/exact (§4).
// auto: scelta compatibile riproducibile; prefer: prova richiesta con fallback
// autorizzato; exact: usa esattamente la scelta o errore (mai sostituzione
// silenziosa da no-repeat/sampler/composizione).
type AnimationDirectiveMode string

const (
	AnimationDirectiveAuto   AnimationDirectiveMode = "auto"
	AnimationDirectivePrefer AnimationDirectiveMode = "prefer"
	AnimationDirectiveExact  AnimationDirectiveMode = "exact"
)

// AnimationDirectiveTarget distingue motion/composition/camera/label/map-treatment.
type AnimationDirectiveTarget string

const (
	AnimationTargetImportantPhrase AnimationDirectiveTarget = "important_phrase"
	AnimationTargetEntityImage     AnimationDirectiveTarget = "entity_image"
	AnimationTargetCaption         AnimationDirectiveTarget = "caption"
	AnimationTargetImage           AnimationDirectiveTarget = "image"
	AnimationTargetMapCamera       AnimationDirectiveTarget = "map_camera"
	AnimationTargetMapComposition  AnimationDirectiveTarget = "map_composition"
	AnimationTargetMapLabel        AnimationDirectiveTarget = "map_label"
	AnimationTargetDate            AnimationDirectiveTarget = "date"
	AnimationTargetMetric          AnimationDirectiveTarget = "metric"
)

// AnimationDirective è la rappresentazione canonica: dichiara scena,
// contenuto, entità opzionale, preferenza e parametri — mai il futuro item.ID.
// I vecchi campi (phrase_motions, animation_policies, map_*_animation)
// restano ingressi compatibili normalizzati una sola volta in direttive.
type AnimationDirective struct {
	Scope    string                   `json:"scope"`
	SceneRef string                   `json:"scene_ref"`
	Target   AnimationDirectiveTarget `json:"target"`
	Mode     AnimationDirectiveMode   `json:"mode,omitempty"`
	MotionID string                   `json:"motion_id,omitempty"`
	Family   string                   `json:"family,omitempty"`
	Params   map[string]any           `json:"params,omitempty"`
}

// AnimationContractVersion versiona il contratto risolto (P2-6).
// 1 = legacy (vecchi campi + animation_policies con doppio sampling);
// 2 = canonico (direttive risolte prima del seal + provenance, no resample).
const AnimationContractVersion = 2

// MapPresentationSpec is the unified map configuration contract: minimal
// intent in, resolved certified composition out. The field contract and its
// fail-closed resolution rules live in the script/detail leaf package; this
// alias keeps the parent package the single import surface.
type MapPresentationSpec = scriptdetail.MapPresentationSpec

// MapPresentation is the RESOLVED canonical map configuration. It is the only
// shape downstream owners consume.
type MapPresentation = scriptdetail.MapPresentation

// DefaultMaxPinsPerGroup is the certified renderer budget for one map group.
const DefaultMaxPinsPerGroup = scriptdetail.DefaultMaxPinsPerGroup

// OverlayBackgroundSpec is the script.generate payload shape for an
// optional visual overlay background. Image/video backgrounds identify one
// content-addressed asset; color backgrounds use RGBA components in [0,1].
type OverlayBackgroundSpec struct {
	Kind    string    `json:"kind"`
	Color   []float64 `json:"color,omitempty"`
	AssetID string    `json:"asset_id,omitempty"`
	URL     string    `json:"url,omitempty"`
	// LocalPath is an internal, verified cache path populated by the
	// composition-layer asset resolver. Callers normally provide AssetID;
	// the path never needs to cross the queue boundary.
	LocalPath string            `json:"local_path,omitempty"`
	SHA256    string            `json:"sha256,omitempty"`
	MediaType string            `json:"media_type,omitempty"`
	Fit       string            `json:"fit,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	Loop      bool              `json:"loop,omitempty"`
	Style     *OverlayStyleSpec `json:"style,omitempty"`
}

// DocumentsSpec is the transport-level document publication configuration.
type DocumentsSpec struct {
	Enabled   bool     `json:"enabled"`
	Languages []string `json:"languages,omitempty"`
	FolderID  string   `json:"folder_id,omitempty"`
}

// FixedPlaybackAudioMode declares the authoritative audio source for a
// protected fixed-media section.
type FixedPlaybackAudioMode string

const (
	// FixedPlaybackOriginalClip keeps the clip's original audio authoritative;
	// it is never replaced by generated voiceover.
	FixedPlaybackOriginalClip FixedPlaybackAudioMode = "original_clip"
)

// FixedPlaybackPolicy is the explicit playback contract for fixed media.
// A zero policy normalizes to original_clip with a deterministic five-second
// editorial window, so the timeline compiler always receives a duration.
type FixedPlaybackPolicy struct {
	AudioMode   FixedPlaybackAudioMode `json:"audio_mode"`
	SourceInMS  int64                  `json:"source_in_ms,omitempty"`
	SourceOutMS int64                  `json:"source_out_ms,omitempty"`
}

// DefaultFixedPlaybackDurationMS is the safe editorial window used when a
// fixed section omits both source endpoints. Callers can still request any
// other window explicitly.
const DefaultFixedPlaybackDurationMS int64 = 5000

// Normalize returns the canonical fixed playback policy.
func (p FixedPlaybackPolicy) Normalize() FixedPlaybackPolicy {
	if p.AudioMode == "" {
		p.AudioMode = FixedPlaybackOriginalClip
	}
	if p.SourceInMS == 0 && p.SourceOutMS == 0 {
		p.SourceOutMS = DefaultFixedPlaybackDurationMS
	}
	return p
}

// Valid reports whether the fixed playback policy is structurally valid.
// A zero-value policy is valid after normalization; a partial window must
// have both endpoints and a strictly positive duration.
func (p FixedPlaybackPolicy) Valid() bool {
	p = p.Normalize()
	if p.AudioMode != FixedPlaybackOriginalClip || p.SourceInMS < 0 || p.SourceOutMS < 0 {
		return false
	}
	if p.SourceOutMS == 0 {
		return p.SourceInMS == 0
	}
	return p.SourceOutMS > p.SourceInMS
}

// FixedSection is a protected intro/outro media section that bypasses the
// LLM and all generated-scene processors. ClipIDs are authoritative media
// bindings; DisplayText is optional visual/document text. DisplayText is never
// sent to TTS or the LLM, but it MAY be translated for subtitle/caption burn
// (Intro V2: SceneExecutionMode.AllowsDisplayTextTranslation) so localized
// renders burn translated captions. Playback explicitly selects original clip
// audio and a source window.
type FixedSection struct {
	// ClipIDs is the authoritative ordered clip binding for this section. Any
	// non-empty sequence is allowed, so an intro/outro can span as many
	// back-to-back clips as the caller needs.
	ClipIDs []string `json:"clip_ids,omitempty"`
	// Title is an optional human-readable title for the Docs scene.
	Title string `json:"title,omitempty"`
	// DisplayText is optional text shown alongside the fixed media. It is not
	// narration and is never synthesized (no TTS) or sent to the LLM; it may be
	// translated for subtitle/caption burn (Intro V2).
	DisplayText string `json:"display_text,omitempty"`
	// Playback is the authoritative original-audio and source-window policy.
	Playback FixedPlaybackPolicy `json:"playback"`
	// Text is a poison-pill field: the legacy `text` narration alias was
	// removed in July 2026 because it reintroduced "speakable text" into a
	// section that must never reach the LLM or TTS. The field exists ONLY so
	// validation can reject legacy payloads with a precise error; it is never
	// read as content. Use display_text instead.
	Text string `json:"text,omitempty"`
}

// EffectiveDisplayText returns the visual text without inviting generated
// narration. There is no legacy fallback: display_text is the only text
// surface a fixed section carries.
func (f *FixedSection) EffectiveDisplayText() string {
	if f == nil {
		return ""
	}
	if text := strings.TrimSpace(f.DisplayText); text != "" {
		return text
	}
	return ""
}

// NormalizedPlayback returns the canonical playback policy for this section.
func (f *FixedSection) NormalizedPlayback() FixedPlaybackPolicy {
	if f == nil {
		return FixedPlaybackPolicy{AudioMode: FixedPlaybackOriginalClip}
	}
	return f.Playback.Normalize()
}

// NormalizedClipIDs returns trimmed, non-empty clip IDs for this section.
func (f *FixedSection) NormalizedClipIDs() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.ClipIDs))
	for _, id := range f.ClipIDs {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// CloneFixedSection returns a deep copy of a FixedSection.
func CloneFixedSection(in *FixedSection) *FixedSection {
	if in == nil {
		return nil
	}
	out := *in
	if in.ClipIDs != nil {
		out.ClipIDs = append([]string(nil), in.ClipIDs...)
	}
	out.Playback = in.Playback.Normalize()
	return &out
}
