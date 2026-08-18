package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// sceneReadyCoordinator owns the asynchronous SceneTextReady downstream
// pipeline. Generation submits immutable scenes and continues; each scene's
// translation and TTS work is joined only when the stream ends.
type sceneReadyCoordinator struct {
	runner  *Runner
	runID   string
	req     GenerateRequest
	routing kernelscript.ArtifactRoutingContext
	ctx     context.Context
	exec    ExecutionContext

	mu         sync.Mutex
	results    map[int]Scene
	errors     []error
	wg         sync.WaitGroup
	started    time.Time
	transCalls int
	ttsCalls   int
}

func newSceneReadyCoordinator(ctx context.Context, runner *Runner, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext) *sceneReadyCoordinator {
	return &sceneReadyCoordinator{ctx: ctx, runner: runner, runID: runID, req: req, routing: routing, exec: exec, results: make(map[int]Scene), started: time.Now()}
}

func (c *sceneReadyCoordinator) submit(scene Scene) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		out, err := c.process(scene)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.errors = append(c.errors, err)
			return
		}
		c.results[scene.Index] = out
	}()
}

func (c *sceneReadyCoordinator) process(scene Scene) (Scene, error) {
	out := scene
	if out.Text == nil {
		out.Text = make(map[Language]string)
	}
	langs := make([]Language, 0, len(c.req.Languages))
	seen := map[Language]bool{}
	for _, lang := range append([]Language{c.req.SourceLanguage}, c.req.Languages...) {
		if lang != "" && !seen[lang] {
			seen[lang] = true
			langs = append(langs, lang)
		}
	}
	transWork := make([]Language, 0, len(langs))
	for _, lang := range langs {
		if lang != c.req.SourceLanguage && out.Text[lang] == "" {
			transWork = append(transWork, lang)
		}
	}
	transStart := time.Now().UTC()
	if len(transWork) > 0 {
		out.TranslationStartedAt = transStart
	}
	translated, err := concurrent.Map(c.ctx, transWork, c.runner.translationConcurrency, func(ctx context.Context, worker int, lang Language) (string, error) {
		var value string
		err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage: "translation", Component: "translator", Operation: "translate", Provider: string(lang),
			WorkerID: fmt.Sprintf("translation-%d", worker), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q}", out.ID, lang),
		}, func(measureCtx context.Context) error {
			var err error
			value, err = c.runner.translator.Translate(measureCtx, TranslationInput{SceneID: out.ID, SourceLanguage: c.req.SourceLanguage, TargetLanguage: lang, SourceText: out.Text[c.req.SourceLanguage]})
			return err
		})
		if err != nil {
			return "", fmt.Errorf("translate ready scene %s to %s: %w", out.ID, lang, err)
		}
		return value, nil
	})
	if err != nil {
		return Scene{}, err
	}
	for i, lang := range transWork {
		out.Text[lang] = translated[i]
	}
	// Per-(scene, language) translation correlation: record each target
	// translation so "Spanish Scene 4" is traceable to this exact operation.
	for _, lang := range transWork {
		if err := c.runner.recordArtifactOperation(c.ctx, c.exec, ArtifactOperation{
			OperationID: artifactOperationID(c.exec.Attempt, OperationTranslation, out.ID, string(lang)),
			Kind:        OperationTranslation,
			SceneID:     out.ID,
			Language:    lang,
			Status:      "COMPLETED",
		}); err != nil {
			return Scene{}, err
		}
	}
	c.mu.Lock()
	c.transCalls += len(transWork)
	c.mu.Unlock()

	mode, err := capabilityaudio.ResolveAudioMode(c.req.Audio, false)
	if err != nil {
		return Scene{}, err
	}
	needsTTS := (mode == capabilityaudio.AudioModeChunkedVoiceover || mode == capabilityaudio.AudioModeCombinedTimeline) && c.runner.voiceoverGen != nil
	if !needsTTS {
		return out, nil
	}
	if strings.TrimSpace(c.routing.Project) == "" {
		return Scene{}, fmt.Errorf("voiceover publishing requires a resolved Project")
	}
	ttsStart := time.Now().UTC()
	out.TTSStartedAt = ttsStart
	tts, err := concurrent.Map(c.ctx, langs, c.runner.ttsConcurrency, func(ctx context.Context, worker int, lang Language) (AudioReference, error) {
		var audioRef AudioReference
		err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage: "voiceover", Component: kernobs.ComponentTTS, Operation: kernobs.OperationSynthesize, Provider: string(lang),
			WorkerID: fmt.Sprintf("tts-%d", worker), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q}", out.ID, lang),
		}, func(measureCtx context.Context) error {
			var err error
			audioRef, err = c.runner.voiceoverGen.Generate(measureCtx, VoiceoverInput{SceneID: out.ID, Language: lang, Text: out.Text[lang], Project: c.routing.Project, VoiceoverFolderID: c.routing.VoiceoverFolderID, Timing: c.req.Timing})
			return err
		})
		return audioRef, err
	})
	if err != nil {
		return Scene{}, fmt.Errorf("TTS ready scene %s: %w", out.ID, err)
	}
	if out.Voiceover == nil {
		out.Voiceover = make(map[Language]AudioReference)
	}
	for i, lang := range langs {
		out.Voiceover[lang] = tts[i]
		// Per-(scene, language) TTS correlation: record the produced
		// voiceover asset so the translation → TTS → render → Drive lineage
		// is joinable on (scene_id, language, asset_id).
		if err := c.runner.recordArtifactOperation(c.ctx, c.exec, ArtifactOperation{
			OperationID: artifactOperationID(c.exec.Attempt, OperationTTS, out.ID, string(lang)),
			Kind:        OperationTTS,
			SceneID:     out.ID,
			Language:    lang,
			AssetID:     out.Voiceover[lang].ID,
			Status:      "COMPLETED",
		}); err != nil {
			return Scene{}, err
		}
		// Localized render fan-out: fire the render the moment this language's
		// TTS is final, so Rust starts on this clip while later scenes are
		// still being translated/voiced (never a global join).
		clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderClipFields(out)
		if err := c.runner.enqueueLocalizedRender(c.ctx, LocalizedRenderInput{
			RunID:          c.runID,
			SceneID:        out.ID,
			SceneIndex:     out.Index,
			Language:       lang,
			Text:           out.Text[lang],
			Voiceover:      out.Voiceover[lang],
			SourceLanguage: c.req.SourceLanguage,
			SourceText:     out.Text[c.req.SourceLanguage],
			ClipID:         clipID,
			ClipAssetID:    clipAssetID,
			ClipSHA256:     clipSHA256,
			ClipDurationMS: clipDurationMS,
		}); err != nil {
			return Scene{}, fmt.Errorf("localized render scene %s lang %s: %w", out.ID, lang, err)
		}
	}
	if (mode == capabilityaudio.AudioModeCombinedTimeline || out.Clip == nil) && len(tts) > 0 && tts[0].Duration > 0 {
		out.DurationMS = int64(tts[0].Duration*1000 + 0.5)
		out.DurationUS = int64(tts[0].Duration*1_000_000 + 0.5)
	}
	c.mu.Lock()
	c.ttsCalls += len(langs)
	c.mu.Unlock()
	return out, nil
}

func (c *sceneReadyCoordinator) wait(ctx context.Context, scenes []Scene) ([]Scene, *TranslationPipelineMetrics, *AudioPipelineMetrics, error) {
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.errors) > 0 {
		return nil, nil, nil, c.errors[0]
	}
	ordered := make([]Scene, len(scenes))
	for i := range scenes {
		value, ok := c.results[scenes[i].Index]
		if !ok {
			return nil, nil, nil, fmt.Errorf("scene ready coordinator missing scene %d", scenes[i].Index)
		}
		ordered[i] = value
	}
	var translation, voiceover kernobs.OperationSummary
	if run := kernobs.FromContext(ctx); run != nil {
		report := run.Report()
		translation = kernobs.SummarizeOperations(report, "translation", "translate")
		voiceover = kernobs.SummarizeOperations(report, "voiceover", "synthesize")
	}
	if translation.Calls == 0 {
		translation.Calls = int64(c.transCalls)
	}
	if voiceover.Calls == 0 {
		voiceover.Calls = int64(c.ttsCalls)
	}
	return ordered, &TranslationPipelineMetrics{Calls: int(translation.Calls), Concurrency: c.runner.translationConcurrency, WallMS: translation.WallMs}, &AudioPipelineMetrics{TTSCalls: int(voiceover.Calls), TTSMS: voiceover.TotalMs}, nil
}
