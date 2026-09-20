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
// TranslateScenes, RenderGoogleDocContent, and CreateGoogleDoc inline.
//
// The canonical caller is the HTTP handler (HandlerGenerate) which
// previously built a SubmitRequest directly from the envelope. After
// this change the handler calls:
//
//	req, err := scriptgeneration.BuildGenerateRequest(env, idempotencyKey)
//	// then: svc.Start(ctx, req)
//
// No Ollama, no Google Docs, no Drive. Pure field mapping only.
package scriptgeneration

import (
	"errors"
	"fmt"
	"strings"

	audiocap "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildGenerateRequest is the sole canonical builder for
// GenerateRequest. It accepts a validated GenerationEnvelopeV2
// and produces the domain-level request with zero side effects.
//
// Verdetto rules:
//   - NO translation.TranslateScenes(ctx, ...)
//   - NO translation.RenderGoogleDocContent(...)
//   - NO docCreator.CreateGoogleDoc(...)
//   - NO Ollama, NO database, NO Drive
//   - Returns only struct-literal construction + field mapping
//
// Source of truth: the first item in the envelope defines the
// primary generation parameters. Multi-item envelopes are treated
// as batch — the primary item configures the top-level parameters
// and each item is handled independently downstream.
func BuildGenerateRequest(env *scriptpkg.GenerationEnvelopeV2, idempotencyKey string) (GenerateRequest, error) {
	if env == nil {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: envelope is nil")
	}
	if len(env.Items) == 0 {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: envelope has no items")
	}

	item := env.Items[0]
	// Envelope-level force refresh is authoritative across every cache layer,
	// not only the job/idempotency boundary. A fresh job that leaves the
	// script, extraction, or asset caches warm can replay stale scene text and
	// provider bindings, defeating a real cold VidRush certification.
	if env.ForceRefresh {
		item.ScriptParams.ForceRefresh = true
		item.Source.ForceRefresh = true
		item.MediaPlan.ForceRefreshAssets = true
		item.MediaPlan.ForceRefreshExtraction = true
		item.MediaPlan.ForceRefreshBindings = true
	}
	// Rendered clips are grouped under the generated script name. Keep the
	// source clip IDs untouched; this field only controls Drive routing.
	if strings.TrimSpace(item.Output.Render.DriveSubfolderName) == "" {
		scriptFolderName := strings.TrimSpace(item.Title)
		if scriptFolderName == "" {
			scriptFolderName = strings.TrimSpace(item.Source.Topic)
		}
		item.Output.Render.DriveSubfolderName = scriptFolderName
	}
	if err := resolveBackgroundReference(&item.Output.Render); err != nil {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: resolve render background: %w", err)
	}
	item.Output.Render.Normalize()
	// SSOT: output.render.watermark / output.render.subtitles are the only
	// spellings. The legacy top-level output.watermark/output.subtitles
	// compatibility blocks were removed; requests carrying them fail at the
	// schema boundary instead of silently merging two sources of truth.

	// Map SourceSpec → scriptgeneration.Source (pure field copy).
	// Source policy fields must survive this durable-runtime boundary:
	// dropping Search/CachePolicy/Research turns a web-research request into
	// an offline cache lookup and fails with RESEARCH_DISABLED_CACHE_MISS.
	source := Source{
		Type:               SourceType(item.Source.Type),
		Topic:              item.Source.Topic,
		SourceText:         item.Source.SourceText,
		ArtlistKeywords:    copyStrings(item.Source.ArtlistKeywords),
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

	// Map languages from the output spec.
	var languages []Language
	for _, lang := range item.Output.Languages {
		if lang != "" {
			languages = append(languages, Language(lang))
		}
	}

	// Source language defaults to the item's language if set,
	// otherwise "en" (the canonical safety default).
	sourceLang := Language(item.Language)
	if sourceLang == "" {
		sourceLang = "en"
	}

	// Documents are opt-in through the canonical docs object. The output
	// languages remain available for translation and are not a docs trigger.
	docsEnabled := item.Docs.Enabled
	docsLanguages := item.Docs.Languages
	docsFolderID := item.Docs.FolderID
	// output.drive_folder_id is the job's explicit artifact root. Keep it
	// connected to the render contract as well: the existing publisher then
	// creates/reuses its deterministic <script>/<language>/overlay child.
	// Docs.folder_id remains the canonical Docs destination when supplied.
	artifactFolderID := firstNonEmpty(item.Output.DriveFolderID, docsFolderID)
	if strings.TrimSpace(item.Output.Render.DriveFolderID) == "" {
		item.Output.Render.DriveFolderID = artifactFolderID
	}

	// generate_timeline is the explicit opt-in for the canonical timeline
	// metadata artifact. Video rendering is no longer part of PipelineGen; the
	// timeline is audio/metadata-only.
	generateTimeline := item.Output.GenerateTimeline
	audioModeInput := item.Audio.Mode
	if audioModeInput == "" { // compatibility with the initial nested output shape
		audioModeInput = item.Output.Audio.Mode
	}
	audioMode, err := audiocap.ResolveAudioMode(audiocap.AudioMode(audioModeInput), item.Output.VoiceoverEnabled.AsBool())
	if err != nil {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: %w", err)
	}

	// godlike/07 NO-FAKE-AVAILABILITY: a voiceover-producing audio mode
	// publishes artifacts, so Project is REQUIRED. Fail at the preflight
	// boundary (before the run is enqueued/started) rather than at the
	// voiceover phase — this turns "job FAILED after pipeline start" into
	// an immediate 400. The runner-phase gate (runner_phase_voiceover.go)
	// remains as a defensive backstop for resumed runs and internal callers.
	needsVoiceover := audioMode == audiocap.AudioModeChunkedVoiceover || audioMode == audiocap.AudioModeCombinedTimeline
	if needsVoiceover && strings.TrimSpace(item.Project) == "" {
		return GenerateRequest{}, fmt.Errorf("%w: voiceover publishing requires a resolved Project", ErrProjectRequired)
	}

	// Voiceover timing policy: the canonical top-level audio config carries
	// the policy; the nested output.audio shape is the compat fallback. nil
	// means the pipeline applies the canonical defaults downstream — timing
	// capture is never implicitly mandatory.
	timing := item.Audio.Timing
	if timing == nil {
		timing = item.Output.Audio.Timing
	}

	// Editorial audio intent block: the canonical top-level audio config
	// carries mix_policy / background_music / sound_effects; the nested
	// output.audio shape is the compat fallback (same pattern as mode and
	// timing). background_music was already normalized to a slice at the
	// wire boundary (AudioOutputConfig.UnmarshalJSON accepts a single
	// object), so the durable domain always works with
	// []BackgroundMusicIntent — no second normalization here.
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
	voiceoverLanguages := item.Audio.VoiceoverLanguages
	if voiceoverLanguages == nil {
		voiceoverLanguages = item.Output.Audio.VoiceoverLanguages
	}
	if needsVoiceover && voiceoverLanguages != nil {
		if len(voiceoverLanguages) == 0 {
			return GenerateRequest{}, fmt.Errorf("scriptgeneration: audio.voiceover_languages must not be empty when audio is enabled")
		}
		allowed := make(map[string]struct{}, len(languages)+1)
		allowed[string(sourceLang)] = struct{}{}
		for _, lang := range languages {
			allowed[string(lang)] = struct{}{}
		}
		for _, lang := range voiceoverLanguages {
			if _, ok := allowed[lang]; !ok {
				return GenerateRequest{}, fmt.Errorf("scriptgeneration: audio.voiceover_languages entry %q must be the source language or included in output.languages", lang)
			}
		}
		includesSource := false
		for _, lang := range voiceoverLanguages {
			if lang == string(sourceLang) {
				includesSource = true
				break
			}
		}
		if !includesSource {
			return GenerateRequest{}, fmt.Errorf("scriptgeneration: audio.voiceover_languages must include the source language %q", sourceLang)
		}
	}

	req := GenerateRequest{
		Model:               item.Model,
		Tone:                item.Tone,
		Style:               item.Style,
		Render:              item.Output.Render,
		OverlayBackground:   item.OverlayBackground,
		OverlayStyle:        item.OverlayStyle,
		IdempotencyKey:      idempotencyKey,
		ForceRefresh:        env.ForceRefresh,
		Source:              source,
		StockBindings:       append([]scriptpkg.StockBindingInput(nil), item.Output.StockBindings...),
		ScriptParams:        item.ScriptParams,
		MediaPlan:           item.MediaPlan.Clone(),
		ExtractEntities:     item.Output.ExtractEntities,
		GenerateSceneImages: item.Output.GenerateSceneImages,
		SourceLanguage:      sourceLang,
		Languages:           languages,
		GenerateTimeline:    generateTimeline,
		Timing:              timing,
		VoiceoverLanguages:  toLanguages(voiceoverLanguages),
		Docs: DocumentsConfig{
			Enabled:   docsEnabled,
			Languages: toLanguages(docsLanguages),
			FolderID:  docsFolderID,
		},
		DocsEnabled:   docsEnabled,
		DriveFolderID: artifactFolderID,
		SaveToDB:      item.Output.SaveToDB,
		Title:         item.Title,
		Intro:         scriptpkg.CloneFixedSection(item.Intro),
		Outro:         scriptpkg.CloneFixedSection(item.Outro),
		// Project is the canonical semantic project namespace for artifact
		// routing, resolved ONCE here from the explicit generation input.
		// Empty Project for a voiceover-enabled generation fails closed
		// before the first TTS call (ErrProjectRequired).
		Project:    item.Project,
		OutputName: item.Title, // fallback: output name defaults to title
		// VoiceoverFolderID is the explicit caller destination for voiceover
		// artifacts; empty falls back to the configured default. Threaded
		// verbatim into the routing context so the per-scene TTS command
		// honors the caller-explicit folder instead of dropping it.
		VoiceoverFolderID: item.Output.VoiceoverFolderID,
		Audio:             audioMode,
		MixPolicy:         mixPolicy,
		BackgroundMusic:   backgroundMusic,
		SoundEffects:      soundEffects,
	}
	// Background centralizzato ON (Intro V2): the canonical editorial
	// selection policy fills the assets the caller left blank (background
	// plate, BGM in COMBINED_TIMELINE). It NEVER overrides a
	// caller-provided selection — an explicit mode (including "none") is
	// preserved — so this is a pure default-fill at the single ingress
	// point both job handlers share.
	if err := ApplyEditingAssetPolicy(&req, mediaregistry.DefaultEditingAssetsPolicy()); err != nil {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: apply editing asset policy: %w", err)
	}
	return req, nil
}

func toLanguages(src []string) []Language {
	if src == nil {
		return nil
	}
	dst := make([]Language, 0, len(src))
	for _, lang := range src {
		if lang != "" {
			dst = append(dst, Language(lang))
		}
	}
	return dst
}

// copyStrings returns a copy of the string slice (nil-safe).
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func cloneFloat64(src *float64) *float64 {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func cloneInt(src *int) *int {
	if src == nil {
		return nil
	}
	value := *src
	return &value
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
//   - background music is filled only when the request declared no BGM AND the
//     request is in the COMBINED_TIMELINE audio mode. Injecting a BGM layer
//     into a job that never builds an audio plan would be a silent no-op.
//   - a transition SFX is NOT auto-placed: its position depends on the scene
//     timeline, which does not exist at request-build time. Callers that do
//     have scene context use policy.SelectTransitionSFX directly.
//
// Determinism: the seed is the request's idempotency key, so retrying the same
// job selects the same plate/track and the resulting plan stays byte-identical.
func ApplyEditingAssetPolicy(req *GenerateRequest, policy mediaregistry.EditingAssetsPolicy) error {
	if req == nil {
		return errors.New("apply editing asset policy: request is required")
	}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("apply editing asset policy: %w", err)
	}
	seed := strings.TrimSpace(req.IdempotencyKey)
	if err := applyBackgroundSelection(req, policy, seed); err != nil {
		return err
	}
	return applyBackgroundMusicSelection(req, policy, seed)
}

func applyBackgroundSelection(req *GenerateRequest, policy mediaregistry.EditingAssetsPolicy, seed string) error {
	if !backgroundSelectionIsBlank(req.Render.Background) {
		return nil
	}
	plate, err := policy.SelectBackground(seed)
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

func applyBackgroundMusicSelection(req *GenerateRequest, policy mediaregistry.EditingAssetsPolicy, seed string) error {
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
