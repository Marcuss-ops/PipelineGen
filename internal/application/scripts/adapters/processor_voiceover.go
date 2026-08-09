// Package scripts — processor_voiceover.go generates voiceovers for
// each scene. Enabled as "voiceover" in the plan's Postprocessors list.
//
// PR 9 (June 2026): the legacy scene-splitters
// (splitScriptIntoSegments / sceneCountFromPlan) were REMOVED.
// The processor now reads scenes directly from
// engineResult.Output.SpecScene.Scenes — the canonical structured
// output from PR 1, validated by PR 6's ValidateAndEnrichSpecScene.
// Each generated voiceover maps to a model-defined scene with
// stable indexes.
//
// P0-#3 final closure (July 2026): the local `VoiceoverService`
// interface (Generate + GenerateWithDestination, positional signature)
// is RETIRED. The processor now depends on the canonical
// `voiceover.VoiceoverItemExecutor` port (single Execute method with a
// typed *voiceover.GenerateVoiceoverItemCommand). The same per-item
// pipeline the voiceover.generate_item child job and the
// promoVoiceoverAdapter already route through. Composition root wires
// *voiceover.ProcessVoiceoverItemUseCase (the concrete
// VoiceoverItemExecutor implementation) via
// `internal/app/wire_script_postprocess.go::registerScriptPostProcessors`.
//
// Partial failures are collected — the processor does NOT abort on
// first error. No-op when plan has no ClipEvidence or when the model
// output has zero scenes.
package adapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"

	"go.uber.org/zap"
)

// VoiceoverProcessor generates scene voiceovers via the canonical
// voiceover.VoiceoverItemExecutor port. Uses
// engineResult.Output.SpecScene.Scenes to drive per-scene voiceover
// generation (PR 9 contract).
//
// P0-#3 final closure (July 2026): the `gen` field is now
// `voiceover.VoiceoverItemExecutor` (the narrow typed port). The
// production concrete wired at composition time is
// *voiceover.ProcessVoiceoverItemUseCase (see
// `internal/app/build_bundles_voiceover.go::buildVoiceoverService`).
// Test doubles inject stubs that record invocations.
type VoiceoverProcessor struct {
	gen             voiceover.VoiceoverItemExecutor
	log             *zap.Logger
	voices          map[string]string
	translator      ports.ScriptTranslator
	defaultLanguage string
}

// NewVoiceoverProcessor creates a VoiceoverProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
//
// P0-#3 final closure (July 2026): the `gen` parameter is now
// `voiceover.VoiceoverItemExecutor` (the canonical narrow port). The
// previous `VoiceoverService` interface (Generate + GenerateWithDestination)
// is RETIRED. Production wiring passes
// `root.Domains.VoiceoverProcessItem` (the *ProcessVoiceoverItemUseCase
// from the composition root). Test stubs implement the single
// `Execute(ctx, *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error)`
// method.
func NewVoiceoverProcessor(gen voiceover.VoiceoverItemExecutor, log *zap.Logger) *VoiceoverProcessor {
	return &VoiceoverProcessor{gen: gen, log: log}
}

// ConfigureMultilingual wires the canonical language voice map and the
// already-composed script translator into the inline voiceover processor.
// The processor remains usable by existing tests and single-language callers
// when either value is nil.
func (p *VoiceoverProcessor) ConfigureDefaults(defaultLanguage string) {
	if p == nil {
		return
	}
	p.defaultLanguage = strings.TrimSpace(defaultLanguage)
}

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

func (p *VoiceoverProcessor) Name() ProcessorName { return ProcessorVoiceover }

// Policy classifies voiceover as ProcessorBestEffort: a missing
// voiceover executor (typed adapter nil at composition time) or a
// runtime TTS failure degrades into a Warning, not a hard failure.
// Voiceover is an auxiliary deliverable; per PR 2 spec: "voiceover =
// configurabile" (best-effort is the safe default). The plan arg is
// accepted for interface uniformity but ignored.
func (p *VoiceoverProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if plan != nil && plan.VoiceoverEnabled.AsBool() {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

// Process generates per-scene voiceovers. PR 9 contract: scenes come
// directly from engineResult.Output.SpecScene.Scenes (validated by
// ValidateAndEnrichSpecScene); no paragraph-splitting helper is
// used.
//
// P0-#3 final closure (July 2026): the per-item Execute call is the
// canonical narrow-port surface — the SAME pipeline the
// voiceover.generate_item child job and the promoVoiceoverAdapter
// route through. Real failures surface as typed Go errors (no
// Result{OK:false} masking).
func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not configured", scriptpkg.ErrPostprocessFailed)
	}

	if val, ok := ctx.Value("script_job_id").(string); !ok || val == "" {
		if parentJobID := corid.FromContext(ctx); parentJobID != "" {
			ctx = context.WithValue(ctx, "script_job_id", parentJobID)
		}
	}

	// PR 9: scenes sourced from canonical typed MSOV1.
	scenes := specScenesFromInput(input)
	// An explicit intro clip set identifies the leading scene as the short
	// spoken introduction. Enforce the editorial contract at the last
	// speakable-text boundary so the TTS input, persisted SpecScene, and
	// Google Doc cannot drift from one another.
	introChanged := capIntroNarration(&input, plan)
	if introChanged {
		scenes = specScenesFromInput(input)
	}
	// Translation is an explicit downstream contract. Prefer the retained
	// translated scene surface over the mutable working envelope because
	// binding processors may rebuild scenes from the original segment source
	// while preserving their asset bindings. TTS must never silently receive
	// the source-language text when the requested target translation exists.
	if len(input.TranslatedSpecScene.Scenes) > 0 {
		scenes = input.TranslatedSpecScene.Scenes
	}
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes to render (no specscene scenes)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
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
		return &PostProcessResult{
			Voiceovers: []SceneVoiceover{{SceneIndex: 0, Language: requestedPrimary, Status: "skipped"}},
			Warnings: []string{fmt.Sprintf(
				"voiceover skipped: requested translation to %q was not completed; effective text language is %q",
				requestedPrimary, primaryLanguage,
			)},
		}, nil
	}
	languages := make([]string, 0, len(plan.Languages)+1)
	seenLanguages := make(map[string]struct{})
	appendLanguage := func(language string) {
		language = strings.TrimSpace(language)
		if language == "" {
			return
		}
		key := strings.ToLower(language)
		if _, ok := seenLanguages[key]; ok {
			return
		}
		seenLanguages[key] = struct{}{}
		languages = append(languages, language)
	}
	appendLanguage(primaryLanguage)
	for _, requested := range plan.Languages {
		appendLanguage(requested)
	}
	baseScenes := scenes
	if len(input.OriginalSpecScene.Scenes) > 0 {
		baseScenes = input.OriginalSpecScene.Scenes
	}
	voiceovers := make([]SceneVoiceover, 0, len(scenes)*len(languages))
	var warnings []string
	for _, language := range languages {
		targetScenes := scenes
		if !strings.EqualFold(language, primaryLanguage) {
			if p.translator == nil {
				warnings = append(warnings, fmt.Sprintf("voiceover skipped for language %s: translator not configured", language))
				continue
			}
			targetScenes = cloneVoiceoverScenes(baseScenes)
			for i := range targetScenes {
				translated, err := p.translator.Translate(ctx, targetScenes[i].Text, language)
				if err != nil || strings.TrimSpace(translated) == "" {
					if err == nil {
						err = fmt.Errorf("translator returned empty text")
					}
					warnings = append(warnings, fmt.Sprintf("voiceover skipped for language %s: scene %d translation failed: %v", language, i, err))
					targetScenes = nil
					break
				}
				targetScenes[i].Text = translated
			}
			if targetScenes == nil {
				continue
			}
		}

		items := make([]VoiceoverSceneInput, 0, len(targetScenes))
		for i, scene := range targetScenes {
			sceneText := scene.Text
			if sceneText == "" {
				sceneText = fmt.Sprintf("Scene %d", i+1)
			}
			// The translated scene surface is the authoritative TTS input. Run
			// the sanitizer here as a final local boundary as well: a translated
			// processor may rebuild the scene after the registry sanitizer pass,
			// and source URLs must never reach the speech provider.
			cleanText, _, sanitizeErr := scriptpkg.SanitizeNarration(sceneText)
			if sanitizeErr != nil {
				return nil, sanitizeErr
			}
			sceneText = cleanText
			if err := scriptpkg.ValidateSpeakableText(sceneText); err != nil {
				return nil, err
			}

			// Sanitize the title for use in a filename, then build a
			// scene-stable filename: {title}_{scene_id}_{lang}.mp3.
			// VoiceoverProcessor used a local character-replacer (no .mp3,
			// no path-traversal guard); now delegates to the canonical
			// voiceover.SanitizeBasename which rejects path separators and
			// normalises unsafe characters via textutil.SanitizeFilename.
			sceneID := scene.ID
			if sceneID == "" {
				sceneID = fmt.Sprintf("%d", i+1)
			}
			// Sanitize sceneID too — it comes from model output and must
			// not contain path separators or unsafe filename characters.
			safeSceneID, serr2 := voiceover.SanitizeBasename(sceneID)
			if serr2 != nil {
				safeSceneID = fmt.Sprintf("s%d", i+1)
			}
			safeTitle, serr := voiceover.SanitizeBasename(plan.Title)
			if serr != nil {
				safeTitle = "scene"
			}
			filenameBase := fmt.Sprintf("%s_%s_%s", safeTitle, safeSceneID, language)
			// A translated scene must not collide with a previously published
			// source-language file. Drive deduplication is keyed by file identity;
			// include the canonical text hash so a changed TTS surface gets its own
			// durable file and SQLite row.
			if !strings.EqualFold(language, primaryLanguage) {
				filenameBase += "_" + string(voiceover.ComputeTextHash(sceneText))
			}
			filename := filenameBase + ".mp3"
			// A request-local folder is an explicit routing decision. Mark it
			// as such so the canonical resolver cannot fall back to the
			// configured voiceover root.
			dest := &voiceover.DestinationRequest{
				Kind:    string(voiceover.KindExplicit),
				Project: safeTitle,
			}
			if folderID := strings.TrimSpace(plan.VoiceoverFolderID); folderID != "" {
				// A plan-level folder is a caller-explicit destination. Mark it
				// explicitly so neither the group resolver nor any configured
				// default can replace it downstream.
				dest.Kind = string(voiceover.KindExplicit)
				dest.FolderID = folderID
			} else if plan.VoiceoverGroup != "" && p.log != nil {
				p.log.Warn("voiceover processor: voiceover_group set but not resolved to folder_id — falling back to default folder",
					zap.String("voiceover_group", plan.VoiceoverGroup))
			}
			items = append(items, VoiceoverSceneInput{
				SceneIndex:  i,
				Text:        sceneText,
				Voice:       p.voices[language],
				Filename:    filename,
				Destination: dest,
			})
		}

		outcomes := RunVoiceoverSceneFanout(ctx, p.gen, language, items, 4)
		for _, out := range outcomes {
			voiceovers = append(voiceovers, SceneVoiceover{
				SceneIndex: out.SceneIndex,
				Language:   language,
				Status:     out.Status,
				Link:       out.Link,
				LocalPath:  out.LocalPath,
			})
			if out.Status == "failed" {
				warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d (language %s): %s", out.SceneIndex, language, out.Error))
			}
		}
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("voiceover processor: partial failures",
			zap.Int("total", len(voiceovers)),
			zap.Int("failed", len(warnings)),
			zap.Strings("warnings", warnings))
	}

	// fix/voiceover-propagate-warnings (June 2026): propagate per-scene
	// failures to PostProcessResult.Warnings so the canonical
	// GenerationResult.Warnings envelope in the API response reports
	// them. The zap log above stays — it serves operator tail/grep,
	// while the envelope is the client-facing canonical surface.
	return &PostProcessResult{
		Voiceovers: voiceovers,
		Warnings:   warnings,
		UpdatedSpecScene: func() scriptpkg.SpecSceneOutput {
			if introChanged {
				return input.SpecScene
			}
			return scriptpkg.SpecSceneOutput{}
		}(),
	}, nil
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

// Compile-time assertion (AGENTS.md Pattern 0, June 2026): the
// canonical production concrete *voiceover.ProcessVoiceoverItemUseCase
// must structurally satisfy voiceover.VoiceoverItemExecutor. Drift
// between the production concrete's Execute signature and the port
// contract triggers a compile error here, not a silent runtime
// dispatch to a different surface. The same assertion is also pinned
// in process_voiceover_item.go (where the use case is defined); this
// is a redundant consumer-site drift detector so a future change to
// either side surfaces a build failure at the call site, not deep in
// the use case package.
var _ voiceover.VoiceoverItemExecutor = (*voiceover.ProcessVoiceoverItemUseCase)(nil)
