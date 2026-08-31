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
	"fmt"
	"strings"

	audiocap "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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
	// Rendered clips are grouped under the generated script name. Keep the
	// source clip IDs untouched; this field only controls Drive routing.
	if strings.TrimSpace(item.Output.Render.DriveSubfolderName) == "" {
		scriptFolderName := strings.TrimSpace(item.Title)
		if scriptFolderName == "" {
			scriptFolderName = strings.TrimSpace(item.Source.Topic)
		}
		item.Output.Render.DriveSubfolderName = scriptFolderName
	}
	item.Output.Render.Normalize()
	if item.Output.Render.Watermark == nil {
		item.Output.Render.Watermark = item.Output.Watermark
	}
	if item.Output.Render.Subtitles == nil {
		item.Output.Render.Subtitles = item.Output.Subtitles
	}
	item.Output.Render.Normalize()

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
		IntroClipIDs:       copyStrings(item.Source.IntroClipIDs),
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

	return GenerateRequest{
		Model:             item.Model,
		Tone:              item.Tone,
		Style:             item.Style,
		Render:            item.Output.Render,
		OverlayBackground: item.OverlayBackground,
		OverlayStyle:      item.OverlayStyle,
		IdempotencyKey:    idempotencyKey,
		ForceRefresh:      env.ForceRefresh,
		Source:            source,
		ScriptParams:      item.ScriptParams,
		MediaPlan:         item.MediaPlan.Clone(),
		ExtractEntities:   item.Output.ExtractEntities,
		SourceLanguage:    sourceLang,
		Languages:         languages,
		GenerateTimeline:  generateTimeline,
		Timing:            timing,
		Docs: DocumentsConfig{
			Enabled:   docsEnabled,
			Languages: toLanguages(docsLanguages),
			FolderID:  docsFolderID,
		},
		DocsEnabled:   docsEnabled,
		DriveFolderID: docsFolderID,
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
	}, nil
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
