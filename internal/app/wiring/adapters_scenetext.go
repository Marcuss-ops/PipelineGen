package wiring

import (
	"context"
	"fmt"
	"math"
	"strings"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scenepkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// SceneTextGenerator wraps the canonical script-generation Engine and
// converts its structured output into the runner's []Scene contract.
type ClipAssetResolver interface {
	ResolveByMediaAssetID(context.Context, string) (*asset.Asset, error)
}

// ensureClipPlanningDuration keeps planning metadata useful without claiming
// that a Drive-only asset is renderable. Binary identity and materialized
// duration are certified later at the render boundary.
func ensureClipPlanningDuration(clip *scriptgen.ClipReference, fallbackMS int64) {
	if clip == nil {
		return
	}
	if clip.SourceOutMS > clip.SourceInMS {
		return
	}
	if clip.Duration <= 0 {
		if fallbackMS <= 0 {
			fallbackMS = 1000
		}
		clip.Duration = float64(fallbackMS) / 1000
	}
	clip.SourceInMS = 0
	clip.SourceOutMS = int64(math.Round(clip.Duration * 1000))
	clip.FrameCount = int64(math.Round(clip.Duration * 30))
}

type SceneTextGenerator struct {
	Engine     *usecase.Engine
	Registry   *adapters.SourceRegistry
	ClipAssets ClipAssetResolver
	Probe      ClipProber
	Memory     *adapters.Service
	Log        *zap.Logger
}

// NewSceneTextGenerator constructs the generator; nil engine panics.
func NewSceneTextGenerator(engine *usecase.Engine, log *zap.Logger) *SceneTextGenerator {
	if engine == nil {
		panic("app: SceneTextGenerator requires a non-nil engine")
	}
	return &SceneTextGenerator{
		Engine: engine,
		Log:    log,
	}
}

// GenerateSceneText converts the request to a plan, invokes the engine, and
// converts its SpecScene output to []Scene.
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

	// Persist the fresh output to the canonical script memory gate so an
	// identical replay (same source, same plan) is served from cache and the
	// downstream per-segment caches (keyed on the deterministic scene text
	// hash) hit exactly. Exact hits are never re-persisted.
	if g.Memory != nil && plan.UseMemory && engineResult.CacheStatus == "generated" && engineResult.Output.Text != "" {
		if _, saveErr := g.Memory.SaveAfterGeneration(ctx, adapters.SaveGenerationInput{
			ChannelID: "default",
			Mode:      plan.Mode,
			Language:  plan.Language,
			Title:     plan.Title,
			Prompt:    plan.RenderedPrompt,
			Model:     engineResult.Model,
			WordCount: engineResult.WordCount,
			CacheKey:  plan.CacheKey,
		}, engineResult.Output.Text); saveErr != nil && g.Log != nil {
			g.Log.Warn("scenetext: failed to save script cache",
				zap.String("title", plan.Title),
				zap.Error(saveErr))
		}
	}

	scenes, err := g.convertScenes(ctx, engineResult, req.SourceLanguage, req.Audio, false)
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
		if req.Source.Type == scriptgen.SourceClips && len(strings.Fields(prose)) < 35*req.Source.NumClips {
			if grounded := explicitClipBriefProse(plan.SourceText, req.Source.NumClips); grounded != "" {
				prose = grounded
				if g.Log != nil {
					g.Log.Warn("scenetext: model prose below clip contract; using explicit grounded clip briefs")
				}
			}
		}
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

// explicitClipBriefProse extracts caller-provided CLIP N briefs. This is a
// deterministic, source-grounded recovery for a transient/empty model reply;
// it never fabricates scene text or invents a placeholder.
func explicitClipBriefProse(source string, count int) string {
	if strings.TrimSpace(source) == "" || count <= 0 {
		return ""
	}
	briefs := make([]string, 0, count)
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "CLIP ") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 || strings.TrimSpace(line[colon+1:]) == "" {
			continue
		}
		briefs = append(briefs, strings.TrimSpace(line[colon+1:]))
		if len(briefs) == count {
			break
		}
	}
	if len(briefs) != count {
		return ""
	}
	return strings.Join(briefs, "\n\n")
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
	if plan == nil {
		return nil, fmt.Errorf("prose scene conversion requires a generation plan")
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		count := len(plan.Segments)
		if count == 0 {
			count = 1
		}
		draft := scenepkg.NewSceneSynthesizer().FromProse(prose, count)
		if len(draft) != count {
			return nil, fmt.Errorf("planned %d text scenes, synthesized %d", count, len(draft))
		}
		scenes := make([]scriptgen.Scene, 0, count)
		for i, raw := range draft {
			text := strings.TrimSpace(raw.Text)
			if text == "" {
				return nil, fmt.Errorf("text scene %d has empty prose", i)
			}
			scenes = append(scenes, scriptgen.Scene{
				ID:    fmt.Sprintf("scene-%d", i),
				Index: i,
				Text:  map[scriptgen.Language]string{req.SourceLanguage: text},
				Audio: capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
				AudioIntents: []capabilityaudio.AudioIntent{{
					Mode: capabilityaudio.AudioVoiceover,
				}},
			})
		}
		return scenes, nil
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
			ID:         fmt.Sprintf("scene-%d", i),
			Index:      i,
			DurationMS: durationMS,
			DurationUS: durationMS * 1000,
			Text:       map[scriptgen.Language]string{req.SourceLanguage: text},
			Audio:      capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		}

		ownedIDs := []string(nil)
		if len(plan.Segments) > 0 && i < len(plan.Segments) {
			ownedIDs = plan.Segments[i].ClipIDs
		} else {
			clipIndex := i
			if len(plan.Segments) == len(clipIDs)+1 {
				clipIndex = i - 1
			}
			if clipIndex >= 0 && clipIndex < len(clipIDs) {
				ownedIDs = []string{clipIDs[clipIndex]}
			}
		}
		var offsetUS int64
		if len(ownedIDs) > 0 {
			out.DurationUS = 0
		}
		for _, clipID := range ownedIDs {
			clip, err := g.resolveEvidenceClip(ctx, plan, clipID, true)
			if err != nil {
				return nil, err
			}
			// A Drive-only clip without recorded start/end metadata carries no
			// usable source range yet. Fall back to the narration estimate so
			// the canonical timeline can still be planned; the real duration
			// is certified at render time from the materialized binary.
			ensureClipPlanningDuration(clip, durationMS)
			clipDurationUS := (clip.SourceOutMS - clip.SourceInMS) * 1000
			out.Clips = append(out.Clips, clip)
			if out.Clip == nil {
				out.Clip = clip
			}
			out.DurationUS += clipDurationUS
			out.AudioIntents = append(out.AudioIntents, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: clipID, SourceInUS: clip.SourceInMS * 1000, SourceDurationUS: clipDurationUS, TimelineOffsetUS: offsetUS, TimelineDurationUS: clipDurationUS, UseOriginalAudio: true})
			offsetUS += clipDurationUS
		}
		if len(out.Clips) > 0 {
			out.DurationMS = out.DurationUS / 1000
			out.Audio = out.AudioIntents[0]
			out.AudioIntents = append(out.AudioIntents, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover})
		}
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

// SetClipProber supplies the canonical media probe port used to measure
// asset durations when the catalog duration is absent.
func (g *SceneTextGenerator) SetClipProber(prober ClipProber) {
	g.Probe = prober
}

// SetMemoryService wires the canonical gemmamemory service used to cache
// successfully generated scene text. The durable runner path calls the
// engine directly (not through the legacy finalizer), so the save must
// happen here to make COLD→WARM replay deterministic. Optional: when nil,
// caching is a no-op.
func (g *SceneTextGenerator) SetMemoryService(svc *adapters.Service) {
	if g != nil {
		g.Memory = svc
	}
}

// ResolveVidRushPlan resolves the per-run generation plan used by the
// incremental VidRush coordinator. It is the same plan buildPlan produces for
// text generation, so the coordinator enriches with the caller's language,
// title, segments and media policy.
func (g *SceneTextGenerator) ResolveVidRushPlan(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
	return g.buildPlan(ctx, req)
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
		ID:                  req.IdempotencyKey,
		Title:               title,
		Project:             req.Project,
		Topic:               topic,
		Language:            string(req.SourceLanguage),
		SourceText:          req.Source.SourceText,
		RenderedPrompt:      renderedPrompt,
		Mode:                scriptpkg.ModeForSource(scriptpkg.SourceType(req.Source.Type)),
		SaveToDB:            req.SaveToDB,
		SourceKind:          string(req.Source.Type),
		TargetWords:         req.ScriptParams.TargetWords,
		SingleScene:         req.ScriptParams.SingleScene,
		Duration:            req.ScriptParams.Duration,
		MinWords:            req.ScriptParams.MinWords,
		SegmentWords:        req.ScriptParams.SegmentWords,
		SegmentTopics:       append([]string(nil), req.ScriptParams.SegmentTopics...),
		Segments:            append([]scriptpkg.ScriptSegment(nil), req.ScriptParams.Segments...),
		SentencesPerImage:   req.ScriptParams.SentencesPerImage,
		ImagesPerScene:      req.ScriptParams.ImagesPerScene,
		Style:               req.ScriptParams.Style,
		Guidelines:          req.ScriptParams.Guidelines,
		IntroClipIDs:        append([]string(nil), req.Source.IntroClipIDs...),
		NumClips:            req.Source.NumClips,
		MediaPlan:           req.MediaPlan.Clone(),
		GroundingPolicy:     req.Source.GroundingPolicy,
		FallbackPolicy:      req.Source.FallbackPolicy,
		PromptVersion:       req.ScriptParams.PromptVersion,
		EditorPromptVersion: req.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     req.ScriptParams.QAPromptVersion,
		UseMemory:           req.ScriptParams.UseMemory,
		ForceRefresh:        req.ForceRefresh || req.ScriptParams.ForceRefresh,
		PromptProfile:       "default-v1",
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
		// An explicit source_text is the caller's editorial brief for Gemma.
		// Resolver evidence enriches the plan but must not replace that brief.
		if plan.SourceText == "" && resolved.SourceText != "" {
			plan.SourceText = resolved.SourceText
		}
		if resolved.Segments != nil {
			plan.Segments = scriptpkg.CloneScriptSegments(resolved.Segments)
		}
		if resolved.ClipEvidence != nil {
			plan.ClipEvidence = resolved.ClipEvidence
		}
		if resolved.ResearchEvidence != nil {
			plan.ResearchEvidence = resolved.ResearchEvidence.Clone()
		}
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
		// Re-render the prompt from the resolved source text so the
		// engine actually sees the clip evidence.
		plan.RenderedPrompt = buildEditorialPrompt(plan.Topic, plan.SourceText, req.Title, req.Source.Query, req.ScriptParams.TargetWords, req.ScriptParams.MinWords, req.ScriptParams.Style, req.ScriptParams.Guidelines, string(req.SourceLanguage), req.ScriptParams.PromptVersion)
	}

	// Text sources do not go through a resolver; derive the canonical
	// source fingerprint directly so the memory-gate cache key is
	// content-aware and deterministic across COLD/WARM replays.
	if plan.SourceFingerprint == "" && req.Source.Type == scriptgen.SourceText {
		plan.SourceFingerprint = scriptpkg.BuildFingerprint(scriptpkg.FingerprintInputFromSource(genSourceToSourceSpec(req.Source), nil))
	}
	plan.CacheKey = scriptpkg.BuildCacheKey(plan)

	return plan, nil
}

// convertScenes maps the engine's structured SpecScene output into
// the simple Scene type consumed by the scriptgeneration runner.
func (g *SceneTextGenerator) convertScenes(
	ctx context.Context,
	result *usecase.EngineResult,
	sourceLang scriptgen.Language,
	requestedAudio capabilityaudio.AudioMode,
	requireLocalMedia bool,
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
		bindings := append([]scriptpkg.ClipBinding(nil), s.Bindings.Clips...)
		if len(bindings) == 0 && s.Bindings.Clip != nil {
			bindings = []scriptpkg.ClipBinding{*s.Bindings.Clip}
		}
		var totalDurationUS int64
		var timelineOffsetUS int64
		for _, binding := range bindings {
			if strings.TrimSpace(binding.ClipID) == "" {
				continue
			}
			clip, err := g.resolveRenderClip(ctx, binding)
			if err != nil {
				if requireLocalMedia {
					return nil, err
				}
				clip = &scriptgen.ClipReference{ID: binding.ClipID, Title: binding.ClipTitle, DriveLink: binding.DriveLink}
			}
			clip.SourceInMS, clip.SourceOutMS = binding.StartMs, binding.EndMs
			if binding.DurationMs > 0 && clip.Duration == 0 {
				clip.Duration = float64(binding.DurationMs) / 1000
				clip.FrameCount = int64(math.Round(clip.Duration * 30))
			}
			// Drive-only clips (render not requested) may carry no recorded
			// source range or duration. Fall back to the narration estimate so
			// the canonical timeline can still be planned.
			ensureClipPlanningDuration(clip, int64(math.Max(1000, float64(len(strings.Fields(s.Text))*60000/150))))
			durationUS := int64(math.Round(clip.Duration * 1_000_000))
			sourceInUS := int64(0)
			if clip.SourceOutMS > clip.SourceInMS {
				sourceInUS = clip.SourceInMS * 1000
				durationUS = int64(clip.SourceOutMS-clip.SourceInMS) * 1000
			}
			totalDurationUS += durationUS
			scene.Clips = append(scene.Clips, clip)
			if requestedAudio == capabilityaudio.AudioModeCombinedTimeline && durationUS > 0 {
				scene.AudioIntents = append(scene.AudioIntents, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioClip, ClipAssetID: clip.ID, SourceInUS: sourceInUS, SourceDurationUS: durationUS, TimelineOffsetUS: timelineOffsetUS, TimelineDurationUS: durationUS, UseOriginalAudio: true})
			}
			timelineOffsetUS += durationUS
		}
		if len(scene.Clips) > 0 {
			scene.Clip = scene.Clips[0]
			scene.DurationUS, scene.DurationMS = totalDurationUS, totalDurationUS/1000
			if len(scene.AudioIntents) > 0 {
				scene.Audio = scene.AudioIntents[0]
			} else {
				scene.Audio = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioSilence}
			}
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

// resolveRenderClip is the only binding-to-render-asset bridge. Paths, hashes,
// durations, and Drive links are always read from the canonical registry.
func (g *SceneTextGenerator) resolveRenderClip(ctx context.Context, binding scriptpkg.ClipBinding) (*scriptgen.ClipReference, error) {
	if g.ClipAssets == nil {
		return nil, fmt.Errorf("resolve clip %s: canonical asset registry is not configured", binding.ClipID)
	}
	canonical, err := g.ClipAssets.ResolveByMediaAssetID(ctx, binding.ClipID)
	if err != nil {
		return nil, fmt.Errorf("resolve clip %s: %w", binding.ClipID, err)
	}
	if canonical == nil {
		return nil, fmt.Errorf("resolve clip %s: canonical asset not found", binding.ClipID)
	}
	sha256Hex, err := renderAssetSHA256(canonical)
	if err != nil {
		return nil, fmt.Errorf("resolve clip %s binary sha256: %w", binding.ClipID, err)
	}
	duration, err := g.renderAssetDurationSeconds(ctx, canonical)
	if err != nil {
		return nil, fmt.Errorf("resolve clip %s duration: %w", binding.ClipID, err)
	}
	return &scriptgen.ClipReference{
		ID: binding.ClipID, Title: binding.ClipTitle, DriveLink: canonical.DriveLink(),
		Path: canonical.LocalPath(), AudioPath: canonical.LocalPath(), SHA256: sha256Hex,
		Duration:       duration,
		DurationUS:     int64(math.Round(duration * 1_000_000)),
		DurationSource: resolveClipDurationSource(canonical),
		FrameCount:     int64(math.Round(duration * 30)),
		// Subject identity threads the canonical asset metadata onto the clip
		// reference so the scene↔clip identity gate can certify the clip
		// actually features the subject its scene narrates.
		Speakers:        canonical.Speakers(),
		MentionedPeople: canonical.MentionedPeople(),
		Subject:         canonical.GetMetadataString("subject"),
	}, nil
}
