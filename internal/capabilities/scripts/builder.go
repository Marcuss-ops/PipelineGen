// Package scriptgeneration — builder.go is the canonical pure
// transformation layer that converts a validated request envelope
// into a typed GenerateRequest.
//
// Verdetto invariant:
//
//	func BuildGenerateRequest(env *GenerationEnvelopeV2, idempotencyKey string) (GenerateRequest, error)
//
// Zero I/O — no network, no database, no Google Drive. The builder
// is demoted from the original ingress registry which called
// TranslateScenes inline.
//
// The canonical caller is the HTTP handler (HandlerGenerate) which
// previously built a SubmitRequest directly from the envelope. After
// this change the handler calls:
//
//	req, err := scriptgeneration.BuildGenerateRequest(env, idempotencyKey)
//	// then: svc.Start(ctx, req)
//
// No Ollama, no Drive. Pure field mapping only.
package scriptgeneration

import (
	"errors"
	"fmt"
	"log"
	"strings"

	audiocap "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/channelprofile"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildGenerateRequest is the sole canonical builder for
// GenerateRequest. It accepts a validated GenerationEnvelopeV2
// and produces the domain-level request with zero side effects.
//
// Verdetto rules:
//   - NO translation.TranslateScenes(ctx, ...)
//   - NO Ollama, NO database, NO Drive
//   - Returns only struct-literal construction + field mapping
//
// Source of truth: the first item in the envelope defines the
// primary generation parameters. Multi-item envelopes are treated
// as batch — the primary item configures the top-level parameters
// and each item is handled independently downstream.
func BuildGenerateRequest(env *scriptpkg.GenerationEnvelopeV2, idempotencyKey string) (contract.GenerateRequest, error) {
	if env == nil {
		return contract.GenerateRequest{}, fmt.Errorf("scriptgeneration: envelope is nil")
	}
	if len(env.Items) == 0 {
		return contract.GenerateRequest{}, fmt.Errorf("scriptgeneration: envelope has no items")
	}

	item, err := normalizeBuilderItem(env, idempotencyKey)
	if err != nil {
		return contract.GenerateRequest{}, err
	}
	// SSOT: output.render.watermark / output.render.subtitles are the only
	// spellings. The legacy top-level output.watermark/output.subtitles
	// compatibility blocks were removed; requests carrying them fail at the
	// schema boundary instead of silently merging two sources of truth.

	source := buildBuilderSource(item)

	languages, sourceLang, err := resolveBuilderTranslation(item)
	if err != nil {
		return contract.GenerateRequest{}, err
	}

	generateTimeline, audio, err := resolveBuilderAudio(item, languages, sourceLang)
	if err != nil {
		return contract.GenerateRequest{}, err
	}

	mapCfg, err := resolveBuilderMapConfig(item)
	if err != nil {
		return contract.GenerateRequest{}, err
	}

	req := buildGenerateRequestCore(item, env, idempotencyKey, source, languages, sourceLang, mapCfg)
	applyBuilderOutput(&req, item, generateTimeline, audio)
	req.ScriptParams.Segments = scriptpkg.CloneScriptSegments(item.ScriptParams.Segments)
	// Channel profile: curated per-channel defaults for the choices this
	// request left BLANK (subtitles, watermark, overlay style, SFX, mix
	// policy, phrase-motion pool). Applied BEFORE the editing asset policy so
	// the policy can still fill whatever the profile — like the caller — left
	// unset. An unknown/empty channel is a no-op: profiles are curated, not a
	// closed vocabulary.
	if profile, ok := channelprofile.Lookup(item.ChannelID); ok {
		if err := ApplyChannelProfile(&req, profile); err != nil {
			return contract.GenerateRequest{}, fmt.Errorf("scriptgeneration: apply channel profile: %w", err)
		}
	}
	// Background centralizzato ON (Intro V2): the canonical editorial
	// selection policy fills the assets the caller left blank (background
	// plate, BGM in COMBINED_TIMELINE). It NEVER overrides a
	// caller-provided selection — an explicit mode (including "none") is
	// preserved — so this is a pure default-fill at the single ingress
	// point both job handlers share.
	if err := ApplyEditingAssetPolicy(&req, mediaregistry.DefaultEditingAssetsPolicy()); err != nil {
		return contract.GenerateRequest{}, fmt.Errorf("scriptgeneration: apply editing asset policy: %w", err)
	}
	// Every request carries the certified entity-image and caption pools. The
	// Rust planner rejects an admitted entity image or caption when these pools
	// are empty; an omitted pool means "automatic rotation", not "no motion",
	// so resolve them once here instead of failing after script, translation
	// and voiceover work.
	req.EntityImageMotions = resolveEntityImageMotionPool(req.EntityImageMotions)
	req.EntityCaptionMotions = resolveEntityCaptionMotionPool(req.EntityCaptionMotions)
	// Three knobs describe the same generic-image lane: output.generate_scene_images,
	// script_params.images_per_scene and max_image_overlays. Resolve them once so a
	// payload cannot say "disabled" twice and still arm the lane through a stale
	// budget (CFG-05).
	normalizeImageToggleCoherence(&req)
	return req, nil
}

// normalizeBuilderItem validates the envelope shape and applies the input
// normalizations that must happen before any field is projected.
func normalizeBuilderItem(env *scriptpkg.GenerationEnvelopeV2, idempotencyKey string) (scriptpkg.GenerationItemV2, error) {
	if env == nil {
		return scriptpkg.GenerationItemV2{}, fmt.Errorf("scriptgeneration: envelope is nil")
	}
	if len(env.Items) == 0 {
		return scriptpkg.GenerationItemV2{}, fmt.Errorf("scriptgeneration: envelope has no items")
	}
	item := env.Items[0]
	// Envelope-level force refresh is authoritative across every cache layer,
	// not only the job/idempotency boundary.
	if env.ForceRefresh {
		item.ScriptParams.ForceRefresh = true
		item.Source.ForceRefresh = true
		item.MediaPlan.ForceRefreshAssets = true
		item.MediaPlan.ForceRefreshExtraction = true
		item.MediaPlan.ForceRefreshBindings = true
	}
	// Rendered clips are grouped under the generated script name. The default
	// is run-scoped so two submissions with the same title never publish into
	// one folder; an explicit caller folder remains authoritative.
	if strings.TrimSpace(item.Output.Render.DriveSubfolderName) == "" {
		item.Output.Render.DriveSubfolderName = defaultRenderSubfolderName(item.Title, item.Source.Topic, idempotencyKey)
	}
	if err := resolveBackgroundReference(&item.Output.Render); err != nil {
		return scriptpkg.GenerationItemV2{}, fmt.Errorf("scriptgeneration: resolve render background: %w", err)
	}
	item.Output.Render.Normalize()
	// output.drive_folder_id is the job's explicit artifact root. Keep it
	// connected to the render contract as well.
	if strings.TrimSpace(item.Output.Render.DriveFolderID) == "" {
		item.Output.Render.DriveFolderID = item.Output.DriveFolderID
	}
	return item, nil
}

// resolveBuilderTranslation validates the translation contract and resolves
// the deduplicated target languages plus the source language.
func resolveBuilderTranslation(item scriptpkg.GenerationItemV2) ([]contract.Language, contract.Language, error) {
	provider := strings.ToLower(strings.TrimSpace(item.Translation.Provider))
	if provider != "" && provider != "ollama" && provider != "argos" {
		return nil, "", fmt.Errorf("scriptgeneration: unsupported translation provider %q", item.Translation.Provider)
	}
	if item.Translation.Concurrency < 0 || item.Translation.Concurrency > 16 {
		return nil, "", fmt.Errorf("scriptgeneration: translation concurrency must be between 0 and 16")
	}
	languages := make([]contract.Language, 0, len(item.Output.Languages))
	seenLanguages := make(map[contract.Language]struct{}, len(item.Output.Languages))
	for _, raw := range item.Output.Languages {
		lang := contract.Language(strings.TrimSpace(raw))
		if lang == "" {
			continue
		}
		if _, exists := seenLanguages[lang]; exists {
			return nil, "", fmt.Errorf("scriptgeneration: output.languages repeats %q", lang)
		}
		seenLanguages[lang] = struct{}{}
		languages = append(languages, lang)
	}
	sourceLang := contract.Language(strings.TrimSpace(item.Language))
	if sourceLang == "" {
		sourceLang = "en"
	}
	if provider != "" {
		hasTarget := false
		for _, lang := range languages {
			if lang != sourceLang {
				hasTarget = true
				break
			}
		}
		if !hasTarget {
			return nil, "", fmt.Errorf("scriptgeneration: translation provider %q is configured but output.languages has no target different from source language %q; add the target language or remove translation.provider", provider, sourceLang)
		}
	}
	return languages, sourceLang, nil
}

// buildBuilderSource projects SourceSpec into the durable source contract.
func buildBuilderSource(item scriptpkg.GenerationItemV2) contract.Source {
	return contract.Source{
		Type:               contract.SourceType(item.Source.Type),
		Topic:              item.Source.Topic,
		SourceText:         item.Source.SourceText,
		Guidelines:         item.Source.Guidelines,
		ClipIDs:            copyStrings(item.Source.ClipIDs),
		NumClips:           item.Source.NumClips,
		Query:              item.Source.Query,
		MaxClips:           item.Source.MaxClips,
		MinCoverage:        item.Source.MinCoverage,
		MinQualityScore:    cloneFloat64(item.Source.MinQualityScore),
		MinTranscriptWords: cloneInt(item.Source.MinTranscriptWords),
		TranscriptPolicy:   item.Source.TranscriptPolicy,
		OrderingStrategy:   item.Source.OrderingStrategy,
		GroundingPolicy:    item.Source.GroundingPolicy,
		FallbackPolicy:     item.Source.FallbackPolicy,
		ForceRefresh:       item.Source.ForceRefresh,
		Search:             item.Source.Search,
		AllowTextOnly:      item.Source.AllowTextOnly,
		SourceFilter:       item.Source.SourceFilter,
		MediaTypeFilter:    item.Source.MediaTypeFilter,
		CachePolicy:        item.Source.CachePolicy,
		Research:           item.Source.Research,
	}
}

// builderAudioIntent is the validated audio projection consumed by the
// request builder. It keeps the compat fallbacks in one place.
type builderAudioIntent struct {
	mode                     audiocap.AudioMode
	timing                   *audiocap.TimingRequest
	mixPolicy                audiocap.AudioMixPolicy
	backgroundMusic          []scriptpkg.BackgroundMusicIntent
	soundEffects             []scriptpkg.SoundEffectIntent
	clipStartSoundEffects    []scriptpkg.ClipStartSoundEffectIntent
	randomSFXOnClipStart     bool
	randomSFXOnImageOverlay  bool
	randomSFXOnPhraseOverlay bool
	voiceoverLanguages       []contract.Language
}

// resolveBuilderAudio validates the audio mode and resolves the canonical
// audio intent, including the nested output.audio compatibility fallbacks.
func resolveBuilderAudio(item scriptpkg.GenerationItemV2, languages []contract.Language, sourceLang contract.Language) (bool, builderAudioIntent, error) {
	audioModeInput := item.Audio.Mode
	if audioModeInput == "" {
		audioModeInput = item.Output.Audio.Mode
	}
	audioMode, err := audiocap.ResolveAudioMode(audiocap.AudioMode(audioModeInput), item.Output.VoiceoverEnabled.AsBool())
	if err != nil {
		return false, builderAudioIntent{}, fmt.Errorf("scriptgeneration: %w", err)
	}
	needsVoiceover := audioMode == audiocap.AudioModeChunkedVoiceover || audioMode == audiocap.AudioModeCombinedTimeline
	if needsVoiceover && strings.TrimSpace(item.Project) == "" {
		return false, builderAudioIntent{}, fmt.Errorf("%w: voiceover publishing requires a resolved Project", ErrProjectRequired)
	}
	timing := item.Audio.Timing
	if timing == nil {
		timing = item.Output.Audio.Timing
	}
	mixPolicy := item.Audio.MixPolicy
	if mixPolicy == "" {
		mixPolicy = item.Output.Audio.MixPolicy
	}
	backgroundMusic := item.Audio.BackgroundMusic
	if backgroundMusic == nil {
		backgroundMusic = item.Output.Audio.BackgroundMusic
	}
	soundEffects := item.Audio.SoundEffects
	if soundEffects == nil {
		soundEffects = item.Output.Audio.SoundEffects
	}
	clipStartSoundEffects := item.Audio.ClipStartSoundEffects
	if clipStartSoundEffects == nil {
		clipStartSoundEffects = item.Output.Audio.ClipStartSoundEffects
	}
	voiceoverLanguages := item.Audio.VoiceoverLanguages
	if voiceoverLanguages == nil {
		voiceoverLanguages = item.Output.Audio.VoiceoverLanguages
	}
	if needsVoiceover && voiceoverLanguages != nil {
		if len(voiceoverLanguages) == 0 {
			return false, builderAudioIntent{}, fmt.Errorf("scriptgeneration: audio.voiceover_languages must not be empty when audio is enabled")
		}
		allowed := make(map[string]struct{}, len(languages)+1)
		allowed[string(sourceLang)] = struct{}{}
		for _, lang := range languages {
			allowed[string(lang)] = struct{}{}
		}
		for _, lang := range voiceoverLanguages {
			if _, ok := allowed[lang]; !ok {
				return false, builderAudioIntent{}, fmt.Errorf("scriptgeneration: audio.voiceover_languages entry %q must be the source language or included in output.languages", lang)
			}
		}
	}
	return item.Output.GenerateTimeline, builderAudioIntent{
		mode:                     audioMode,
		timing:                   timing,
		mixPolicy:                mixPolicy,
		backgroundMusic:          backgroundMusic,
		soundEffects:             soundEffects,
		clipStartSoundEffects:    clipStartSoundEffects,
		randomSFXOnClipStart:     item.Audio.RandomSFXOnClipStart || item.Output.Audio.RandomSFXOnClipStart,
		randomSFXOnImageOverlay:  item.Audio.RandomSFXOnImageOverlay || item.Output.Audio.RandomSFXOnImageOverlay,
		randomSFXOnPhraseOverlay: item.Audio.RandomSFXOnPhraseOverlay || item.Output.Audio.RandomSFXOnPhraseOverlay,
		voiceoverLanguages:       toLanguages(voiceoverLanguages),
	}, nil
}

// builderMapConfig is the resolved map presentation and its legacy
// compatibility projection.
type builderMapConfig struct {
	typ, camera, label, animation, level string
	presentation                         *scriptpkg.MapPresentation
}

// resolveBuilderMapConfig resolves the single map_presentation contract and
// rejects mixed legacy spellings instead of guessing a winner.
func resolveBuilderMapConfig(item scriptpkg.GenerationItemV2) (builderMapConfig, error) {
	cfg := builderMapConfig{
		typ: item.MapType, camera: item.MapCameraAnimation,
		label: item.MapLabelAnimation, animation: item.MapAnimation,
	}
	if item.MapPresentation == nil {
		return cfg, nil
	}
	if strings.TrimSpace(cfg.typ) != "" || strings.TrimSpace(cfg.camera) != "" ||
		strings.TrimSpace(cfg.label) != "" || strings.TrimSpace(cfg.animation) != "" {
		return builderMapConfig{}, fmt.Errorf("scriptgeneration: map_presentation is the single map configuration contract; remove the legacy map_type/map_camera_animation/map_label_animation/map_animation fields")
	}
	spec := *item.MapPresentation
	if err := resolveMapPresentationPresetSpec(&spec); err != nil {
		return builderMapConfig{}, fmt.Errorf("scriptgeneration: %w", err)
	}
	resolved, err := spec.Normalize()
	if err != nil {
		return builderMapConfig{}, fmt.Errorf("scriptgeneration: %w", err)
	}
	if len(resolved.AnimationPool) == 0 {
		resolved.AnimationPool = capabilityoverlay.MapCompositionRandomPool()
	}
	if len(resolved.StylePool) == 0 {
		resolved.StylePool = capabilityoverlay.NativeMapBasemapPool()
	}
	if err := validateMapPresentationPools(resolved); err != nil {
		return builderMapConfig{}, fmt.Errorf("scriptgeneration: %w", err)
	}
	cfg.presentation = &resolved
	cfg.typ = resolved.Style
	cfg.level = resolved.Level
	cfg.label = "none"
	cfg.animation = resolved.Animation
	if resolved.Camera != "auto" {
		cfg.camera = resolved.Camera
	}
	return cfg, nil
}

// buildGenerateRequestCore projects the identity, source, media and overlay
// fields. Output/audio fields are applied by applyBuilderOutput.
func buildGenerateRequestCore(item scriptpkg.GenerationItemV2, env *scriptpkg.GenerationEnvelopeV2, idempotencyKey string, source contract.Source, languages []contract.Language, sourceLang contract.Language, mapCfg builderMapConfig) contract.GenerateRequest {
	req := contract.GenerateRequest{
		Model:                         item.Model,
		TranslationProvider:           strings.ToLower(strings.TrimSpace(item.Translation.Provider)),
		TranslationModel:              strings.TrimSpace(item.Translation.Model),
		TranslationConcurrency:        item.Translation.Concurrency,
		Tone:                          item.Tone,
		Style:                         item.Style,
		Render:                        item.Output.Render,
		OverlayBackground:             item.OverlayBackground,
		OverlayStyle:                  item.OverlayStyle,
		MapType:                       mapCfg.typ,
		MapCameraAnimation:            mapCfg.camera,
		MapLabelAnimation:             mapCfg.label,
		MapAnimation:                  mapCfg.animation,
		MapLevel:                      mapCfg.level,
		MapPresentation:               mapCfg.presentation,
		ShortPhraseMotion:             strings.TrimSpace(item.ShortPhraseMotion),
		PhraseMotionFamily:            item.PhraseMotionFamily,
		EditorialBulletStyles:         append([]scriptpkg.EditorialBulletStyle(nil), item.EditorialBulletStyles...),
		DateMotionFamily:              item.DateMotionFamily,
		MotionSeed:                    strings.TrimSpace(item.MotionSeed),
		ImageMotions:                  append([]string(nil), item.ImageMotions...),
		ImageWithTextMotions:          append([]string(nil), item.ImageWithTextMotions...),
		EntityImageMotions:            append([]string(nil), item.EntityImageMotions...),
		EntityCaptionMotions:          append([]string(nil), item.EntityCaptionMotions...),
		RandomLightLeakOnClipStart:    item.RandomLightLeakOnClipStart,
		RandomLightLeakOnOverlayStart: item.RandomLightLeakOnOverlayStart,
		RandomImageEffects:            item.RandomImageEffects,
		MaxPhraseOverlays:             item.MaxPhraseOverlays,
		MaxOverlaysPerScene:           item.MaxOverlaysPerScene,
		MapsOnly:                      item.MapsOnly,
		MaxMapOverlays:                item.MaxMapOverlays,
		MaxImageOverlays:              item.MaxImageOverlays,
		EditorialDensity:              strings.TrimSpace(item.EditorialDensity),
		AnimationCounts:               cloneAnimationCounts(item.AnimationCounts),
		OverlayGroupLimits:            cloneOverlayGroupLimits(item.OverlayGroupLimits),
		EntityStyleID:                 strings.TrimSpace(item.EntityStyleID),
		DisableNumberOverlays:         item.DisableNumberOverlays,
		DisableRecapOverlays:          item.DisableRecapOverlays,
		DisableChartOverlays:          item.DisableChartOverlays,
		DisableEditorialOverlays:      item.DisableEditorialOverlays,
		EnableCharts:                  item.EnableCharts,
		HeavyPhrasePriority:           item.HeavyPhrasePriority,
		OverlayCategories:             scriptpkg.CloneOverlayCategories(item.OverlayCategories),
		OverlayImages:                 append([]scriptpkg.OverlayImageAsset(nil), item.OverlayImages...),
		IdempotencyKey:                idempotencyKey,
		ForceRefresh:                  env.ForceRefresh,
		FinalJob:                      env.FinalJob,
		Source:                        source,
		MediaMode:                     item.MediaMode,
		StockBindings:                 append([]scriptpkg.StockBindingInput(nil), item.Output.StockBindings...),
		ScriptParams:                  item.ScriptParams,
		MediaPlan:                     item.MediaPlan.Clone(),
		ExtractEntities:               item.Output.ExtractEntities,
		GenerateSceneImages:           item.Output.GenerateSceneImages,
		SourceLanguage:                sourceLang,
		Languages:                     languages,
		ChannelID:                     item.ChannelID,
	}
	return req
}

// applyBuilderOutput projects the output, project, documentation and audio
// fields onto the request built from the envelope item.
func applyBuilderOutput(req *contract.GenerateRequest, item scriptpkg.GenerationItemV2, generateTimeline bool, audio builderAudioIntent) {
	req.GenerateTimeline = generateTimeline
	req.Timing = audio.timing
	req.VoiceoverLanguages = audio.voiceoverLanguages
	req.Docs = item.Docs
	req.DocsEnabled = item.Docs.Enabled
	req.SaveToDB = item.Output.SaveToDB
	req.Title = item.Title
	req.Intro = scriptpkg.CloneFixedSection(item.Intro)
	req.Outro = scriptpkg.CloneFixedSection(item.Outro)
	req.Project = item.Project
	req.OutputName = item.Title
	req.VoiceoverFolderID = item.Output.VoiceoverFolderID
	req.TTSRate = item.Output.TTSRate
	req.Audio = audio.mode
	req.MixPolicy = audio.mixPolicy
	req.BackgroundMusic = audio.backgroundMusic
	req.SoundEffects = audio.soundEffects
	req.ClipStartSoundEffects = audio.clipStartSoundEffects
	req.RandomSFXOnClipStart = audio.randomSFXOnClipStart
	req.RandomSFXOnImageOverlay = audio.randomSFXOnImageOverlay
	req.RandomSFXOnPhraseOverlay = audio.randomSFXOnPhraseOverlay
}

// normalizeImageToggleCoherence turns the explicit "no scene images" pair into
// a zero budget. The entity-image lane is independent and keeps its budget.
// A single disabled knob is not enough: the payload contract treats an omitted
// value as "keep the certified default", so only both-at-once is an opt-out.
func normalizeImageToggleCoherence(req *contract.GenerateRequest) {
	if req == nil || req.GenerateSceneImages != scriptpkg.ToggleDisabled || req.ScriptParams.ImagesPerScene != 0 {
		return
	}
	if req.MediaPlan.Extraction.EntityImages.Enabled || req.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return
	}
	if req.MaxImageOverlays <= 0 {
		return
	}
	log.Printf("scriptgeneration: output.generate_scene_images=disabled and script_params.images_per_scene=0; clearing max_image_overlays=%d for the generic image lane", req.MaxImageOverlays)
	req.MaxImageOverlays = 0
}

// defaultRenderSubfolderName returns the run-scoped Drive folder used when the
// caller did not name one. The idempotency-key suffix keeps two submissions of
// the same title in separate folders, while a retry of the same submission
// (same key) reuses its folder. Explicit caller folders are never rewritten.
func defaultRenderSubfolderName(title, topic, idempotencyKey string) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = strings.TrimSpace(topic)
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return base
	}
	// Digest SSOT: kernel/digest owns the algorithm (godlike/06); the first
	// four bytes of the SHA-256 stay byte-identical to the retired inline call.
	suffix := digest.SHA256String(key)[:8]
	if base == "" {
		return "run-" + suffix
	}
	return base + "-" + suffix
}

// ── Editorial asset SELECTION policy application ──────────────────────────
//
// The catalogs own WHICH editorial assets exist
// (internal/capabilities/mediaregistry); the policy owns WHICH of them a job
// picks when it does not name one. ApplyEditingAssetPolicy is the integration
// seam between the two: the pipeline decides WHEN to opt in, the policy decides
// WHAT is selected.
//
// This lives beside the builder because it is the same pure, zero-I/O request
// transformation surface: it mutates only the in-memory GenerateRequest.

// videoBackgroundModeAsset is the canonical clip-background mode literal owned
// by the cliprender capability (none | blur_source | asset).
const videoBackgroundModeAsset = "asset"

const (
	// overlayBackgroundPlateKind is the media family of every curated editorial
	// background plate (the video-background-v1 contract normalizes them all to
	// video-only MP4), so a plate selected for the overlay render carries the
	// same kind as one selected for the clip render.
	overlayBackgroundPlateKind = "video"
)

// resolveBackgroundReference converts a human channel label (or a friendly
// asset id such as "Boxe") into the canonical registry alias before the
// generic render normalizer applies its mode defaults. This keeps payloads
// readable while preserving the existing asset-id-only render contract.
func resolveBackgroundReference(render *scriptpkg.VideoRenderSpec) error {
	if render == nil || render.Background == nil {
		return nil
	}
	background := render.Background
	profile := strings.TrimSpace(background.Profile)
	assetID := strings.TrimSpace(background.AssetID)
	if profile != "" {
		asset, ok := mediaregistry.ResolveEditorialBackgroundReference(profile)
		if !ok {
			return fmt.Errorf("unknown background profile %q", profile)
		}
		if assetID != "" && !strings.EqualFold(assetID, asset.ID) {
			return fmt.Errorf("background profile %q conflicts with asset_id %q", profile, assetID)
		}
		if mode := strings.ToLower(strings.TrimSpace(background.Mode)); mode != "" && mode != videoBackgroundModeAsset {
			return fmt.Errorf("background profile %q cannot be used with mode %q", profile, background.Mode)
		}
		background.AssetID = asset.ID
		background.Mode = videoBackgroundModeAsset
		background.Profile = ""
		return nil
	}
	if assetID == "" {
		return nil
	}
	if asset, ok := mediaregistry.ResolveEditorialBackgroundReference(assetID); ok {
		background.AssetID = asset.ID
		if strings.TrimSpace(background.Mode) == "" {
			background.Mode = videoBackgroundModeAsset
		}
	}
	return nil
}

// ApplyEditingAssetPolicy fills the editorial assets a request left blank,
// using the canonical selection policy. It is OPT-IN: nothing calls it
// implicitly, and it NEVER overrides a caller-provided selection.
//
// Scope and rules:
//
//   - the clip background is filled only when the request left it blank (a
//     nil block, or an empty mode AND an empty asset_id). An explicit mode —
//     including "none" — is caller intent and is preserved.
//   - the overlay background is filled only when the request ALREADY enabled
//     rendering (req.Render.Enabled) and left the overlay background blank.
//     A background must never turn overlay rendering on by itself, and an
//     explicit kind (including "color", i.e. the deliberate black canvas) is
//     preserved.
//   - background music is filled only when the request declared no BGM AND the
//     request is in the COMBINED_TIMELINE audio mode. Injecting a BGM layer
//     into a job that never builds an audio plan would be a silent no-op.
//   - a transition SFX is NOT auto-placed: its position depends on the scene
//     timeline, which does not exist at request-build time. Callers that do
//     have scene context use policy.SelectTransitionSFX directly.
//
// Determinism: the seed is the video's STABLE CREATIVE IDENTITY (Project), not
// the submit. The idempotency key changes on every submission — a retry, a
// revision or a second run of the same payload is a new key — so seeding from
// it made the background plate and the BGM track change per run. Measured on
// the Garlasco payload: identical duration, three different audio plans across
// r64/r65/r67 (bgm3 vs bgm5), which re-mixed, re-encoded and could never hit
// the plan-keyed audio cache. Project is set before this call (builder
// request normalization) and is the same creative namespace the overlay style
// sampling and the render-cache digest already use; the idempotency key stays
// as the fallback for callers that declared no project.
func ApplyEditingAssetPolicy(req *contract.GenerateRequest, policy mediaregistry.EditingAssetsPolicy) error {
	if req == nil {
		return errors.New("apply editing asset policy: request is required")
	}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("apply editing asset policy: %w", err)
	}
	seed := strings.TrimSpace(req.Project)
	if seed == "" {
		seed = strings.TrimSpace(req.IdempotencyKey)
	}
	if err := applyBackgroundSelection(req, policy, seed); err != nil {
		return err
	}
	if err := applyOverlayBackgroundSelection(req, policy, seed); err != nil {
		return err
	}
	return applyBackgroundMusicSelection(req, policy, seed)
}

func applyBackgroundSelection(req *contract.GenerateRequest, policy mediaregistry.EditingAssetsPolicy, seed string) error {
	if !backgroundSelectionIsBlank(req.Render.Background) {
		return nil
	}
	plate, err := policy.SelectBackgroundForChannel(seed, req.ChannelID)
	if err != nil {
		return fmt.Errorf("apply editing asset policy: background: %w", err)
	}
	if req.Render.Background == nil {
		req.Render.Background = &scriptpkg.VideoBackgroundSpec{}
	}
	req.Render.Background.Mode = videoBackgroundModeAsset
	req.Render.Background.AssetID = plate.ID
	return nil
}

func applyBackgroundMusicSelection(req *contract.GenerateRequest, policy mediaregistry.EditingAssetsPolicy, seed string) error {
	if len(req.BackgroundMusic) > 0 {
		return nil
	}
	if req.Audio != audiocap.AudioModeCombinedTimeline {
		return nil
	}
	track, err := policy.SelectBGM(seed)
	if err != nil {
		return fmt.Errorf("apply editing asset policy: background music: %w", err)
	}
	req.BackgroundMusic = []scriptpkg.BackgroundMusicIntent{{
		AssetID:            track.Alias,
		Loop:               policy.BGM.Loop,
		GainDB:             policy.BGM.GainDB,
		DuckUnderVoiceover: policy.BGM.DuckUnderVoiceover,
		DuckGainDB:         policy.BGM.DuckGainDB,
	}}
	return nil
}

// backgroundSelectionIsBlank reports whether the caller left the clip
// background unspecified. An explicit mode is caller intent and is preserved;
// notably mode=none (or blur_source) must not be replaced by a plate.
func backgroundSelectionIsBlank(spec *scriptpkg.VideoBackgroundSpec) bool {
	if spec == nil {
		return true
	}
	return strings.TrimSpace(spec.Mode) == "" && strings.TrimSpace(spec.AssetID) == ""
}
