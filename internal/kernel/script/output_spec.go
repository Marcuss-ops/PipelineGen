package script

import (
	"encoding/json"
	"fmt"
	"strings"

	audio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
)

// OutputSpec declares which post-generation artifacts to produce.
// ExtractEntities and GenerateMetadata are Toggle tri-state values.
// Caller-explicit ToggleDisabled survives the applySafetyDefaults +
// ApplyPreset chain. SaveToDB is a bool persistence flag.
type OutputSpec struct {
	// VideoRender requests reconstruction of every resolved clip with the
	// selected subtitle/watermark layers. It is opt-in and is carried through
	// script.generate into the localized render fan-out.
	Render VideoRenderSpec `json:"render,omitempty"`
	// SSOT: watermark/subtitles configuration lives ONLY in output.render.
	// The former top-level compatibility spellings (output.watermark /
	// output.subtitles) were removed — two spellings for one fact caused the
	// watermark drift fixed in the visual-contract cleanup.
	// Audio is the explicit audio execution mode. Empty preserves the
	// legacy voiceover behavior and is resolved once at the capability edge.
	Audio AudioOutputConfig `json:"audio,omitempty"`
	// ── Postprocessors (Toggle tri-state) ──────────────────────────
	//
	// ExtractEntities is resolved by the semantic runner before the
	// postprocessor walk. Caller
	// explicit ToggleDisabled is preserved through the resolution
	// chain.
	ExtractEntities Toggle `json:"extract_entities,omitempty"`

	// GenerateMetadata is an ACTIVE inline postprocessor
	// (ProcessorMetadata). See ExtractEntities comment for
	// Toggle semantics.
	GenerateMetadata Toggle `json:"generate_metadata,omitempty"`
	// GenerateSceneImages enables the canonical per-scene AI image
	// postprocessor. It is opt-in; omitted and disabled both leave the
	// image processor out of the plan.
	GenerateSceneImages Toggle `json:"generate_scene_images,omitempty"`

	StockEnabled  Toggle              `json:"stock_enabled,omitempty"`
	StockBindings []StockBindingInput `json:"stock_bindings,omitempty"`

	// ── Persistence (bool — out of PR-3 scope per action plan) ──
	SaveToDB bool `json:"save_to_db,omitempty"`
	// GenerateTimeline requests the canonical timeline metadata artifact
	// (scene durations, video segments) WITHOUT binary render
	// materialization. It only needs transcripts and Drive references;
	// no local media is staged and no render job is enqueued.
	GenerateTimeline bool `json:"generate_timeline,omitempty"`

	// ── Voiceover options ────────────────────────────────────────────
	// VoiceoverEnabled is the canonical capability toggle. Routing fields
	// below select the destination only; they are not consulted as an
	// implicit enable switch after normalization.
	VoiceoverEnabled  Toggle `json:"voiceover_enabled,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// ── Document options ─────────────────────────────────────────────
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Formatting ──────────────────────────────────────────────────
	MaxChars  int    `json:"max_chars,omitempty"`
	OutputFmt string `json:"output_fmt,omitempty"`

	// ── Translations ────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// PR-TRANSLATE-SCRIPT-SPEC PR-5+PR-6 (2026-07-09): the canonical
	// opt-in trigger for the TranslationProcessor. When non-empty,
	// buildPostprocessorList appends ProcessorTranslation between
	// metadata and clip_bindings in the EXECUTION order so the
	// translated SpecScene is visible to the downstream clip binder
	// (localised Drive links + clip titles). Empty string is the
	// "no translation requested" sentinel — caller-omission is
	// distinguishable from caller-explicit-empty because callers
	// that want "explicit no translation" pass TranslateTo="". The
	// resolution chain (caller > preset > config > safety) applies
	// unchanged from PR-3.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: a caller that supplies
	// TranslateTo="en" (the script's primary language) intentionally
	// bypasses translation (translator would no-op into ErrTranslationEqualToSource)
	// — this is the canonical explicit opt-in for "I already wrote in
	// the target language, don't waste LLM tokens". The processor
	// surfaces the no-op soft-warning + the bounded-reason metric so
	// operator dashboards can distinguish "translator idle" from
	// "translator absent".
	//
	// godlike/06 SSOT (one-canonical-owner-per-fact): TranslateTo lives
	// ONLY here on OutputSpec. BuildPlan copies it onto the
	// canonical ResolvedGenerationPlan so the postprocessor reads
	// a single source (the plan); no duplicate expression in
	// ResolvedGenerationPlan or in processor_translation.go.
	TranslateTo string `json:"translate_to,omitempty"`
}

type AudioOutputConfig struct {
	Mode string `json:"mode,omitempty"`
	// VoiceoverLanguages limits speech synthesis independently from the
	// translated output languages. Nil preserves legacy synthesis for the
	// source and every requested translation; a non-nil list selects the
	// exact languages to synthesize.
	VoiceoverLanguages []string `json:"voiceover_languages,omitempty"`
	// Timing is the canonical voiceover timing policy nested inside the
	// existing audio config (wire key "timing"). nil means the pipeline
	// applies the canonical defaults (best_effort / word / [json]) —
	// timing capture is never implicitly mandatory.
	Timing *audio.TimingRequest `json:"timing,omitempty"`

	// MixPolicy is the editorial mix decision applied when compiling the
	// audio plan: "VOICEOVER_ONLY" or "VOICEOVER_DUCKED_CLIP" (the wire
	// spelling "voiceover_with_ducked_clip" normalizes to the latter).
	// Empty means no policy (legacy full-volume overlap).
	MixPolicy audio.AudioMixPolicy `json:"mix_policy,omitempty"`

	// BackgroundMusic is the ordered list of BGM layer intents. Each entry
	// references an asset by asset_id only — filesystem paths are never
	// accepted at the wire boundary. Entries may cover disjoint windows of
	// the timeline (start_ms/end); the compiler resolves each window into
	// fully determined timeline events.
	BackgroundMusic []BackgroundMusicIntent `json:"background_music,omitempty"`

	// SoundEffects is the list of SFX intents, placed either at absolute
	// timeline offsets (at_ms) or relative to a scene (scene_id + anchor +
	// offset_ms). Each entry references an asset by asset_id only.
	SoundEffects []SoundEffectIntent `json:"sound_effects,omitempty"`
}

// HasAnyPostprocessor returns true when at least one active postprocessor
// flag is non-disabled (ToggleEnabled or ToggleDefault resolve to true;
// ToggleDisabled resolves to false). SaveToDB is intentionally out of scope.
func (o *OutputSpec) HasAnyPostprocessor() bool {
	return o.ExtractEntities.AsBool() ||
		o.GenerateMetadata.AsBool() ||
		o.GenerateSceneImages.AsBool()
}

// VideoRenderSpec is the opt-in video reconstruction contract carried by
// POST /api/script/generate. It is deliberately independent from the
// narration contract: generation decides the scenes, while the localized
// render fan-out materializes each selected clip.
//
// The block is the SSOT for the full visual stack: background, watermark,
// and subtitles. Style/shadow/transition live ONLY here (canonical
// definition in the kernel); the capability boundaries project them into
// their typed equivalents instead of re-defining parallel structs.
type VideoRenderSpec struct {
	Enabled    bool                 `json:"enabled,omitempty"`
	Background *VideoBackgroundSpec `json:"background,omitempty"`
	Watermark  *VideoWatermarkSpec  `json:"watermark,omitempty"`
	Subtitles  *VideoSubtitlesSpec  `json:"subtitles,omitempty"`
	// SubtitlePresets are reusable request-local styles selected by
	// subtitles.preset or subtitles.style_id.
	SubtitlePresets        map[string]VideoVisualStyleSpec `json:"subtitle_presets,omitempty"`
	RenderConcurrency      int                             `json:"render_concurrency,omitempty"`
	ForegroundScalePercent int                             `json:"foreground_scale_percent,omitempty"`
	OutputDir              string                          `json:"output_dir,omitempty"`
	// DriveFolderID is the parent Drive folder for rendered clips. When
	// DriveSubfolderName is set, the renderer creates/reuses that child.
	DriveFolderID      string `json:"drive_folder_id,omitempty"`
	DriveSubfolderName string `json:"drive_subfolder_name,omitempty"`
	RequireGPU         bool   `json:"require_gpu,omitempty"`
}

// VideoBackgroundSpec selects the rendered background behind the source
// clip. Mode is one of none, blur_source, asset (canonical literals owned
// by the cliprender capability): blur_source derives the blurred background
// from the source itself, asset requires AssetID. Mode "" (omitted)
// normalises to none.
type VideoBackgroundSpec struct {
	Mode    string `json:"mode,omitempty"`
	AssetID string `json:"asset_id,omitempty"`
	// Profile is a human-friendly editorial label (for example "Boxe" or
	// "Discovery"). The script-generation boundary resolves it to AssetID
	// before the request enters the durable pipeline.
	Profile string `json:"profile,omitempty"`
}

// UnmarshalJSON keeps the payload ergonomic: `background: "Boxe"` is
// accepted alongside the canonical object form. Resolution remains outside
// this kernel package so the kernel does not depend on the media registry.
func (s *VideoBackgroundSpec) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("video background: cannot unmarshal into nil receiver")
	}
	if len(data) > 0 && data[0] == '"' {
		var profile string
		if err := json.Unmarshal(data, &profile); err != nil {
			return fmt.Errorf("video background profile: %w", err)
		}
		*s = VideoBackgroundSpec{Profile: profile}
		return nil
	}
	type plainVideoBackgroundSpec VideoBackgroundSpec
	var decoded plainVideoBackgroundSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("video background object: %w", err)
	}
	*s = VideoBackgroundSpec(decoded)
	return nil
}

// VideoVisualStyleSpec is the canonical shared visual override block for
// video layers (watermark + subtitles). Every field is optional so existing
// payloads keep their exact behaviour; the render boundary projects these
// into the strongly-typed Chronon/Rust layer style.
type VideoVisualStyleSpec struct {
	// Concise payload aliases used by clip subtitle presets.
	Font         string               `json:"font,omitempty"`
	Position     string               `json:"position,omitempty"`
	Size         float64              `json:"size,omitempty"`
	Color        string               `json:"color,omitempty"`
	FontSizePX   float64              `json:"font_size_px,omitempty"`
	WidthPX      int                  `json:"width_px,omitempty"`
	HeightPX     int                  `json:"height_px,omitempty"`
	ScalePercent float64              `json:"scale_percent,omitempty"`
	Stroke       *VideoStrokeSpec     `json:"stroke,omitempty"`
	Shadow       *VideoShadowSpec     `json:"shadow,omitempty"`
	TransitionIn *VideoTransitionSpec `json:"transition_in,omitempty"`
}

// VideoStrokeSpec is the canonical solid outline block shared by every video
// text layer. Color is a CSS-style hex string ("#RRGGBB"); Width is in
// render pixels. When a layer declares only a shadow, the plan boundary
// derives a stroke from the shadow color so subtitle text always carries a
// readable black contour; an explicit stroke always wins over that fallback.
type VideoStrokeSpec struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}

// VideoShadowSpec is the canonical drop-shadow block shared by every video
// layer. Color is a CSS-style hex string ("#RRGGBB"); offsets are in pixels.
type VideoShadowSpec struct {
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	BlurPX  float64 `json:"blur_px,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

// VideoTransitionSpec is the canonical entry-animation block for a video
// layer. Preset selects the animation intent (e.g. fade_in); DurationMS is
// the animation length in milliseconds.
type VideoTransitionSpec struct {
	Preset     string `json:"preset,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type VideoWatermarkSpec struct {
	Enabled  bool                  `json:"enabled,omitempty"`
	Text     string                `json:"text,omitempty"`
	AssetID  string                `json:"asset_id,omitempty"`
	Position string                `json:"position,omitempty"`
	Opacity  float64               `json:"opacity,omitempty"`
	MarginPX int                   `json:"margin_px,omitempty"`
	Style    *VideoVisualStyleSpec `json:"style,omitempty"`
}

type VideoSubtitlesSpec struct {
	Enabled bool                  `json:"enabled,omitempty"`
	Mode    string                `json:"mode,omitempty"`
	StyleID string                `json:"style_id,omitempty"`
	Preset  string                `json:"preset,omitempty"`
	Style   *VideoVisualStyleSpec `json:"style,omitempty"`
}

// normalizeSubtitlePreset resolves a named preset through the preset table.
// The table is the single preset registry consulted by Normalize; adding a
// preset means adding one entry here (or in SubtitlePresets), never another
// hardcoded branch.
var canonicalSubtitlePresets = map[string]VideoVisualStyleSpec{
	"impact":          {Font: "Impact", FontSizePX: 58},
	"anton":           {Font: "Anton", FontSizePX: 56},
	"bebas":           {Font: "Bebas Neue", FontSizePX: 60},
	"bebas_neue":      {Font: "Bebas Neue", FontSizePX: 60},
	"bebas neue":      {Font: "Bebas Neue", FontSizePX: 60},
	"roboto":          {Font: "Roboto", FontSizePX: 52},
	"roboto_bold":     {Font: "Roboto", FontSizePX: 52},
	"montserrat":      {Font: "Montserrat", FontSizePX: 54},
	"montserrat_bold": {Font: "Montserrat", FontSizePX: 54},
	// "Young" family — generate-time projection of the ASS typography in
	// assets/texttracks/ass_materializer.go::ResolveFontPreset (same ids by
	// substring); sizes must agree with the burnt ASS preset.
	"subs-young":       {Font: "Poppins", FontSizePX: 60},
	"subs-young-pop":   {Font: "Poppins", FontSizePX: 64},
	"subs-young-clean": {Font: "Montserrat", FontSizePX: 56}, "subs-young-center": {Font: "Poppins", FontSizePX: 60, Position: "middle_center"},
}

// IsValidSubtitlePreset reports whether id names a CANONICAL subtitle preset
// (the table above). Request-local presets are deliberately out of scope: they
// live inside one request and a durable channel profile can never reference
// them. It exists so a durable profile naming a preset this build does not
// ship is rejected at load time instead of silently falling back to another
// typography at render time.
func IsValidSubtitlePreset(id string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(id))
	if trimmed == "" {
		return false
	}
	_, ok := canonicalSubtitlePresets[trimmed]
	return ok
}

// ResolveSubtitleStyle projects a canonical subtitle preset (by id) UNDER an
// inline style override (explicit inline fields win), the same precedence rule
// Normalize applies. Empty presetID returns the inline style untouched; an
// unknown preset fails closed. Color/font/position defaulting stays in
// VideoRenderSpec.Normalize, AFTER this merge.
func ResolveSubtitleStyle(presetID string, inline *VideoVisualStyleSpec) (*VideoVisualStyleSpec, error) {
	id := strings.ToLower(strings.TrimSpace(presetID))
	if id == "" {
		return inline, nil
	}
	preset, ok := canonicalSubtitlePresets[id]
	if !ok {
		return nil, fmt.Errorf("script: unknown subtitle preset %q", strings.TrimSpace(presetID))
	}
	merged := mergeSubtitlePreset(inline, preset)
	return &merged, nil
}

// Normalize preserves the caller's explicit choices and enables the video
// path whenever the background is a real layer or either requested overlay
// is enabled. Empty values receive the same safe defaults as clip.render.
func (r *VideoRenderSpec) Normalize() {
	if r == nil {
		return
	}
	if r.RenderConcurrency < 1 {
		r.RenderConcurrency = 2
	}
	if r.ForegroundScalePercent < 0 || r.ForegroundScalePercent > 100 {
		r.ForegroundScalePercent = 100
	}
	if r.Background != nil {
		if r.Background.Mode == "" {
			r.Background.Mode = "none"
		}
		if r.Background.Mode != "none" {
			r.Enabled = true
		}
	}
	if r.Watermark != nil && r.Watermark.Enabled {
		r.Enabled = true
		if r.Watermark.Position == "" {
			r.Watermark.Position = "top_right"
		}
		if r.Watermark.Opacity == 0 {
			r.Watermark.Opacity = 1
		}
		if r.Watermark.MarginPX <= 0 {
			r.Watermark.MarginPX = 40
		}
		if r.Watermark.Style == nil {
			r.Watermark.Style = &VideoVisualStyleSpec{}
		}
		if r.Watermark.Style.Color == "" {
			r.Watermark.Style.Color = "#FFFFFF"
		}
		if r.Watermark.Style.FontSizePX == 0 {
			if r.Watermark.Style.Size > 0 {
				r.Watermark.Style.FontSizePX = r.Watermark.Style.Size
			} else {
				r.Watermark.Style.FontSizePX = 42
			}
		}
	}
	if r.Subtitles != nil && r.Subtitles.Enabled {
		r.Enabled = true
		if r.Subtitles.Mode == "" {
			r.Subtitles.Mode = "burn"
		}
		presetID := strings.ToLower(strings.TrimSpace(r.Subtitles.Preset))
		if presetID == "" {
			presetID = strings.ToLower(strings.TrimSpace(r.Subtitles.StyleID))
		}
		if r.Subtitles.Style == nil {
			r.Subtitles.Style = &VideoVisualStyleSpec{}
		}
		if r.Subtitles.Style.Font == "" && presetID != "" {
			// Named presets resolve through the single preset registry —
			// canonical table first, then request-local presets. Explicit
			// fields in an inline style remain authoritative: in particular,
			// a caller-provided stroke/shadow must not disappear merely because
			// the font and size came from a named preset.
			if preset, ok := canonicalSubtitlePresets[presetID]; ok {
				style := mergeSubtitlePreset(r.Subtitles.Style, preset)
				r.Subtitles.Style = &style
			} else if preset, ok := r.SubtitlePresets[presetID]; ok {
				style := mergeSubtitlePreset(r.Subtitles.Style, preset)
				r.Subtitles.Style = &style
			}
		}
		if r.Subtitles.Style.Color == "" {
			r.Subtitles.Style.Color = "#FFFFFF"
		}
		if r.Subtitles.Style.Font == "" {
			r.Subtitles.Style.Font = "Montserrat"
		}
		if r.Subtitles.Style.Position == "" {
			r.Subtitles.Style.Position = "bottom_center"
		}
		if r.Subtitles.Style.FontSizePX == 0 {
			if r.Subtitles.Style.Size > 0 {
				r.Subtitles.Style.FontSizePX = r.Subtitles.Style.Size
			} else {
				r.Subtitles.Style.FontSizePX = 54
			}
		}
	}
}

// ── the preset font must render the language it burns ────────────────
//
// The shipped subtitle fonts do not all cover the same writing systems.
// `Poppins-Bold.ttf` (471 glyphs) carries NO Cyrillic code point at all, while
// `Montserrat-Bold.ttf` (1312 glyphs) carries 222 of them. A preset that asks
// for Poppins therefore produces no glyphs for a Russian subtitle — and with
// the GPU-native text policy (a render plan whose output requires GPU-native
// work, e.g. require_gpu=true) the renderer treats the resulting empty glyph
// run as an UnsupportedCapability error instead of falling back to its software
// text path. That is a whole-run failure, and it landed mid-render (observed at
// frame 283 of a 25 s clip), long after every plan, track and folder had been
// resolved correctly.
//
// The languages of a fan-out are known BEFORE the render, so the font is chosen
// per language where the plan is built, and the swap is explicit: the plan
// carries the font it actually burns, and SubtitleStyleHash (which the ASS
// compiler resolves the preset font from) is derived from that same effective
// style, so the ASS style id and the materialized font cannot disagree.
//
// The declared coverage below is verified against the real font files by
// subtitle_font_coverage_test.go, which parses the shipped cmap tables: a font
// swap in assets/fonts cannot leave this table lying.

// FontScript names a writing system subtitles are rendered in. Only the scripts
// the shipped fonts distinguish are modelled: every declared font covers Latin,
// and Cyrillic is the one script where the presets genuinely differ.
type FontScript string

const (
	ScriptLatin    FontScript = "latin"
	ScriptCyrillic FontScript = "cyrillic"
)

// LocalizationSubtitleStyleHash is the canonical ASS style + generator hash
// prefix. The full style id is this prefix plus the effective font slug
// ("vidrush-default-montserrat"), which is what
// assets/texttracks/ass_materializer.go::ResolveFontPreset resolves the burnt
// font from.
const LocalizationSubtitleStyleHash = "vidrush-default"

// cyrillicLanguages is the BCP-47 base tag of every language the supported set
// writes in Cyrillic. Anything absent is treated as Latin: Latin is the baseline
// the declared fonts all cover, so an unknown language keeps the caller's font
// instead of being swapped on a guess.
var cyrillicLanguages = map[string]bool{
	"ru": true, "uk": true, "be": true, "bg": true, "sr": true, "mk": true,
	"mn": true, "kk": true, "ky": true, "tg": true, "tt": true, "ba": true,
	"cv": true, "ce": true, "os": true, "ab": true, "av": true, "sah": true,
	"udm": true, "kv": true, "kbd": true, "lez": true, "mhr": true, "myv": true,
	"cu": true, "hy": true,
}

// SubtitleFontAsset identifies a font FILE the clip render can burn. Coverage
// belongs to the file, not to the family label the ASS header carries: the
// renderer rasterizes the asset, so a declaration keyed by family name could
// certify a font whose file is never used.
type SubtitleFontAsset string

const (
	// SubtitleFontAssetMontserrat mirrors renderinggen.FontMontserratBold.
	SubtitleFontAssetMontserrat SubtitleFontAsset = "font-montserrat-bold"
	// SubtitleFontAssetPoppins mirrors renderinggen.FontPoppinsBold.
	SubtitleFontAssetPoppins SubtitleFontAsset = "font-poppins-bold"
)

// SubtitleFontAssetForStyle projects a subtitle style onto the font asset the
// clip-render plan will burn for it. It mirrors the projection owned by
// internal/platform/renderinggen (clip_plan_mapper.go::fontAssetID): a style
// whose font names Poppins burns Poppins-Bold.ttf, EVERY other font burns
// Montserrat-Bold.ttf. The mirror is deliberate — the coverage decision must be
// about the file that is rasterized — and the two sides are pinned together by
// renderinggen's font-asset test, so neither rule can drift alone.
func SubtitleFontAssetForStyle(style *VideoVisualStyleSpec) SubtitleFontAsset {
	if style != nil && strings.Contains(strings.ToLower(strings.TrimSpace(style.Font)), "poppins") {
		return SubtitleFontAssetPoppins
	}
	return SubtitleFontAssetMontserrat
}

// subtitleFontScriptCoverage declares, per burnable font ASSET, the scripts its
// glyphs cover. Because the projection above is TOTAL (every font name resolves
// to one of these two shipped files), every style has a declared answer: there
// is no "unknown font" left for this guard to guess about. The previous shape
// keyed coverage by family name and treated anything absent as covered, which
// silently certified Impact/Anton/Bebas/Roboto styles for Cyrillic on the
// strength of a label no renderer burns — and would have kept them certified if
// one of those families ever became the effective font.
//
// The declaration is verified against the real .ttf cmap tables by
// subtitle_font_coverage_test.go, which parses the shipped assets: swapping a
// font file cannot leave this table (and the renderer) disagreeing.
var subtitleFontScriptCoverage = map[SubtitleFontAsset]map[FontScript]bool{
	SubtitleFontAssetMontserrat: {ScriptLatin: true, ScriptCyrillic: true},
	SubtitleFontAssetPoppins:    {ScriptLatin: true},
}

// subtitleFontsByScript is the preference order used to replace a font that
// cannot render the target script. The canonical clip font (Montserrat) leads
// because it is the deployment default and the one shipped asset guaranteed to
// carry Cyrillic glyphs.
var subtitleFontsByScript = map[FontScript][]string{
	ScriptCyrillic: {"Montserrat", "Poppins"},
	ScriptLatin:    {"Montserrat", "Poppins"},
}

// LanguageFontScript returns the writing system a language's subtitles are
// rendered in. Region and case variants are folded, so "ru-RU" and "RU" are the
// same script as "ru".
func LanguageFontScript(language string) FontScript {
	base := strings.ToLower(strings.TrimSpace(language))
	if i := strings.IndexAny(base, "-_"); i > 0 {
		base = base[:i]
	}
	if cyrillicLanguages[base] {
		return ScriptCyrillic
	}
	return ScriptLatin
}

// FontCoversScript reports whether the file a font name projects to can render
// a script. The second result stays in the signature because a coverage gap
// must still fail closed rather than invent coverage; with the total projection
// in place it is true for every input the render path can produce, and the
// false branch is reached only if a style ever projects outside the shipped
// assets (the test below pins that unreachable-by-construction property).
func FontCoversScript(font string, script FontScript) (covers bool, known bool) {
	scripts, ok := subtitleFontScriptCoverage[SubtitleFontAssetForStyle(&VideoVisualStyleSpec{Font: font})]
	if !ok {
		return false, false
	}
	return scripts[script], true
}

// FontCoversLanguage reports whether a font renders the script of a language.
// The question is answered through the projected asset, so the guard swaps
// exactly the fonts proven unable to burn the language, and never swaps one on
// a guess about a family label.

// SubtitleStyleHash is the canonical ASS style id for a subtitle style: the
// base hash plus the effective font slug. A style with no font keeps the bare
// base hash. This is the only owner of that rule; the localization wiring and
// the per-language plan builder both derive the id through it.
func SubtitleStyleHash(style *VideoVisualStyleSpec) string {
	if style == nil || strings.TrimSpace(style.Font) == "" {
		return LocalizationSubtitleStyleHash
	}
	return fmt.Sprintf("%s-%s", LocalizationSubtitleStyleHash, strings.ToLower(strings.TrimSpace(style.Font)))
}

// EnsureSubtitleFontForLanguage returns the subtitle style to burn for a
// language: the caller's style when its font can render the language, else a
// copy of it whose font is the first declared font that covers the script. The
// second result reports whether a swap happened, so the caller can record the
// substitution. A nil/absent font is never swapped (the preset default fills it
// in downstream), and a language whose script no shipped font covers fails
// closed with a typed error instead of reaching the renderer with no glyphs.
func EnsureSubtitleFontForLanguage(style *VideoVisualStyleSpec, language string) (*VideoVisualStyleSpec, bool, error) {
	if style == nil || strings.TrimSpace(style.Font) == "" {
		return style, false, nil
	}
	script := LanguageFontScript(language)
	covers, known := FontCoversScript(style.Font, script)
	if !known || covers {
		return style, false, nil
	}
	for _, font := range subtitleFontsByScript[script] {
		if covers, known := FontCoversScript(font, script); known && covers {
			swapped := *style
			swapped.Font = font
			return &swapped, true, nil
		}
	}
	return nil, false, fmt.Errorf(
		"subtitle font %q cannot render the %s script required by language %q and no shipped font covers it",
		style.Font, script, language)
}
