// Package overlayplan owns the wire contract shared by the Go consumer and
// the BrainRust overlay compiler for the 13d single-call cutover.
//
// The envelope is versioned (`editorial_plan.v2`) and additive: later phases
// add typed sections without changing the meaning of existing fields. A
// missing or unknown schema_version is rejected by the Rust validator before
// any decision runs; this package only provides the exact JSON projection.
package overlayplan

import "encoding/json"

const (
	// SchemaVersionV2 is the full-plan request/response envelope version.
	SchemaVersionV2 = "editorial_plan.v2"
	// EditorialManifestVersion is the per-scene editorial manifest version.
	EditorialManifestVersion = "editorial.v1"
	// ChapterManifestVersion is the chapter manifest version.
	ChapterManifestVersion = "chapter_manifest.v1"
	// EditorialProfileVersion is the certified editorial profile.
	EditorialProfileVersion = "highlights.v1"
)

// V2Request is one complete overlay-plan compilation request. It carries the
// inputs the Go planner currently feeds to the interleaved phases; the final
// single call will consume this envelope and return the sealed plan.
type V2Request struct {
	// Operation keeps older installed semantic-engine binaries compatible
	// with the full-plan wire contract; current BrainRust versions accept the
	// field as an optional operation echo and validate its value when present.
	Operation      string       `json:"operation"`
	SchemaVersion  string       `json:"schema_version"`
	Plan           PlanIdentity `json:"plan"`
	Language       string       `json:"language"`
	SourceLanguage string       `json:"source_language"`
	Canvas         Canvas       `json:"canvas"`
	Seal           *Seal        `json:"seal,omitempty"`
	Scenes         []Scene      `json:"scenes"`
	// Clips are the canonical real video spans. Rust derives clip-start light
	// leaks from them; they never contain an admitted overlay.
	Clips          []Clip              `json:"clips,omitempty"`
	EntityTimeline *EntityTimeline     `json:"entity_timeline,omitempty"`
	Media          *Media              `json:"media,omitempty"`
	Budgets        *Budgets            `json:"budgets,omitempty"`
	SceneWindows   map[string][2]int64 `json:"scene_windows,omitempty"`
	Editorial      *EditorialManifest  `json:"editorial,omitempty"`
	Chapters       *ChapterManifest    `json:"chapters,omitempty"`
	// ImagePlan carries the producer's eligibility and run-level image ceiling;
	// Rust performs the fair allocation. Nil keeps fixtures pre-projected.
	ImagePlan *ImagePlan `json:"image_plan,omitempty"`
	// HeavySentences is the certified phrase-impact ranking. Rust uses it to
	// raise (never lower) the score of a grounded phrase candidate.
	HeavySentences []HeavySentence `json:"heavy_sentences,omitempty"`
	// NumericLexicon is the data-only vocabulary the Rust numeric cleaner uses
	// to decide whether a raw NLP surface is a number or date context. It is
	// vocabulary, not a pre-selected candidate.
	NumericLexicon *NumericLexicon `json:"numeric_lexicon,omitempty"`
	// MotionCapabilities is the declared capability catalog. Rust admits a
	// motion into a shared-canvas chain from `supports_chain` instead of the
	// `short_phrase_` id prefix (ED-08). It is additive: when absent the planner
	// keeps its legacy prefix rule, so an older producer is unchanged.
	MotionCapabilities *MotionCapabilities `json:"motion_capabilities,omitempty"`
}

// PlanIdentity is the immutable plan identity. Motion entropy is derived from
// it, never from wall-clock state.
type PlanIdentity struct {
	PlanID     string `json:"plan_id"`
	VideoID    string `json:"video_id"`
	ProjectID  string `json:"project_id"`
	MotionSeed string `json:"motion_seed"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FPSNum     int    `json:"fps_num"`
	FPSDen     int    `json:"fps_den"`
}

// Canvas carries the run-level selection controls. Rendering metadata stays
// opaque and is deliberately absent from this contract.
type Canvas struct {
	OverlayCategories   map[string]string `json:"overlay_categories"`
	GroupLimits         map[string]int    `json:"group_limits,omitempty"`
	MaxOverlaysPerScene int               `json:"max_overlays_per_scene"`
	MaxPhraseOverlays   int               `json:"max_phrase_overlays"`
	MaxImageOverlays    int               `json:"max_image_overlays"`
	MaxMapOverlays      int               `json:"max_map_overlays"`
	// Coverage pacing (ITCLOSE-05). MaxGapUS is the longest uncovered
	// interval the planner may leave after its gap-aware pass; GapCooldownUS is
	// the minimum distance between a promoted card and its neighbours;
	// CoverageMinPercent is the editorial target the pre-render gate enforces;
	// PhrasePerMinute derives the phrase ceiling from the plan duration instead
	// of a fixed number a long video can never reach. Zero keeps the planner's
	// editorial defaults, so an existing producer keeps its exact request.
	MaxGapUS           int64           `json:"max_gap_us,omitempty"`
	GapCooldownUS      int64           `json:"gap_cooldown_us,omitempty"`
	CoverageMinPercent float64         `json:"coverage_min_percent,omitempty"`
	PhrasePerMinute    float64         `json:"phrase_per_minute,omitempty"`
	MapPresentation    MapPresentation `json:"map_presentation"`
	AnimationCounts    map[string]int  `json:"animation_counts,omitempty"`
	ShortPhraseMotion  string          `json:"short_phrase_motion,omitempty"`
	// ShortPhraseMotionsByWord is the certified short-phrase entrance pool
	// keyed by the phrase's word count ("1".."5"). With no pin the planner
	// rotates the bucket for the item's own length, so two phrases of
	// different lengths cannot collapse onto one motion. An absent bucket
	// leaves that length on the presentation default.
	ShortPhraseMotionsByWord map[string][]string `json:"short_phrase_motions_by_word,omitempty"`
	// LongPhraseMotions is the certified long-phrase (6+ words) entrance pool.
	// It has no per-length buckets: the planner rotates this pool with the same
	// deterministic sampler as the short lane, and the pin still wins.
	LongPhraseMotions  []string `json:"long_phrase_motions,omitempty"`
	PhraseMotionFamily string   `json:"phrase_motion_family,omitempty"`
	// DateMotions and MetricMotions are the certified `entity.date` and
	// `entity.metric` registry groups. They travel in the request so the Rust
	// planner selects from the catalog instead of its own copied id list
	// (ANIMRAND-08). An empty pool leaves the lane on the presentation default;
	// production always fills them from the animation registry.
	DateMotions   []string `json:"date_motions,omitempty"`
	MetricMotions []string `json:"metric_motions,omitempty"`
	// MotionFamilies maps every numeric motion id to its registry family
	// ("classic", "didone", "typewriter"). The planner classifies with this
	// metadata instead of an id prefix (ANIMRAND-12).
	MotionFamilies map[string]string `json:"motion_families,omitempty"`
	// LightLeakMotions is the certified `image.lightleak` registry group. Same
	// rule: the catalog is the single owner, the planner never keeps a copy.
	LightLeakMotions []string `json:"light_leak_motions,omitempty"`
	// SourceHighlightMotions is the certified `phrase.documentary` group: the
	// compatible pool a source-highlight recipe selects from instead of pinning
	// `doc_04_archival_source_highlight` on every hit (ANIMRAND-05).
	SourceHighlightMotions []string `json:"source_highlight_motions,omitempty"`
	ImageMotions           []string `json:"image_motions,omitempty"`
	ImageWithTextMotions   []string `json:"image_with_text_motions,omitempty"`
	// ImagePresets is the certified `preset.image` vocabulary. The planner's
	// seeded sampler selects from it when it promotes an entity card to an
	// image, so the Rust side never hardcodes a Chronon-owned preset id.
	ImagePresets             []string          `json:"image_presets,omitempty"`
	EntityImageMotions       []string          `json:"entity_image_motions,omitempty"`
	EntityCaptionMotions     []string          `json:"entity_caption_motions,omitempty"`
	ImageMotionOffset        int               `json:"image_motion_offset,omitempty"`
	EntityStyleID            string            `json:"entity_style_id,omitempty"`
	EntityBadgeColor         string            `json:"entity_badge_color,omitempty"`
	AnimationDirectives      []json.RawMessage `json:"animation_directives,omitempty"`
	AnimationContractVersion int               `json:"animation_contract_version,omitempty"`
	AnimationCatalogDigest   string            `json:"animation_catalog_digest,omitempty"`
	DisableEditorialOverlays bool              `json:"disable_editorial_overlays"`
	DisableRecapOverlays     bool              `json:"disable_recap_overlays"`
	DateMotionFamily         string            `json:"date_motion_family,omitempty"`
	EnableCharts             bool              `json:"enable_charts"`
	DisableChartOverlays     bool              `json:"disable_chart_overlays"`
	DisableNumberOverlays    bool              `json:"disable_number_overlays"`
	MapsOnly                 bool              `json:"maps_only"`
	LightLeakOnClipStart     bool              `json:"light_leak_on_clip_start,omitempty"`
	LightLeakOnOverlayStart  bool              `json:"light_leak_on_overlay_start,omitempty"`
	// EditorialBulletStyles pins the font and entrance motion of individual
	// editorial bullets. A scene keeps at most one bullet, so scene_id is the
	// bullet identity; the compiler rejects an unknown or duplicated scene.
	EditorialBulletStyles []EditorialBulletStyle `json:"editorial_bullet_styles,omitempty"`
}

// EditorialBulletStyle is the wire shape of one per-bullet presentation pin.
// Empty font_family/motion_id keep the presentation defaults, and zero
// font_size_px keeps the size derived from the bullet length.
type EditorialBulletStyle struct {
	SceneID    string  `json:"scene_id"`
	FontFamily string  `json:"font_family,omitempty"`
	FontSizePX float64 `json:"font_size_px,omitempty"`
	MotionID   string  `json:"motion_id,omitempty"`
}

// MapPresentation is the resolved unified map policy. Only the fields that
// change planner decisions are typed; renderer parameters stay outside.
type MapPresentation struct {
	MapType           string   `json:"map_type,omitempty"`
	CameraAnimation   string   `json:"camera_animation,omitempty"`
	LabelAnimation    string   `json:"label_animation,omitempty"`
	Animation         string   `json:"animation,omitempty"`
	AnimationPool     []string `json:"animation_pool,omitempty"`
	StylePool         []string `json:"style_pool,omitempty"`
	Level             string   `json:"level,omitempty"`
	MaxPinsPerGroup   int      `json:"max_pins_per_group,omitempty"`
	GroupNearbyPlaces bool     `json:"group_nearby_places,omitempty"`
	AllowRoute        bool     `json:"allow_route,omitempty"`
}

// Scene is one timed scene. TimelineStartUS/DurationUS are the canonical plan
// offsets and the only authority for clipping a projected phrase.
type Scene struct {
	ID                 string            `json:"id"`
	Text               string            `json:"text"`
	DurationMS         int64             `json:"duration_ms"`
	ExecutionMode      string            `json:"execution_mode"`
	ShortPhraseOverlay bool              `json:"short_phrase_overlay"`
	OverlayCategories  map[string]string `json:"overlay_categories"`
	Index              int               `json:"index,omitempty"`
	Annotations        *SceneAnnotations `json:"annotations,omitempty"`
	// Raw is the pre-selection semantic surface. When present, Rust projects
	// it into Annotations; Go must not ground, time, drop or reorder it.
	Raw             *RawScene  `json:"raw,omitempty"`
	Voiceover       *Voiceover `json:"voiceover,omitempty"`
	TimelineStartUS int64      `json:"timeline_start_us"`
	DurationUS      int64      `json:"duration_us"`
}

// Clip is one real video span on the canonical timeline. Freeze tails are
// carried explicitly so the planner never derives a light leak from a held
// frame.
type Clip struct {
	SceneID    string `json:"scene_id"`
	AssetID    string `json:"asset_id,omitempty"`
	Index      int    `json:"index,omitempty"`
	StartUS    int64  `json:"start_us,omitempty"`
	DurationUS int64  `json:"duration_us,omitempty"`
	Freeze     bool   `json:"freeze,omitempty"`
}

// SceneAnnotations is the planner's complete certified annotation projection.
type SceneAnnotations struct {
	Phrases []TimedAnnotation `json:"phrases,omitempty"`
	// VisualRecipes carries every grounded recipe of the scene. VisualRecipe/
	// VisualPayload stay the legacy first element.
	VisualRecipe      string             `json:"visual_recipe,omitempty"`
	VisualPayload     json.RawMessage    `json:"visual_payload,omitempty"`
	VisualRecipes     []VisualRecipeSpec `json:"visual_recipes,omitempty"`
	Keywords          []TimedAnnotation  `json:"keywords,omitempty"`
	Images            []ImageCandidate   `json:"images,omitempty"`
	Numbers           []TimedAnnotation  `json:"numbers,omitempty"`
	ChartNumbers      []TimedAnnotation  `json:"chart_numbers,omitempty"`
	BrandTexts        []TimedAnnotation  `json:"brand_texts,omitempty"`
	Quotes            []TimedAnnotation  `json:"quotes,omitempty"`
	Recaps            []RecapCard        `json:"recaps,omitempty"`
	RecapPlacement    bool               `json:"recap_placement,omitempty"`
	Products          []ImageCandidate   `json:"products,omitempty"`
	Logos             []ImageCandidate   `json:"logos,omitempty"`
	Maps              []MapCandidate     `json:"maps,omitempty"`
	MultiEntityGroups []MultiEntityGroup `json:"multi_entity_groups,omitempty"`
	EndUS             int64              `json:"end_us,omitempty"`
}

// RawScene is the pre-selection scene surface sent by the producer. Rust owns
// the phrase/entity/date/chart projection; this type deliberately carries no
// admitted item and no pre-computed window.
type RawScene struct {
	// SceneID is the local join key and is never serialized; the owning Scene
	// already carries the id on the wire.
	SceneID string `json:"-"`

	ImportantPhrases  []RawPhrase `json:"important_phrases,omitempty"`
	PrimaryEntities   []RawEntity `json:"primary_entities,omitempty"`
	SecondaryEntities []RawEntity `json:"secondary_entities,omitempty"`
	ChartValues       []string    `json:"chart_values,omitempty"`
	// VisualRecipe is the opaque producer hint ("counter_odometer", ...). The
	// producer never interprets it: overlay-compiler validates it and lowers it
	// onto a certified template. Empty when the scene has no grounded recipe.
	VisualRecipe string `json:"visual_recipe,omitempty"`
	// VisualPayload is the recipe's grounded payload, forwarded verbatim.
	VisualPayload json.RawMessage `json:"visual_payload,omitempty"`
	// VisualRecipes is every supported grounded recipe, ordered by priority.
	// VisualRecipe/VisualPayload remain the first element so older Rust
	// compilers keep consuming the same wire.
	VisualRecipes   []VisualRecipeSpec `json:"visual_recipes,omitempty"`
	ChartsCertified bool               `json:"charts_certified,omitempty"`
	// SourceNumericValues is the source-language numeric vocabulary used by the
	// translated grounding gate. It is data, not a selected candidate.
	SourceNumericValues []string `json:"source_numeric_values,omitempty"`
}

// VisualRecipeSpec is one grounded recipe plus its opaque payload. The
// producer never interprets either; overlay-compiler validates the name and
// lowers the payload onto a certified template.
type VisualRecipeSpec struct {
	Recipe  string          `json:"recipe"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type RawPhrase struct {
	Text  string  `json:"text"`
	Score float64 `json:"score,omitempty"`
}

type RawEntity struct {
	StableEntityID    string          `json:"stable_entity_id,omitempty"`
	CanonicalEntityID string          `json:"canonical_entity_id,omitempty"`
	Type              string          `json:"type"`
	CanonicalName     string          `json:"canonical_name"`
	Confidence        float64         `json:"confidence,omitempty"`
	Geo               *RawGeo         `json:"geo,omitempty"`
	Image             *RawEntityImage `json:"image,omitempty"`
	Mentions          []RawMention    `json:"mentions,omitempty"`
	// PlateCovered is a media fact: a certified local raster covers the point.
	// Rust still owns whether the place becomes a map.
	PlateCovered bool `json:"plate_covered,omitempty"`
}

type RawGeo struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Scope     string  `json:"scope,omitempty"`
}

type RawEntityImage struct {
	AssetID   string `json:"asset_id,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type RawMention struct {
	Text      string `json:"text,omitempty"`
	StartRune int    `json:"start_rune,omitempty"`
	EndRune   int    `json:"end_rune,omitempty"`
}

type HeavySentence struct {
	Text       string  `json:"text"`
	Importance float64 `json:"importance,omitempty"`
}

type NumericLexicon struct {
	Words   []string `json:"words,omitempty"`
	Context []string `json:"context,omitempty"`
}

// TimedAnnotation is one grounded text surface with exact timing. The fields
// mirror the Rust `TimedAnnotationV2` wire shape one for one.
type TimedAnnotation struct {
	Text               string  `json:"text"`
	Type               string  `json:"type,omitempty"`
	StartMS            int64   `json:"start_ms,omitempty"`
	EndMS              int64   `json:"end_ms,omitempty"`
	StartUS            int64   `json:"start_us,omitempty"`
	DurationUS         int64   `json:"duration_us,omitempty"`
	Score              float64 `json:"score,omitempty"`
	PresentationRole   string  `json:"presentation_role,omitempty"`
	EditorialCandidate string  `json:"editorial_candidate,omitempty"`
}

// Voiceover is the scene's certified voiceover projection; only timing is
// needed by the editorial/chapter phase.
type Voiceover struct {
	Timing *Timing `json:"timing,omitempty"`
}

// Timing is the certified word-level timing, copied verbatim from the audio
// authority. It is never estimated from text length or scene duration.
type Timing struct {
	DurationUS int64        `json:"duration_us"`
	Words      []WordTiming `json:"words"`
}

// WordTiming is one word boundary in integer microseconds.
type WordTiming struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
}

// EditorialManifest is the immutable editorial extractor output.
type EditorialManifest struct {
	SchemaVersion  string           `json:"schema_version"`
	SourceLanguage string           `json:"source_language"`
	ProfileVersion string           `json:"profile_version"`
	Scenes         []EditorialScene `json:"scenes"`
	Certified      bool             `json:"certified"`
	Fingerprint    string           `json:"fingerprint"`
}

// EditorialScene is one scene's extractive editorial product.
type EditorialScene struct {
	SceneID         string            `json:"scene_id"`
	Title           *string           `json:"title,omitempty"`
	TitleStatus     string            `json:"title_status"`
	TitleSource     string            `json:"title_source"`
	GloballyIndexed bool              `json:"globally_indexed"`
	Bullets         []EditorialBullet `json:"bullets"`
}

// EditorialBullet is one extractive editorial sentence span.
type EditorialBullet struct {
	SentenceStart int    `json:"sentence_start"`
	SentenceEnd   int    `json:"sentence_end"`
	Text          string `json:"text"`
}

// ChapterManifest is the chapter projection of the transcript plus the policy
// the worker applied to it.
type ChapterManifest struct {
	SchemaVersion string    `json:"schema_version"`
	Chapters      []Chapter `json:"chapters"`
	// Mode is off, auto, explicit or chapter_only. Empty keeps the historical
	// automatic extraction.
	Mode string `json:"mode,omitempty"`
	// StyleFamily is the declared chapter style family (documentary or
	// editorial_minimal).
	StyleFamily string `json:"style_family,omitempty"`
	// DurationMS is the fixed readable beat of the chapter card.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// ChapterOnlySceneIDs names the scenes that admit the chapter and nothing
	// else.
	ChapterOnlySceneIDs []string `json:"chapter_only_scene_ids,omitempty"`
}

// Chapter is one chapter. Its bullets participate in the editorial projection;
// an authored title opens the chapter with its own card.
type Chapter struct {
	// Title is the chapter title. TitleSource says who authored it: an
	// `authored` title is editorial authorship and is exempt from verbatim
	// grounding.
	Title         string `json:"title,omitempty"`
	TitleSource   string `json:"title_source,omitempty"`
	ChapterNumber int    `json:"chapter_number,omitempty"`
	// OpeningSceneID is the scene the chapter opens on; the card is placed
	// there.
	OpeningSceneID string          `json:"opening_scene_id,omitempty"`
	Bullets        []ChapterBullet `json:"bullets"`
}

// ChapterBullet is one extractive chapter bullet.
type ChapterBullet struct {
	Text          string `json:"text"`
	StartSentence int    `json:"start_sentence"`
	EndSentence   int    `json:"end_sentence"`
}

// V2Response is the projection response. The caller keeps its complete scene
// DTOs and appends these annotations.
type V2Response struct {
	SchemaVersion string                   `json:"schema_version"`
	ScenePhrases  map[string][]ScenePhrase `json:"scene_phrases"`
	CoveredScenes []string                 `json:"covered_scenes"`
}

// Seal is the canonical plan extent and output profile.
type Seal struct {
	AudioDurationUS int64  `json:"audio_duration_us"`
	OutputProfileID string `json:"output_profile_id,omitempty"`
	// Background is the sealed canvas background. It is typed here instead of
	// `any` so a producer field cannot drift past the wire contract unnoticed;
	// the shape mirrors the overlay model field for field.
	Background *Background `json:"background,omitempty"`
}

// Background is the sealed canvas background declaration.
type Background struct {
	Kind      string         `json:"kind"`
	Color     []float64      `json:"color,omitempty"`
	AssetRefs []AssetRef     `json:"asset_refs,omitempty"`
	Fit       string         `json:"fit,omitempty"`
	Opacity   *float64       `json:"opacity,omitempty"`
	Loop      bool           `json:"loop,omitempty"`
	Style     map[string]any `json:"style,omitempty"`
}

// ImageCandidate is one certified, fetchable still candidate.
type ImageCandidate struct {
	AssetID    string  `json:"asset_id,omitempty"`
	URL        string  `json:"url,omitempty"`
	SHA256     string  `json:"sha256,omitempty"`
	MediaType  string  `json:"media_type,omitempty"`
	StartMS    int64   `json:"start_ms,omitempty"`
	EndMS      int64   `json:"end_ms,omitempty"`
	StartUS    int64   `json:"start_us,omitempty"`
	DurationUS int64   `json:"duration_us,omitempty"`
	Score      float64 `json:"score,omitempty"`
	// Pinned stills keep their own geometry/style: Rust leaves them out of
	// automatic image chains.
	Pinned bool `json:"pinned,omitempty"`
}

// MapCandidate is one geocoder-validated place candidate.
type MapCandidate struct {
	EntityID   string  `json:"entity_id"`
	Label      string  `json:"label"`
	Scope      string  `json:"scope,omitempty"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	StartMS    int64   `json:"start_ms,omitempty"`
	EndMS      int64   `json:"end_ms,omitempty"`
	StartUS    int64   `json:"start_us,omitempty"`
	DurationUS int64   `json:"duration_us,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

// MultiEntityGroup is an explicitly authored adjacent image/phrase group.
type MultiEntityGroup struct {
	GroupID       string                 `json:"group_id"`
	SceneID       string                 `json:"scene_id,omitempty"`
	GroupType     string                 `json:"group_type,omitempty"`
	Count         int                    `json:"count,omitempty"`
	Items         []MultiEntityCandidate `json:"items,omitempty"`
	StartMS       int64                  `json:"start_ms,omitempty"`
	EndMS         int64                  `json:"end_ms,omitempty"`
	FocusStrategy string                 `json:"focus_strategy,omitempty"`
}

// MultiEntityCandidate is one member of a multi-entity group.
type MultiEntityCandidate struct {
	EntityID string    `json:"entity_id"`
	Kind     string    `json:"kind,omitempty"`
	Label    string    `json:"label,omitempty"`
	AssetRef *AssetRef `json:"asset_ref,omitempty"`
	StartMS  int64     `json:"start_ms,omitempty"`
	EndMS    int64     `json:"end_ms,omitempty"`
}

// AssetRef is the canonical, location-free media identity plus fetchable URL.
type AssetRef struct {
	AssetID   string `json:"asset_id"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// EntityTimeline is the canonical entity occurrence projection.
type EntityTimeline struct {
	Version    int                   `json:"version"`
	ProjectID  string                `json:"project_id,omitempty"`
	Language   string                `json:"language,omitempty"`
	DurationUS int64                 `json:"duration_us"`
	Scenes     []SceneEntityTimeline `json:"scenes,omitempty"`
}

// SceneEntityTimeline is one scene's occurrences plus its canonical offset.
type SceneEntityTimeline struct {
	SceneID          string             `json:"scene_id"`
	SceneIndex       int                `json:"scene_index,omitempty"`
	TimelineStartUS  int64              `json:"timeline_start_us,omitempty"`
	VoiceoverAssetID string             `json:"voiceover_asset_id,omitempty"`
	Entities         []EntityOccurrence `json:"entities,omitempty"`
}

// EntityOccurrence is one grounded entity occurrence on the final timeline.
type EntityOccurrence struct {
	EntityID          string  `json:"entity_id"`
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	SceneID           string  `json:"scene_id,omitempty"`
	SceneIndex        int     `json:"scene_index,omitempty"`
	TextStart         int     `json:"text_start,omitempty"`
	TextEnd           int     `json:"text_end,omitempty"`
	WordStart         int     `json:"word_start,omitempty"`
	WordEnd           int     `json:"word_end,omitempty"`
	LocalStartUS      int64   `json:"local_start_us,omitempty"`
	LocalEndUS        int64   `json:"local_end_us,omitempty"`
	TimelineStartUS   int64   `json:"timeline_start_us,omitempty"`
	AudioStartUS      int64   `json:"audio_start_us,omitempty"`
	AudioEndUS        int64   `json:"audio_end_us,omitempty"`
	Confidence        float64 `json:"confidence,omitempty"`
	Role              string  `json:"role,omitempty"`
	CanonicalEntityID string  `json:"canonical_entity_id,omitempty"`
}

// Media is the materialized/verified media and certified scene image index.
type Media struct {
	MaterializedAssets []AssetRef `json:"materialized_assets,omitempty"`
	// VerifiedEntityAssets is the ONE entity-portrait surface: annotation
	// bindings and bundle/timeline resolutions land here under the same
	// occurrence identity. The planner owns the card-vs-image decision; there
	// is no second list of "promotable" portraits.
	VerifiedEntityAssets []VerifiedEntityAsset    `json:"verified_entity_assets,omitempty"`
	SceneImageCandidates []SceneImageCandidateSet `json:"scene_image_candidates,omitempty"`
}

// VerifiedEntityAsset is the certified asset set for one entity identity.
type VerifiedEntityAsset struct {
	EntityID          string     `json:"entity_id,omitempty"`
	CanonicalEntityID string     `json:"canonical_entity_id,omitempty"`
	Assets            []AssetRef `json:"assets,omitempty"`
}

// SceneImageCandidateSet is one scene's certified still candidates.
type SceneImageCandidateSet struct {
	SceneID    string           `json:"scene_id"`
	Candidates []ImageCandidate `json:"candidates,omitempty"`
}

// ImagePlan is the producer's eligibility and run-level ceiling. Rust owns the
// round-robin allocation; Go only verified that every candidate is fetchable.
type ImagePlan struct {
	Enabled        bool     `json:"enabled"`
	Ceiling        int      `json:"ceiling"`
	ExcludedSHA256 []string `json:"excluded_sha256,omitempty"`
}

// RecapCard is one Rust-projected recap card on the certified QUOTE primitive.
type RecapCard struct {
	Text       string  `json:"text"`
	Score      float64 `json:"score,omitempty"`
	StartUS    int64   `json:"start_us"`
	DurationUS int64   `json:"duration_us"`
}

// Budgets is the resolved production budget, before admission.
type Budgets struct {
	PhraseOverlays int `json:"phrase_overlays,omitempty"`
	ImageOverlays  int `json:"image_overlays,omitempty"`
	MapOverlays    int `json:"map_overlays,omitempty"`
	MaxPerScene    int `json:"max_per_scene,omitempty"`
	ShortPhrase    int `json:"short_phrase,omitempty"`
	LongPhrase     int `json:"long_phrase,omitempty"`
	Bullet         int `json:"bullet,omitempty"`
}

// ScenePhrase is one projected editorial/chapter phrase with certified timing.
type ScenePhrase struct {
	Text             string  `json:"text"`
	StartUS          int64   `json:"start_us"`
	DurationUS       int64   `json:"duration_us"`
	Score            float64 `json:"score"`
	PresentationRole string  `json:"presentation_role"`
	Candidate        string  `json:"candidate,omitempty"`
}
