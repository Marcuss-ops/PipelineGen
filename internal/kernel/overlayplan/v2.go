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
}

// PlanIdentity is the immutable plan identity. Motion entropy is derived from
// it, never from wall-clock state.
type PlanIdentity struct {
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	ProjectID     string `json:"project_id"`
	MotionSeed    string `json:"motion_seed"`
	CoherenceSeed string `json:"coherence_seed"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPSNum        int    `json:"fps_num"`
	FPSDen        int    `json:"fps_den"`
}

// Canvas carries the run-level selection controls. Rendering metadata stays
// opaque and is deliberately absent from this contract.
type Canvas struct {
	OverlayCategories        map[string]string `json:"overlay_categories"`
	GroupLimits              map[string]int    `json:"group_limits,omitempty"`
	MaxOverlaysPerScene      int               `json:"max_overlays_per_scene"`
	MaxPhraseOverlays        int               `json:"max_phrase_overlays"`
	MaxImageOverlays         int               `json:"max_image_overlays"`
	MaxMapOverlays           int               `json:"max_map_overlays"`
	MapPresentation          MapPresentation   `json:"map_presentation"`
	AnimationCounts          map[string]int    `json:"animation_counts,omitempty"`
	PhraseMotions            []string          `json:"phrase_motions,omitempty"`
	ShortPhraseMotion        string            `json:"short_phrase_motion,omitempty"`
	LongPhraseMotion         string            `json:"long_phrase_motion,omitempty"`
	ImageMotions             []string          `json:"image_motions,omitempty"`
	ImageWithTextMotions     []string          `json:"image_with_text_motions,omitempty"`
	EntityImageMotions       []string          `json:"entity_image_motions,omitempty"`
	EntityCaptionMotions     []string          `json:"entity_caption_motions,omitempty"`
	ImageMotionOffset        int               `json:"image_motion_offset,omitempty"`
	EntityStyleID            string            `json:"entity_style_id,omitempty"`
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
}

// MapPresentation is the resolved unified map policy. Only the fields that
// change planner decisions are typed; renderer parameters stay outside.
type MapPresentation struct {
	MapType           string `json:"map_type,omitempty"`
	CameraAnimation   string `json:"camera_animation,omitempty"`
	LabelAnimation    string `json:"label_animation,omitempty"`
	Animation         string `json:"animation,omitempty"`
	Level             string `json:"level,omitempty"`
	MaxPinsPerGroup   int    `json:"max_pins_per_group,omitempty"`
	GroupNearbyPlaces bool   `json:"group_nearby_places,omitempty"`
	AllowRoute        bool   `json:"allow_route,omitempty"`
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
	Phrases           []TimedAnnotation  `json:"phrases,omitempty"`
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
	ChartsCertified   bool        `json:"charts_certified,omitempty"`
	// SourceNumericValues is the source-language numeric vocabulary used by the
	// translated grounding gate. It is data, not a selected candidate.
	SourceNumericValues []string `json:"source_numeric_values,omitempty"`
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

// ChapterManifest is the extractive chapter projection of the transcript.
type ChapterManifest struct {
	SchemaVersion string    `json:"schema_version"`
	Chapters      []Chapter `json:"chapters"`
}

// Chapter is one chapter; only its bullets participate in the projection.
type Chapter struct {
	Bullets []ChapterBullet `json:"bullets"`
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
	Background      any    `json:"background,omitempty"`
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
	MaterializedAssets   []AssetRef               `json:"materialized_assets,omitempty"`
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
