package wiring

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
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
	Engine             *usecase.Engine
	Registry           *adapters.SourceRegistry
	ClipAssets         ClipAssetResolver
	Probe              ClipProber
	Memory             *adapters.Service
	Log                *zap.Logger
	segmentConcurrency int
}

// SetSegmentConcurrency configures the bounded fan-out used by streaming
// scene generation. The Engine's GenerationGate remains the hard Ollama
// ceiling; this controls how many scene jobs can be in flight locally.
func (g *SceneTextGenerator) SetSegmentConcurrency(concurrency int) {
	if g == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	g.segmentConcurrency = concurrency
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
	scenes, _, err := g.GenerateSceneTextWithTrace(ctx, req)
	return scenes, err
}

// GenerateSceneTextStreamWithTrace generates explicit editorial segments one
// at a time. Each completed segment is emitted immediately, allowing the
// runner's downstream SceneTextReady fan-out to begin while the next model
// call is still running. Requests without explicit segments keep the batch
// path because prose has no safe scene boundary until the full response is
// available.
func (g *SceneTextGenerator) GenerateSceneTextStreamWithTrace(
	ctx context.Context,
	req scriptgen.GenerateRequest,
	emit func(scriptgen.Scene) error,
) (scriptpkg.SourceTrace, error) {
	if emit == nil {
		return scriptpkg.SourceTrace{}, fmt.Errorf("scenetext: stream emit callback is required")
	}
	plan, err := g.buildPlan(ctx, req)
	if err != nil {
		return scriptpkg.SourceTrace{}, err
	}

	segments := append([]scriptpkg.ScriptSegment(nil), plan.Segments...)
	if len(segments) == 0 && len(plan.SegmentTopics) > 0 {
		segments = make([]scriptpkg.ScriptSegment, 0, len(plan.SegmentTopics))
		for _, topic := range plan.SegmentTopics {
			segments = append(segments, scriptpkg.ScriptSegment{Topic: topic})
		}
	}
	if len(segments) == 0 {
		scenes, trace, batchErr := g.GenerateSceneTextWithTrace(ctx, req)
		if batchErr != nil {
			return scriptpkg.SourceTrace{}, batchErr
		}
		for _, scene := range scenes {
			if err := emit(scene); err != nil {
				return scriptpkg.SourceTrace{}, err
			}
		}
		return trace, nil
	}

	type sceneResult struct {
		index int
		scene scriptgen.Scene
		err   error
	}
	generateOne := func(index int, segment scriptpkg.ScriptSegment) sceneResult {
		segmentReq := req
		// Each provider call owns exactly one topic. Keep it on the legacy
		// single-topic surface so the multi-segment paragraph validator does
		// not expect a five-paragraph envelope inside one call.
		segmentReq.ScriptParams.Segments = nil
		segmentReq.ScriptParams.SegmentTopics = []string{segment.Topic}
		segmentReq.ScriptParams.SingleScene = true
		segmentTarget := segment.TargetWords
		if segmentTarget <= 0 {
			segmentTarget = req.ScriptParams.SegmentWords
		}
		if segmentTarget <= 0 && req.ScriptParams.TargetWords > 0 && len(segments) > 0 {
			segmentTarget = req.ScriptParams.TargetWords / len(segments)
		}
		if segmentTarget > 0 {
			segmentReq.ScriptParams.TargetWords = segmentTarget
		}

		segmentPlan := *plan
		// Keep the segment on the Engine plan so its word-budget validator is
		// active. The old nil value silently routed this call through the
		// unvalidated whole-script path, which is why 500-word targets could
		// become 900+ words.
		if segment.TargetWords > 0 || segment.MinWords > 0 || segment.MaxWords > 0 || segmentReq.ScriptParams.SegmentWords > 0 || segmentReq.ScriptParams.TargetWords > 0 {
			segmentPlan.Segments = []scriptpkg.ScriptSegment{segment}
		} else {
			segmentPlan.Segments = nil
		}
		segmentPlan.SegmentTopics = []string{segment.Topic}
		segmentPlan.SingleScene = true
		segmentPlan.Topic = strings.TrimSpace(segment.Topic)
		segmentPlan.TargetWords = segmentReq.ScriptParams.TargetWords
		segmentPlan.UseMemory = false // the aggregate cache key cannot identify one segment safely
		segmentPlan.RenderedPrompt = buildEditorialPromptFromGenReq(segmentReq)

		result, genErr := g.Engine.Generate(ctx, &segmentPlan)
		if genErr != nil {
			return sceneResult{index: index, err: fmt.Errorf("scenetext: segment %d generate failed: %w", index, genErr)}
		}
		scenes, convertErr := g.convertScenes(ctx, result, req.SourceLanguage, req.Audio, false)
		if convertErr != nil {
			return sceneResult{index: index, err: convertErr}
		}
		if len(scenes) == 0 && strings.TrimSpace(result.Output.Text) != "" {
			// Single-topic calls may legitimately return plain prose without a
			// structured SpecScene envelope. Reuse the canonical prose planner
			// instead of treating that valid narration as an empty segment.
			scenes, convertErr = g.convertClipProseScenes(ctx, &segmentPlan, result.Output.Text, segmentReq)
			if convertErr != nil {
				return sceneResult{index: index, err: fmt.Errorf("scenetext: segment %d prose planning failed: %w", index, convertErr)}
			}
		}
		if len(scenes) != 1 {
			return sceneResult{index: index, err: fmt.Errorf("scenetext: segment %d produced %d scenes; want exactly one", index, len(scenes))}
		}
		scenes[0].Index = index
		scenes[0].ID = fmt.Sprintf("scene-%d", index)
		return sceneResult{index: index, scene: scenes[0]}
	}
	workers := g.segmentConcurrency
	if workers <= 0 {
		workers = 1
	}
	if g.Engine != nil && g.Engine.GenerationConcurrency() > 0 && workers > g.Engine.GenerationConcurrency() {
		workers = g.Engine.GenerationConcurrency()
	}
	if workers > len(segments) {
		workers = len(segments)
	}
	jobs := make(chan int)
	results := make(chan sceneResult, len(segments))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results <- generateOne(index, segments[index])
			}
		}()
	}
	go func() {
		for index := range segments {
			select {
			case jobs <- index:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				close(results)
				return
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for output := range results {
		if output.err != nil {
			return scriptpkg.SourceTrace{}, output.err
		}
		if err := emit(output.scene); err != nil {
			return scriptpkg.SourceTrace{}, err
		}
	}
	return researchTrace(plan), nil
}

// GenerateSceneTextStream is the no-trace compatibility surface.
func (g *SceneTextGenerator) GenerateSceneTextStream(ctx context.Context, req scriptgen.GenerateRequest, emit func(scriptgen.Scene) error) error {
	_, err := g.GenerateSceneTextStreamWithTrace(ctx, req, emit)
	return err
}

// GenerateSceneTextWithTrace preserves source provenance produced while the
// plan is resolved. This is consumed by the durable runner and avoids losing
// web-research evidence at the capability boundary.
func (g *SceneTextGenerator) GenerateSceneTextWithTrace(
	ctx context.Context,
	req scriptgen.GenerateRequest,
) ([]scriptgen.Scene, scriptpkg.SourceTrace, error) {
	plan, err := g.buildPlan(ctx, req)
	if err != nil {
		return nil, scriptpkg.SourceTrace{}, err
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
		return nil, scriptpkg.SourceTrace{}, fmt.Errorf("scenetext: engine generate failed: %w", err)
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
		return nil, scriptpkg.SourceTrace{}, fmt.Errorf("scenetext: enrich generated scenes: %w", err)
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
			return nil, scriptpkg.SourceTrace{}, fmt.Errorf("scenetext: engine returned zero scenes and empty prose")
		}
		scenes, err = g.convertClipProseScenes(ctx, plan, prose, req)
		if err != nil {
			return nil, scriptpkg.SourceTrace{}, fmt.Errorf("scenetext: resolve prose scenes: %w", err)
		}
		if len(scenes) == 0 {
			return nil, scriptpkg.SourceTrace{}, fmt.Errorf("scenetext: prose scene planner returned zero scenes")
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

	return scenes, researchTrace(plan), nil
}
