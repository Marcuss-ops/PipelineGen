// Package app — adapters_scenetext.go implements
// scriptgeneration.TextGenerator by wrapping the canonical
// script-generation Engine.
//
// Verdetto P1: the TextGenerator produces scene text (AI-generated,
// scene-by-scene) SEPARATELY from the Translator. The current code
// does NOT generate text — it only uses script_text already received
// or a local fallback.
//
// This adapter bridges scripgeneration.GenerateRequest (the clean
// domain type) with the existing usecase.Engine (which owns the
// full production-grade prompt construction, ollama invocation,
// output parsing, and sanitization).
//
// Architecture:
//
//	scriptgeneration.TextGenerator
//	         ↑ implements
//	app.SceneTextGenerator
//	         ↑ wraps
//	usecase.Engine → ollama.Generator.GenerateScript
package wiring

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scenepkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// SceneTextGenerator implements scriptgeneration.TextGenerator by
// wrapping the canonical script-generation Engine. Every call to
// GenerateSceneText builds a plan from the request, dispatches the
// engine, and converts the engine's structured output into the
// simple []Scene type expected by the runner.
//
// Panics on nil engine (fail-fast per godlike/07 NO-FAKE-AVAILABILITY).
type ClipAssetResolver interface {
	ResolveByMediaAssetID(context.Context, string) (*asset.Asset, error)
}

type SceneTextGenerator struct {
	Engine     *usecase.Engine
	Registry   *adapters.SourceRegistry
	ClipAssets ClipAssetResolver
	Log        *zap.Logger
}

// NewSceneTextGenerator constructs the SceneTextGenerator.
// engine is required; nil panics at construction time.
func NewSceneTextGenerator(engine *usecase.Engine, log *zap.Logger) *SceneTextGenerator {
	if engine == nil {
		panic("app: SceneTextGenerator requires a non-nil engine")
	}
	return &SceneTextGenerator{
		Engine: engine,
		Log:    log,
	}
}

// GenerateSceneText implements scriptgeneration.TextGenerator.
// It converts the GenerateRequest to a plan, calls the engine,
// and converts the engine's SpecScene output to []Scene.
//
// Each returned Scene contains:
//   - ID and Index from the engine's structured output
//   - Text[sourceLanguage] populated with the scene's narrative prose
//   - Clip populated when the engine returned binding data
//
// Returns an error when the engine fails or returns zero scenes.
func (g *SceneTextGenerator) GenerateSceneText(
	ctx context.Context,
	req scriptgen.GenerateRequest,
) ([]scriptgen.Scene, error) {
	plan, err := g.buildPlan(ctx, req)
	if err != nil {
		return nil, err
	}

	if g.Log != nil {
		g.Log.Info("scenetext: generating scene text",
			zap.String("title", plan.Title),
			zap.String("source_type", string(req.Source.Type)),
			zap.String("language", string(req.SourceLanguage)),
		)
	}

	engineResult, err := g.Engine.Generate(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("scenetext: engine generate failed: %w", err)
	}

	scenes, err := g.convertScenes(ctx, engineResult, req.SourceLanguage, req.Audio)
	if err != nil {
		return nil, fmt.Errorf("scenetext: enrich generated scenes: %w", err)
	}
	if len(scenes) == 0 {
		// Fresh clip generation uses a plain-text model contract.  The
		// canonical scene planner/postprocessor is responsible for
		// partitioning that prose and binding the resolved clips; failing
		// here would discard the valid model output before that boundary.
		// Keep this envelope deliberately provisional: it is replaced by
		// the planner before timeline/audio compilation.
		prose := strings.TrimSpace(engineResult.Output.Text)
		if prose == "" {
			return nil, fmt.Errorf("scenetext: engine returned zero scenes and empty prose")
		}
		scenes, err = g.convertClipProseScenes(ctx, plan, prose, req)
		if err != nil {
			return nil, fmt.Errorf("scenetext: resolve prose scenes: %w", err)
		}
		if len(scenes) == 0 {
			return nil, fmt.Errorf("scenetext: prose scene planner returned zero scenes")
		}
		if g.Log != nil {
			g.Log.Info("scenetext: passing prose to canonical scene planner",
				zap.Int("prose_words", len(strings.Fields(prose))))
		}
	}

	if g.Log != nil {
		g.Log.Info("scenetext: scene text generated",
			zap.Int("scene_count", len(scenes)),
			zap.Int("word_count", engineResult.WordCount),
		)
	}

	return scenes, nil
}

// convertClipProseScenes is the canonical bridge for the plain-text clip
// contract. The model owns narrative prose; PipelineGen owns scene count,
// clip binding, source ranges and audio intents. In particular, the first
// declared segment is an unbound short intro and accepted clips bind to the
// following scenes one-to-one.
func (g *SceneTextGenerator) convertClipProseScenes(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	prose string,
	req scriptgen.GenerateRequest,
) ([]scriptgen.Scene, error) {
	if plan == nil || plan.ClipEvidence == nil {
		return nil, fmt.Errorf("clip prose requires clip evidence")
	}
	clipIDs := plan.ClipEvidence.AcceptedClipIDs
	count := len(clipIDs)
	if len(plan.Segments) > 0 {
		count = len(plan.Segments)
	}
	if count <= 0 {
		return nil, fmt.Errorf("clip prose requires accepted clips")
	}
	draft := scenepkg.NewSceneSynthesizer().FromProse(prose, count)
	if len(draft) != count {
		return nil, fmt.Errorf("planned %d scenes, synthesized %d", count, len(draft))
	}

	scenes := make([]scriptgen.Scene, 0, count)
	for i, raw := range draft {
		text := strings.TrimSpace(raw.Text)
		if text == "" {
			return nil, fmt.Errorf("scene %d has empty prose", i)
		}
		durationMS := int64(math.Max(1000, float64(len(strings.Fields(text))*60000/150)))
		out := scriptgen.Scene{
			ID:           fmt.Sprintf("scene-%d", i),
			Index:        i,
			DurationMS:   durationMS,
			DurationUS:   durationMS * 1000,
			Text:         map[scriptgen.Language]string{req.SourceLanguage: text},
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		}

		// The first declared segment is the short intro. It is intentionally
		// not assigned a clip; clip i maps to scene i+1.
		clipIndex := i
		if len(plan.Segments) == len(clipIDs)+1 {
			clipIndex = i - 1
		}
		if clipIndex < 0 {
			scenes = append(scenes, out)
			continue
		}
		if clipIndex >= len(clipIDs) {
			return nil, fmt.Errorf("scene %d has no matching accepted clip", i)
		}
		clipID := clipIDs[clipIndex]
		canonical, err := g.ClipAssets.ResolveByMediaAssetID(ctx, clipID)
		if err != nil || canonical == nil {
			if err == nil {
				err = fmt.Errorf("asset not found")
			}
			return nil, fmt.Errorf("resolve clip %s: %w", clipID, err)
		}
		sha256Hex, err := renderAssetSHA256(canonical)
		if err != nil {
			return nil, fmt.Errorf("resolve clip %s binary sha256: %w", clipID, err)
		}
		durationSeconds, err := renderAssetDurationSeconds(canonical)
		if err != nil {
			return nil, fmt.Errorf("resolve clip %s duration: %w", clipID, err)
		}
		clip := &scriptgen.ClipReference{ID: clipID, Title: plan.ClipEvidence.ClipNames[clipID], Path: canonical.LocalPath(), AudioPath: canonical.LocalPath(), SHA256: sha256Hex, Duration: durationSeconds, FrameCount: int64(math.Round(durationSeconds * 30))}
		if detail, ok := plan.ClipEvidence.ClipDetails[clipID]; ok {
			clip.SourceInMS, clip.SourceOutMS = detail.StartMs, detail.EndMs
		}
		if clip.SourceOutMS <= clip.SourceInMS {
			clip.SourceInMS = 0
			clip.SourceOutMS = int64(math.Round(clip.Duration * 1000))
		}
		if clip.SourceOutMS <= clip.SourceInMS {
			return nil, fmt.Errorf("clip %s has no usable source duration", clipID)
		}
		clipDurationUS := (clip.SourceOutMS - clip.SourceInMS) * 1000
		out.Clip = clip
		out.DurationMS = clipDurationUS / 1000
		out.DurationUS = clipDurationUS
		out.Audio = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: clipID, SourceInUS: clip.SourceInMS * 1000, SourceDurationUS: clipDurationUS, UseOriginalAudio: true}
		out.AudioIntents = []capabilityaudio.AudioIntent{out.Audio, {Mode: capabilityaudio.AudioVoiceover}}
		scenes = append(scenes, out)
	}
	return scenes, nil
}

// SetSourceRegistry injects the source registry used to resolve
// non-text sources (clips, catalog, search, curate). The registry is
// built later in the composition root than the SceneTextGenerator,
// so it is supplied via this setter.
func (g *SceneTextGenerator) SetSourceRegistry(reg *adapters.SourceRegistry) {
	g.Registry = reg
}

// SetClipAssetResolver supplies the canonical media-registry lookup used to
// enrich model bindings with the local path, byte hash, duration, and source
// range required by the sealed RenderPlan. The model only emits a logical ID;
// renderable file identity must come from the registry, never from the model.
func (g *SceneTextGenerator) SetClipAssetResolver(resolver ClipAssetResolver) {
	g.ClipAssets = resolver
}

// buildPlan converts a scriptgeneration.GenerateRequest into a
// ResolvedGenerationPlan consumable by the engine. For text sources
// it assembles the topic/source_text directly. For non-text sources
// it delegates to the injected SourceRegistry to fetch clip evidence
// and the canonical source text.
func (g *SceneTextGenerator) buildPlan(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
	topic := req.Source.Topic
	if topic == "" && req.Source.SourceText != "" {
		topic = firstLine(req.Source.SourceText)
	}
	title := req.Title
	if title == "" {
		title = topic
		if title == "" {
			title = "Untitled Script"
		}
	}

	renderedPrompt := buildEditorialPromptFromGenReq(req)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:                req.IdempotencyKey,
		Title:             title,
		Topic:             topic,
		Language:          string(req.SourceLanguage),
		SourceText:        req.Source.SourceText,
		RenderedPrompt:    renderedPrompt,
		Mode:              scriptpkg.ModeForSource(scriptpkg.SourceType(req.Source.Type)),
		SourceKind:        string(req.Source.Type),
		TargetWords:       req.ScriptParams.TargetWords,
		SingleScene:       req.ScriptParams.SingleScene,
		Duration:          req.ScriptParams.Duration,
		MinWords:          req.ScriptParams.MinWords,
		SegmentWords:      req.ScriptParams.SegmentWords,
		SegmentTopics:     append([]string(nil), req.ScriptParams.SegmentTopics...),
		Segments:          append([]scriptpkg.ScriptSegment(nil), req.ScriptParams.Segments...),
		SentencesPerImage: req.ScriptParams.SentencesPerImage,
		ImagesPerScene:    req.ScriptParams.ImagesPerScene,
		Style:             req.ScriptParams.Style,
		Guidelines:        req.ScriptParams.Guidelines,
		IntroClipIDs:      append([]string(nil), req.Source.IntroClipIDs...),
		NumClips:          req.Source.NumClips,
		// Postprocessors left empty — the engine generates raw
		// narrative prose. Downstream phases handle translation,
		// voiceover, and docs.
	}

	// Non-text sources require source resolution to populate clip
	// evidence and the canonical source text.
	if req.Source.Type != scriptgen.SourceText {
		if g.Registry == nil {
			return nil, fmt.Errorf("scenetext: source registry not configured for source type %q", req.Source.Type)
		}
		resolved, err := g.Registry.Resolve(ctx, genSourceToSourceSpec(req.Source), genRequestToResolutionContext(req))
		if err != nil {
			return nil, fmt.Errorf("scenetext: source resolution failed for type %q: %w", req.Source.Type, err)
		}
		if resolved.Topic != "" {
			plan.Topic = resolved.Topic
		}
		if resolved.Title != "" {
			plan.Title = resolved.Title
		}
		if resolved.SourceText != "" {
			plan.SourceText = resolved.SourceText
		}
		if resolved.ClipEvidence != nil {
			plan.ClipEvidence = resolved.ClipEvidence
		}
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
		// Re-render the prompt from the resolved source text so the
		// engine actually sees the clip evidence.
		plan.RenderedPrompt = buildEditorialPrompt(plan.Topic, plan.SourceText, req.Title, req.Source.Query)
	}

	return plan, nil
}

// convertScenes maps the engine's structured SpecScene output into
// the simple Scene type consumed by the scriptgeneration runner.
func (g *SceneTextGenerator) convertScenes(
	ctx context.Context,
	result *usecase.EngineResult,
	sourceLang scriptgen.Language,
	requestedAudio capabilityaudio.AudioMode,
) ([]scriptgen.Scene, error) {
	scenes := make([]scriptgen.Scene, 0, len(result.Output.SpecScene.Scenes))
	for _, s := range result.Output.SpecScene.Scenes {
		scene := scriptgen.Scene{
			ID:    s.ID,
			Index: s.Index,
			Text: map[scriptgen.Language]string{
				sourceLang: s.Text,
			},
		}
		// The model emits only a logical clip ID. Resolve all render-critical
		// fields from the canonical media registry before a RenderPlan can be
		// built; accepting model-supplied paths or hashes would break the
		// registry/CAS ownership boundary.
		if s.Bindings.Clip != nil && s.Bindings.Clip.ClipID != "" {
			binding := s.Bindings.Clip
			clip := &scriptgen.ClipReference{ID: binding.ClipID, Title: binding.ClipTitle, DriveLink: binding.DriveLink}
			if g.ClipAssets != nil {
				canonical, err := g.ClipAssets.ResolveByMediaAssetID(ctx, binding.ClipID)
				if err != nil {
					return nil, fmt.Errorf("resolve clip %s: %w", binding.ClipID, err)
				}
				if canonical != nil {
					clip.DriveLink = canonical.DriveLink()
					clip.Path = canonical.LocalPath()
					clip.SHA256, err = renderAssetSHA256(canonical)
					if err != nil {
						return nil, fmt.Errorf("resolve clip %s binary sha256: %w", binding.ClipID, err)
					}
					clip.AudioPath = canonical.LocalPath()
					clip.Duration, err = renderAssetDurationSeconds(canonical)
					if err != nil {
						return nil, fmt.Errorf("resolve clip %s duration: %w", binding.ClipID, err)
					}
					// The current render runtime uses the canonical 30 fps
					// default; the request-level rational rate is applied at
					// RenderPlan compilation. Keep the asset bound in frames so
					// source-range validation cannot accept an overrun.
					clip.FrameCount = int64(math.Round(clip.Duration * 30))
				}
			}
			clip.SourceInMS = binding.StartMs
			clip.SourceOutMS = binding.EndMs
			if binding.DurationMs > 0 && clip.Duration == 0 {
				clip.Duration = float64(binding.DurationMs) / 1000
				clip.FrameCount = int64(math.Round(clip.Duration * 30))
			}
			durationUS := int64(math.Round(clip.Duration * 1_000_000))
			sourceInUS := int64(0)
			if clip.SourceOutMS > clip.SourceInMS {
				sourceInUS = clip.SourceInMS * 1000
				durationUS = int64(clip.SourceOutMS-clip.SourceInMS) * 1000
			}
			switch requestedAudio {
			case capabilityaudio.AudioModeCombinedTimeline:
				if s.Bindings.Clip != nil && durationUS > 0 {
					clipIntent := capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: clip.ID, SourceInUS: sourceInUS, SourceDurationUS: durationUS, UseOriginalAudio: true}
					scene.Audio = clipIntent
					scene.AudioIntents = []capabilityaudio.AudioIntent{clipIntent}
				} else {
					scene.Audio = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}
				}
			case capabilityaudio.AudioModeChunkedVoiceover:
				scene.Audio = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover}
			default:
				scene.Audio = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioSilence}
			}
			scene.DurationMS = int64(math.Round(float64(durationUS) / 1000))
			scene.DurationUS = durationUS
			scene.Clip = clip
		}
		scenes = append(scenes, scene)
	}
	// Non-clip scenes have no source media duration yet. Use the engine's
	// deterministic estimate as the initial timeline duration; the voiceover
	// stage replaces it with the certified TTS duration before compilation.
	fallbackMS := int64(1000)
	if result.EstDuration > 0 && len(scenes) > 0 {
		fallbackMS = int64(result.EstDuration) * 1000 / int64(len(scenes))
		if fallbackMS <= 0 {
			fallbackMS = 1000
		}
	}
	for i := range scenes {
		if scenes[i].DurationMS <= 0 {
			scenes[i].DurationMS = fallbackMS
		}
		if scenes[i].DurationUS <= 0 {
			scenes[i].DurationUS = scenes[i].DurationMS * 1000
		}
	}
	return scenes, nil
}

// buildEditorialPromptFromGenReq builds the editorial prompt string
// from a GenerateRequest — specifically the source text assembly that
// tells the model what to write about.
func buildEditorialPromptFromGenReq(req scriptgen.GenerateRequest) string {
	return buildEditorialPrompt(req.Source.Topic, req.Source.SourceText, req.Title, req.Source.Query)
}

// buildEditorialPrompt builds the editorial prompt string from the
// resolved source fields. It is used both for direct text sources
// and for sources that have been resolved through the SourceRegistry.
func buildEditorialPrompt(topic, sourceText, title, query string) string {
	var parts []string
	if topic != "" {
		parts = append(parts, "Topic: "+topic)
	}
	if sourceText != "" {
		parts = append(parts, "Source text:\n"+sourceText)
	}
	if title != "" {
		parts = append(parts, "Title: "+title)
	}
	if query != "" {
		parts = append(parts, "Search query: "+query)
	}
	// The engine prompt builder appends plainTextInstruction
	// at generation time — we don't need to repeat it here.
	parts = append(parts, "Do not include raw URLs, hyperlinks, or source citations in the prose output.")
	return strings.Join(parts, "\n\n")
}

// genSourceToSourceSpec maps the scriptgeneration.Source (the
// pipeline's small source value) to the domain SourceSpec consumed
// by the source registry.
func genSourceToSourceSpec(src scriptgen.Source) scriptpkg.SourceSpec {
	return scriptpkg.SourceSpec{
		Type:       scriptpkg.SourceType(src.Type),
		Topic:      src.Topic,
		SourceText: src.SourceText,
		ClipIDs:    copyStrings(src.ClipIDs),
		Query:      src.Query,
		MaxClips:   src.MaxClips,
	}
}

// genRequestToResolutionContext maps the scriptgeneration request to
// the domain SourceResolutionContext consumed by the source registry.
func genRequestToResolutionContext(req scriptgen.GenerateRequest) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		Title:    req.Title,
		Language: string(req.SourceLanguage),
	}
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

// renderAssetSHA256 returns the binary identity required by RenderPlan. Older
// imported clip rows may only contain a Drive MD5; that value is not accepted
// as a render manifest identity, so derive the SHA-256 from the canonical local
// bytes at the boundary.
func renderAssetSHA256(a *asset.Asset) (string, error) {
	if a == nil || strings.TrimSpace(a.LocalPath()) == "" {
		return "", fmt.Errorf("asset has no local path")
	}
	if candidate := strings.TrimSpace(a.FileHash()); len(candidate) == 64 && !strings.Contains(candidate, ":") {
		return candidate, nil
	}
	return fileutil.SHA256File(a.LocalPath())
}

// renderAssetDurationSeconds is the execution-boundary fallback for legacy
// registry rows whose online Drive asset is valid but whose duration column
// was never backfilled. The media bytes remain the source of truth; this does
// not invent a timeline duration or persist a second timing representation.
func renderAssetDurationSeconds(a *asset.Asset) (float64, error) {
	if a == nil {
		return 0, fmt.Errorf("asset is nil")
	}
	if a.Duration > 0 {
		return a.Duration.Seconds(), nil
	}
	path := strings.TrimSpace(a.LocalPath())
	if path == "" {
		return 0, fmt.Errorf("asset has no local execution source")
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, fmt.Errorf("probe media duration: %w", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("probe returned invalid duration %q", strings.TrimSpace(string(out)))
	}
	return seconds, nil
}

// firstLine returns the first line of a multi-line string.
func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}
