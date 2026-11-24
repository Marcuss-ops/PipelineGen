// Package voiceover generates per-scene voiceovers for the scripts pipeline.
// It owns the voiceover postprocessor, its scene fanout, clip bindings and the
// narration sanitizer. The processor reads scenes directly from the canonical
// SpecScene output; no paragraph-splitting helper is used.
//
// Partial failures are collected — the processor does NOT abort on the first
// TTS error. A missing executor is still a composition failure.
package voiceover

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	voiceoversvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"

	"go.uber.org/zap"
)

// voiceoverTranslationConcurrency bounds the per-language scene-translation
// fan-out of the inline voiceover processor.
const voiceoverTranslationConcurrency = 4

// VoiceoverProcessor generates scene voiceovers via the canonical
// voiceoversvc.VoiceoverItemExecutor port.
type VoiceoverProcessor struct {
	gen             voiceoversvc.VoiceoverItemExecutor
	log             *zap.Logger
	voices          map[string]string
	translator      ports.ScriptTranslator
	defaultLanguage string
}

// NewVoiceoverProcessor creates a VoiceoverProcessor. gen must be non-nil at
// registration time; Process still fails closed when it is nil.
func NewVoiceoverProcessor(gen voiceoversvc.VoiceoverItemExecutor, log *zap.Logger) *VoiceoverProcessor {
	return &VoiceoverProcessor{gen: gen, log: log}
}

// ConfigureDefaults wires the fallback source language.
func (p *VoiceoverProcessor) ConfigureDefaults(defaultLanguage string) {
	if p == nil {
		return
	}
	p.defaultLanguage = strings.TrimSpace(defaultLanguage)
}

// ConfigureMultilingual wires the canonical language voice map and the
// already-composed script translator.
func (p *VoiceoverProcessor) ConfigureMultilingual(voices map[string]string, translator ports.ScriptTranslator) {
	if p == nil {
		return
	}
	p.voices = make(map[string]string, len(voices))
	for language, voice := range voices {
		p.voices[strings.TrimSpace(language)] = strings.TrimSpace(voice)
	}
	p.translator = translator
}

func (p *VoiceoverProcessor) Name() adapters.ProcessorName { return adapters.ProcessorVoiceover }

// Policy classifies voiceover as ProcessorBestEffort unless the plan requires
// voiceover or required timing, in which case it is fail-closed.
func (p *VoiceoverProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	if plan != nil && plan.VoiceoverEnabled.AsBool() {
		return adapters.ProcessorRequired
	}
	if plan != nil && plan.Timing != nil && plan.Timing.Mode == audio.TimingRequired {
		return adapters.ProcessorRequired
	}
	return adapters.ProcessorBestEffort
}

// Process generates per-scene voiceovers. It resolves the scene/language
// surface and then delegates each language to renderVoiceoverLanguage.
func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input adapters.ProcessInput) (*adapters.PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not configured", scriptpkg.ErrPostprocessFailed)
	}

	if val, ok := ctx.Value(scriptpkg.ContextKeyScriptJobID).(string); !ok || val == "" {
		if parentJobID := corid.FromContext(ctx); parentJobID != "" {
			ctx = context.WithValue(ctx, scriptpkg.ContextKeyScriptJobID, parentJobID)
		}
	}

	scenes := specScenesFromInput(input)
	// Translation is an explicit downstream contract. Prefer the retained
	// translated scene surface over the mutable working envelope because
	// binding processors may rebuild scenes from the original segment source
	// while preserving their asset bindings.
	if len(input.TranslatedSpecScene.Scenes) > 0 {
		scenes = input.TranslatedSpecScene.Scenes
	}
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes to render (no specscene scenes)",
				zap.String("item_id", plan.ID))
		}
		return &adapters.PostProcessResult{}, nil
	}

	sourceLanguage := strings.TrimSpace(plan.Language)
	if sourceLanguage == "" {
		sourceLanguage = strings.TrimSpace(p.defaultLanguage)
	}
	if sourceLanguage == "" {
		return nil, fmt.Errorf("voiceover processor: default language is not resolved")
	}
	primaryLanguage := strings.TrimSpace(input.EffectiveLanguage)
	if primaryLanguage == "" {
		primaryLanguage = sourceLanguage
	}
	requestedPrimary := strings.TrimSpace(plan.TranslateTo)
	if requestedPrimary != "" && !strings.EqualFold(primaryLanguage, requestedPrimary) {
		voiceovers := make([]adapters.SceneVoiceover, 0, len(scenes))
		for i := range scenes {
			voiceovers = append(voiceovers, adapters.SceneVoiceover{SceneIndex: i, Language: requestedPrimary, Status: "skipped"})
		}
		return &adapters.PostProcessResult{
			Voiceovers: voiceovers,
			Warnings: []string{fmt.Sprintf(
				"voiceover skipped: requested translation to %q was not completed; effective text language is %q",
				requestedPrimary, primaryLanguage,
			)},
		}, nil
	}

	languages := voiceoverLanguages(primaryLanguage, plan.Languages)
	baseScenes := scenes
	if len(input.OriginalSpecScene.Scenes) > 0 {
		baseScenes = input.OriginalSpecScene.Scenes
	}

	voiceovers := make([]adapters.SceneVoiceover, 0, len(scenes)*len(languages))
	var warnings []string
	for _, language := range languages {
		languageVoiceovers, languageWarnings, err := p.renderVoiceoverLanguage(ctx, plan, baseScenes, scenes, language, primaryLanguage)
		if err != nil {
			return nil, err
		}
		voiceovers = append(voiceovers, languageVoiceovers...)
		warnings = append(warnings, languageWarnings...)
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("voiceover processor: partial failures",
			zap.Int("total", len(voiceovers)),
			zap.Int("failed", len(warnings)),
			zap.Strings("warnings", warnings))
	}

	return &adapters.PostProcessResult{
		Voiceovers:       voiceovers,
		Warnings:         warnings,
		UpdatedSpecScene: scriptpkg.SpecSceneOutput{},
	}, nil
}

func voiceoverLanguages(primary string, requested []string) []string {
	languages := make([]string, 0, len(requested)+1)
	seen := make(map[string]struct{}, len(requested)+1)
	appendLanguage := func(language string) {
		language = strings.TrimSpace(language)
		if language == "" {
			return
		}
		key := strings.ToLower(language)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		languages = append(languages, language)
	}
	appendLanguage(primary)
	for _, language := range requested {
		appendLanguage(language)
	}
	return languages
}

// renderVoiceoverLanguage translates (when needed), builds the per-scene items
// and runs one bounded fanout. Translation failures remove only the affected
// scenes from that language's TTS pass; they never discard the whole language
// and never substitute source text under a target-language filename.
func (p *VoiceoverProcessor) renderVoiceoverLanguage(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	baseScenes, scenes []scriptpkg.SpecScene,
	language, primaryLanguage string,
) ([]adapters.SceneVoiceover, []string, error) {
	targetScenes := scenes
	var warnings []string
	var translationFailures map[int]error
	if !strings.EqualFold(language, primaryLanguage) {
		if p.translator == nil {
			return nil, []string{fmt.Sprintf("voiceover skipped for language %s: translator not configured", language)}, nil
		}
		targetScenes, translationFailures = p.translateScenesForLanguage(ctx, baseScenes, language)
		for _, index := range sortedFailureIndexes(translationFailures) {
			warnings = append(warnings, fmt.Sprintf("voiceover skipped for language %s: scene %d translation failed: %v", language, index, translationFailures[index]))
		}
	}

	items, err := p.buildVoiceoverItems(plan, targetScenes, translationFailures, language, primaryLanguage)
	if err != nil {
		return nil, nil, err
	}

	outcomes := RunVoiceoverSceneFanout(ctx, p.gen, language, items, 4)
	voiceovers := make([]adapters.SceneVoiceover, 0, len(outcomes))
	for _, out := range outcomes {
		voiceovers = append(voiceovers, adapters.SceneVoiceover{
			SceneIndex: out.SceneIndex,
			Language:   language,
			Status:     out.Status,
			Link:       out.Link,
			LocalPath:  out.LocalPath,
			DurationMs: out.DurationMs,
			Timing:     voiceoverTimingToDomain(out.Timing),
			TimingArtifact: func() *audio.SpeechTimingArtifact {
				if out.Timing == nil {
					return nil
				}
				return out.Timing.Artifact
			}(),
		})
		if out.Status == "failed" {
			// required timing is fail-closed at the script job level.
			if plan.Timing != nil && plan.Timing.Mode == audio.TimingRequired {
				return nil, nil, fmt.Errorf("voiceover required-timing: scene %d (language %s) failed: %s", out.SceneIndex, language, out.Error)
			}
			warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d (language %s): %s", out.SceneIndex, language, out.Error))
		}
	}
	return voiceovers, warnings, nil
}

func (p *VoiceoverProcessor) buildVoiceoverItems(
	plan *scriptpkg.ResolvedGenerationPlan,
	targetScenes []scriptpkg.SpecScene,
	translationFailures map[int]error,
	language, primaryLanguage string,
) ([]VoiceoverSceneInput, error) {
	items := make([]VoiceoverSceneInput, 0, len(targetScenes))
	for i, scene := range targetScenes {
		if _, failed := translationFailures[i]; failed {
			continue
		}
		if !scene.AllowsTTS() || !scene.AllowsGeneratedAudio() {
			continue
		}
		item, err := p.buildVoiceoverSceneInput(plan, scene, i, language, primaryLanguage)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *VoiceoverProcessor) buildVoiceoverSceneInput(
	plan *scriptpkg.ResolvedGenerationPlan,
	scene scriptpkg.SpecScene,
	index int,
	language, primaryLanguage string,
) (VoiceoverSceneInput, error) {
	sceneText := scene.Text
	if sceneText == "" {
		sceneText = fmt.Sprintf("Scene %d", index+1)
	}
	// The translated scene surface is the authoritative TTS input. Run the
	// sanitizer here as a final local boundary as well: a translated processor
	// may rebuild the scene after the registry sanitizer pass, and source URLs
	// must never reach the speech provider.
	cleanText, _, sanitizeErr := scriptpkg.SanitizeNarration(sceneText)
	if sanitizeErr != nil {
		return VoiceoverSceneInput{}, sanitizeErr
	}
	sceneText = cleanText
	if err := scriptpkg.ValidateSpeakableText(sceneText); err != nil {
		return VoiceoverSceneInput{}, err
	}

	// Build a scene-stable filename: {title}_{scene_id}_{lang}.mp3.
	sceneID := scene.ID
	if sceneID == "" {
		sceneID = fmt.Sprintf("%d", index+1)
	}
	safeSceneID, serr2 := voiceoversvc.SanitizeBasename(sceneID)
	if serr2 != nil {
		safeSceneID = fmt.Sprintf("s%d", index+1)
	}
	sanitizedTitle, serr := voiceoversvc.SanitizeBasename(plan.Title)
	// Project is already resolved at generation ingress. Do not derive it from
	// the title or filename; an absent value must fail closed at the canonical
	// publisher seam.
	project := strings.TrimSpace(plan.Project)
	filenameTitle := sanitizedTitle
	if serr != nil || strings.TrimSpace(filenameTitle) == "" {
		filenameTitle = "scene"
	}
	filenameBase := fmt.Sprintf("%s_%s_%s", filenameTitle, safeSceneID, language)
	// A translated scene must not collide with a previously published
	// source-language file. Include the canonical text hash so a changed TTS
	// surface gets its own durable file and SQLite row.
	if !strings.EqualFold(language, primaryLanguage) {
		filenameBase += "_" + string(voiceoversvc.ComputeTextHash(sceneText))
	}
	filename := filenameBase + ".mp3"

	// A request-local folder is an explicit routing decision. Mark it as such
	// so the canonical resolver cannot fall back to the configured root.
	dest := &voiceoversvc.DestinationRequest{
		Kind:    string(voiceoversvc.KindExplicit),
		Project: project,
	}
	if folderID := strings.TrimSpace(plan.VoiceoverFolderID); folderID != "" {
		dest.Kind = string(voiceoversvc.KindExplicit)
		dest.FolderID = folderID
	} else if plan.VoiceoverGroup != "" && p.log != nil {
		p.log.Warn("voiceover processor: voiceover_group set but not resolved to folder_id — falling back to default folder",
			zap.String("voiceover_group", plan.VoiceoverGroup))
	}

	return VoiceoverSceneInput{
		SceneIndex:     index,
		Text:           sceneText,
		Voice:          p.voices[language],
		Filename:       filename,
		Destination:    dest,
		Moments:        momentQueriesFromAnnotations(scene.Annotations),
		Timing:         plan.Timing,
		ForceSynthesis: plan.ForceRefresh,
	}, nil
}

func specScenesFromInput(input adapters.ProcessInput) []scriptpkg.SpecScene {
	if len(input.SpecScene.Scenes) == 0 {
		return nil
	}
	return input.SpecScene.Scenes
}

// voiceoverTimingToDomain converts the per-item timing result into the domain
// binding shape (nil-safe: disabled timing produces a nil entry).
func voiceoverTimingToDomain(in *voiceoversvc.VoiceoverTimingResult) *scriptpkg.VoiceoverTimingBinding {
	if in == nil {
		return nil
	}
	return &scriptpkg.VoiceoverTimingBinding{
		Status:       string(in.Status),
		JSONLink:     in.JSONLink,
		SRTLink:      in.SRTLink,
		VTTLink:      in.VTTLink,
		BoundaryMode: in.BoundaryMode,
		WordCount:    in.WordCount,
		DurationUS:   in.DurationUS,
		TextSHA256:   in.TextSHA256,
		AudioSHA256:  in.AudioSHA256,
	}
}

// translateScenesForLanguage translates every translatable scene into
// `language` with a bounded fan-out and returns the translated scenes plus the
// per-scene failures. The source slice is never mutated.
func (p *VoiceoverProcessor) translateScenesForLanguage(ctx context.Context, scenes []scriptpkg.SpecScene, language string) ([]scriptpkg.SpecScene, map[int]error) {
	out := cloneVoiceoverScenes(scenes)
	failures := make(map[int]error)
	if len(out) == 0 {
		return out, failures
	}

	indexes := make([]int, 0, len(out))
	for i := range out {
		if out[i].AllowsTranslation() {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) == 0 {
		return out, failures
	}

	var mu sync.Mutex
	_, _ = concurrent.Map(ctx, indexes, voiceoverTranslationConcurrency, func(opCtx context.Context, _ int, index int) (struct{}, error) {
		translated, err := p.translator.Translate(opCtx, scenes[index].Text, language)
		if err == nil && strings.TrimSpace(translated) == "" {
			err = fmt.Errorf("translator returned empty text")
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failures[index] = err
			return struct{}{}, nil
		}
		out[index].Text = translated
		return struct{}{}, nil
	})
	return out, failures
}

// sortedFailureIndexes returns failed scene indexes in ascending order so the
// emitted warnings are stable across runs.
func sortedFailureIndexes(failures map[int]error) []int {
	if len(failures) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(failures))
	for index := range failures {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func cloneVoiceoverScenes(src []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	if len(src) == 0 {
		return nil
	}
	dst := make([]scriptpkg.SpecScene, len(src))
	copy(dst, src)
	for i := range dst {
		dst[i].Text = strings.Clone(src[i].Text)
	}
	return dst
}

// momentQueriesFromAnnotations converts deterministic semantic annotations
// into PhraseLocator queries. Entities contribute their mention surface forms;
// important phrases and keywords contribute their span text.
func momentQueriesFromAnnotations(ann *scriptpkg.SceneAnnotations) []audio.MomentQuery {
	if ann == nil {
		return nil
	}
	var queries []audio.MomentQuery
	entities := make([]scriptpkg.AnnotatedEntity, 0, len(ann.PrimaryEntities)+len(ann.SecondaryEntities))
	entities = append(entities, ann.PrimaryEntities...)
	entities = append(entities, ann.SecondaryEntities...)
	for _, entity := range entities {
		for _, mention := range entity.Mentions {
			if v := strings.TrimSpace(mention.Text); v != "" {
				queries = append(queries, audio.MomentQuery{Kind: audio.MomentEntity, Value: v})
			}
		}
		if len(entity.Mentions) == 0 {
			if v := strings.TrimSpace(entity.Text); v != "" {
				queries = append(queries, audio.MomentQuery{Kind: audio.MomentEntity, Value: v})
			}
		}
	}
	for _, phrase := range ann.ImportantPhrases {
		if v := strings.TrimSpace(phrase.Text); v != "" {
			queries = append(queries, audio.MomentQuery{Kind: audio.MomentPhrase, Value: v})
		}
	}
	for _, word := range ann.ImportantWords {
		if v := strings.TrimSpace(word.Text); v != "" {
			queries = append(queries, audio.MomentQuery{Kind: audio.MomentKeyword, Value: v})
		}
	}
	return queries
}

// Compile-time assertion: the production concrete must structurally satisfy
// the canonical executor port.
var _ voiceoversvc.VoiceoverItemExecutor = (*voiceoversvc.ProcessVoiceoverItemUseCase)(nil)
