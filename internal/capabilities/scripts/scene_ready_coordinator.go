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
	"go.uber.org/zap"
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
	renderWg   sync.WaitGroup
	started    time.Time
	transCalls int
	ttsCalls   int

	// translationSlots and ttsSlots are the coordinator's own bounded pools.
	// A (scene, language) task acquires a translation slot only for its own
	// target text and a TTS slot only for its own synthesis, never both at
	// once, so the two certified pool widths stay enforced independently
	// while a scene's languages advance independently of each other.
	translationSlots concurrent.Semaphore
	ttsSlots         concurrent.Semaphore

	// rendered accumulates the certified produced videos of the localized
	// render fan-out fired from this coordinator's scene workers. The runner
	// merges them into the run result once the stream joins (the coordinator
	// has no result pointer of its own).
	rendered []LocalizedRenderResult
	failures []LocalizedRenderFailure
}

// sceneLanguageWork is one independent (scene, language) unit of the
// SceneTextReady downstream: which language, and whether its target text still
// needs translating. The source language never needs translation, so it
// carries no translation dependency at all.
type sceneLanguageWork struct {
	lang             Language
	needsTranslation bool
}

// sceneLanguageOutcome is the per-language result of the fan-out. The
// coordinator applies it to the scene after the join, so the scene's Text and
// Voiceover maps keep exactly one writer.
type sceneLanguageOutcome struct {
	lang       Language
	text       string
	translated bool
	audioRef   AudioReference
}

// buildSceneLanguageWork projects the ordered language list into the
// per-language work items, preserving the canonical dispatch order. A target
// language is translated only when its text is still empty; the source
// language is never a translation work item.
func buildSceneLanguageWork(langs []Language, text map[Language]string, source Language) []sceneLanguageWork {
	work := make([]sceneLanguageWork, 0, len(langs))
	for _, lang := range langs {
		needsTranslation := lang != source && text[lang] == ""
		work = append(work, sceneLanguageWork{lang: lang, needsTranslation: needsTranslation})
	}
	return work
}

// poolSize falls back to the certified default when a configured pool width is
// missing. It keeps the semaphore constructor's >= 1 precondition explicit at
// the call site instead of panicking on a zero-width pool.
func poolSize(value, fallback int) int {
	if value < 1 {
		return fallback
	}
	return value
}

func newSceneReadyCoordinator(ctx context.Context, runner *Runner, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext) *sceneReadyCoordinator {
	return &sceneReadyCoordinator{
		ctx:     ctx,
		runner:  runner,
		runID:   runID,
		req:     req,
		routing: routing,
		exec:    exec,
		results: make(map[int]Scene),
		started: time.Now(),

		translationSlots: concurrent.NewSemaphore(poolSize(runner.translationConcurrency, DefaultTranslationConcurrency)),
		ttsSlots:         concurrent.NewSemaphore(poolSize(runner.ttsConcurrency, DefaultTTSConcurrency)),
	}
}

// languageWorkers sizes the per-scene (scene, language) task pool: one worker
// per translation slot plus one per TTS slot. Both pools can therefore be
// saturated at the same time (which is the point of removing the barrier)
// without any worker holding two slots at once.
func (c *sceneReadyCoordinator) languageWorkers(needsTTS bool) int {
	workers := c.translationSlots.Cap()
	if needsTTS {
		workers += c.ttsSlots.Cap()
	}
	return workers
}

// translateLanguage runs one measured target-language translation under the
// coordinator's translation pool. The slot is released before the caller moves
// on to TTS, so a task never holds two pools at once and no cycle can form.
func (c *sceneReadyCoordinator) translateLanguage(ctx context.Context, itemIdx int, sceneID string, lang Language, sourceText string) (string, error) {
	if err := c.translationSlots.AcquireCtx(ctx); err != nil {
		return "", err
	}
	defer c.translationSlots.Release()

	var value string
	err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage: "translation", Component: "translator", Operation: "translate", Provider: string(lang),
		WorkerID: fmt.Sprintf("translation-%d", itemIdx), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q}", sceneID, lang),
	}, func(measureCtx context.Context) error {
		var err error
		value, err = c.runner.translator.Translate(measureCtx, TranslationInput{SceneID: sceneID, SourceLanguage: c.req.SourceLanguage, TargetLanguage: lang, SourceText: sourceText})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("translate ready scene %s to %s: %w", sceneID, lang, err)
	}
	return value, nil
}

// synthesizeLanguage runs one measured voiceover synthesis under the
// coordinator's TTS pool. The text is the value produced for THIS language
// (translated or already present), never a shared map read.
func (c *sceneReadyCoordinator) synthesizeLanguage(ctx context.Context, itemIdx int, sceneID string, lang Language, text string) (AudioReference, error) {
	if err := c.ttsSlots.AcquireCtx(ctx); err != nil {
		return AudioReference{}, err
	}
	defer c.ttsSlots.Release()

	var audioRef AudioReference
	err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage: "voiceover", Component: kernobs.ComponentTTS, Operation: kernobs.OperationSynthesize, Provider: string(lang),
		WorkerID: fmt.Sprintf("tts-%d", itemIdx), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q}", sceneID, lang),
	}, func(measureCtx context.Context) error {
		var err error
		audioRef, err = c.runner.voiceoverGen.Generate(measureCtx, VoiceoverInput{SceneID: sceneID, Language: lang, Text: text, Project: c.routing.Project, VoiceoverFolderID: c.routing.VoiceoverFolderID, Timing: c.req.Timing})
		return err
	})
	return audioRef, err
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
	if !out.ExecutionMode.AllowsTranslation() || !out.ExecutionMode.AllowsTTS() || !out.ExecutionMode.AllowsGeneratedAudio() {
		return out, nil
	}
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

	// Per-(scene, language) work: a target translates its OWN text; the source
	// language has no translation dependency at all. The order is the canonical
	// dispatch order, so results stay deterministic.
	work := buildSceneLanguageWork(langs, out.Text, c.req.SourceLanguage)
	translationWork := 0
	for _, item := range work {
		if item.needsTranslation {
			translationWork++
		}
	}
	// The source text is read once, on the coordinator goroutine, before the
	// fan-out: workers never re-read the shared map for it.
	sourceText := out.Text[c.req.SourceLanguage]

	mode, err := capabilityaudio.ResolveAudioMode(c.req.Audio, false)
	if err != nil {
		return Scene{}, err
	}
	needsTTS := (mode == capabilityaudio.AudioModeChunkedVoiceover || mode == capabilityaudio.AudioModeCombinedTimeline) && c.runner.voiceoverGen != nil
	if needsTTS && strings.TrimSpace(c.routing.Project) == "" {
		return Scene{}, fmt.Errorf("voiceover publishing requires a resolved Project")
	}

	// A scene records when its downstream branches began: translation for the
	// scene's target work, TTS when any voiceover is produced.
	if translationWork > 0 {
		out.TranslationStartedAt = time.Now().UTC()
	}
	if needsTTS {
		out.TTSStartedAt = time.Now().UTC()
	}

	// ── Independent (scene, language) pipeline ─────────────────────────
	// ONE task per language, instead of "join every translation, then start
	// every TTS". A target language translates its own text and then
	// synthesises; the source language skips translation entirely and starts
	// TTS immediately, so it no longer waits for the scene's target
	// translations. Translation and TTS keep their own bounded pools, so the
	// certified widths are unchanged.
	outcomes, err := concurrent.Map(c.ctx, work, c.languageWorkers(needsTTS), func(ctx context.Context, itemIdx int, item sceneLanguageWork) (sceneLanguageOutcome, error) {
		res := sceneLanguageOutcome{lang: item.lang, text: out.Text[item.lang]}
		if item.needsTranslation {
			translated, err := c.translateLanguage(ctx, itemIdx, out.ID, item.lang, sourceText)
			if err != nil {
				return sceneLanguageOutcome{}, err
			}
			res.text = translated
			res.translated = true
		}
		if needsTTS {
			audioRef, err := c.synthesizeLanguage(ctx, itemIdx, out.ID, item.lang, res.text)
			if err != nil {
				return sceneLanguageOutcome{}, fmt.Errorf("TTS ready scene %s: %w", out.ID, err)
			}
			res.audioRef = audioRef
		}
		return res, nil
	})
	if err != nil {
		return Scene{}, err
	}

	// Apply from the coordinator goroutine: the scene's Text/Voiceover maps
	// have exactly one writer, and the durable artifacts are recorded in
	// canonical (scene, language) order regardless of completion order.
	for _, res := range outcomes {
		if res.translated {
			out.Text[res.lang] = res.text
		}
	}
	// Per-(scene, language) translation correlation: record each target
	// translation so "Spanish Scene 4" is traceable to this exact operation.
	for _, res := range outcomes {
		if !res.translated {
			continue
		}
		if err := c.runner.recordArtifactOperation(c.ctx, c.exec, ArtifactOperation{
			OperationID: artifactOperationID(c.exec.Attempt, OperationTranslation, out.ID, string(res.lang)),
			Kind:        OperationTranslation,
			SceneID:     out.ID,
			Language:    res.lang,
			Status:      "COMPLETED",
		}); err != nil {
			return Scene{}, err
		}
	}
	c.mu.Lock()
	c.transCalls += translationWork
	c.mu.Unlock()

	if !needsTTS {
		return out, nil
	}

	if out.Voiceover == nil {
		out.Voiceover = make(map[Language]AudioReference)
	}
	for _, res := range outcomes {
		out.Voiceover[res.lang] = res.audioRef
		audioRef := res.audioRef
		lang := res.lang
		if lang == c.req.SourceLanguage && out.Clip == nil && !out.ExecutionMode.IsFixedMedia() {
			out.Audio = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: audioRef.ID}
			out.AudioIntents = []capabilityaudio.AudioIntent{out.Audio}
		}
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
		// Localized render fan-out: fire the render in a separate goroutine
		// the moment this language's TTS is final, so Rust starts on this
		// clip while later scenes are still being translated/voiced — and,
		// critically, the TTS worker slot is freed immediately instead of
		// being held for the entire render duration. The renderGate inside
		// the adapter already bounds render concurrency; the OnRendered /
		// OnFailed callbacks capture the certified result asynchronously.
		renderText := out.Text[lang]
		if strings.TrimSpace(renderText) == "" {
			renderText = out.Text[c.req.SourceLanguage]
		}
		renderSourceText := out.Text[c.req.SourceLanguage]
		if strings.TrimSpace(renderSourceText) == "" {
			renderSourceText = renderText
		}
		if strings.TrimSpace(renderSourceText) == "" {
			renderSourceText = c.req.Source.SourceText
			renderText = renderSourceText
		}
		clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderClipFields(out)
		c.renderWg.Add(1)
		go func() {
			defer c.renderWg.Done()
			if err := c.runner.enqueueLocalizedRender(c.ctx, LocalizedRenderInput{
				RunID:          c.runID,
				ParentJobID:    c.exec.JobID,
				SceneID:        out.ID,
				SceneIndex:     out.Index,
				Language:       lang,
				Text:           renderText,
				Voiceover:      audioRef,
				SourceLanguage: c.req.SourceLanguage,
				SourceText:     renderSourceText,
				ClipID:         clipID,
				ClipAssetID:    clipAssetID,
				ClipSHA256:     clipSHA256,
				ClipDurationMS: clipDurationMS,
				Render:         c.req.Render,
				OnRendered: func(rendered LocalizedRenderResult) error {
					c.mu.Lock()
					c.rendered = append(c.rendered, rendered)
					c.mu.Unlock()
					return c.runner.recordLocalizedRender(c.ctx, c.exec, nil, rendered)
				},
				OnFailed: func(failure LocalizedRenderFailure) error {
					c.mu.Lock()
					c.failures = append(c.failures, failure)
					c.mu.Unlock()
					return nil
				},
			}); err != nil {
				c.runner.log.Error("streaming localized render enqueue failed",
					zap.String("scene_id", out.ID),
					zap.String("clip_id", clipID),
					zap.Error(err))
				c.mu.Lock()
				c.failures = append(c.failures, LocalizedRenderFailure{
					SceneID: out.ID, Language: lang, ClipID: clipID,
					ErrorCode: "LOCALIZED_RENDER_ENQUEUE_FAILED", Error: err.Error(),
				})
				c.mu.Unlock()
			}
		}()
	}
	// The scene duration is the source language's narration length: langs[0]
	// is the source language whenever it is set, so outcomes[0] is its
	// voiceover — identical to the previous joined fan-out's tts[0].
	if len(outcomes) > 0 && outcomes[0].audioRef.Duration > 0 && (mode == capabilityaudio.AudioModeCombinedTimeline || out.Clip == nil) {
		out.DurationMS = int64(outcomes[0].audioRef.Duration*1000 + 0.5)
		out.DurationUS = int64(outcomes[0].audioRef.Duration*1_000_000 + 0.5)
	}
	c.mu.Lock()
	c.ttsCalls += len(langs)
	c.mu.Unlock()
	return out, nil
}

func (c *sceneReadyCoordinator) wait(ctx context.Context, scenes []Scene) ([]Scene, *TranslationPipelineMetrics, *AudioPipelineMetrics, error) {
	// Wait for all scene processors (translation + TTS) to finish.
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	}
	// Wait for all async render goroutines to finish so renderedVideos()
	// and renderFailures() are complete when the caller collects them.
	renderDone := make(chan struct{})
	go func() { c.renderWg.Wait(); close(renderDone) }()
	select {
	case <-renderDone:
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
	dbCacheHits := 0
	for i := range ordered {
		for _, ref := range ordered[i].Voiceover {
			if ref.Cached {
				dbCacheHits++
			}
		}
	}
	if run := kernobs.FromContext(ctx); run != nil {
		report := run.Report()
		translation = kernobs.SummarizeOperations(report, "translation", "translate")
		voiceover = kernobs.SummarizeOperations(report, "voiceover", "synthesize")
		// Cache-hit acquisitions still emit a synthesize observation but
		// never reach the TTS provider: subtract them so TTSCalls counts
		// real provider synthesis calls.
		if fresh := voiceover.Calls - int64(dbCacheHits); fresh > 0 {
			voiceover.Calls = fresh
		} else {
			voiceover.Calls = 0
		}
	}
	if translation.Calls == 0 {
		translation.Calls = int64(c.transCalls)
	}
	if voiceover.Calls == 0 && dbCacheHits < c.ttsCalls {
		// Unbound run fallback: count only the fresh (non-cached)
		// acquisitions so a fully-served warm stream reports 0.
		voiceover.Calls = int64(c.ttsCalls - dbCacheHits)
	}
	return ordered, &TranslationPipelineMetrics{Calls: int(translation.Calls), Concurrency: c.runner.translationConcurrency, WallMS: translation.WallMs}, &AudioPipelineMetrics{TTSCalls: int(voiceover.Calls), TTSMS: voiceover.TotalMs, VoiceoverDBCacheHits: dbCacheHits}, nil
}

// renderedVideos returns the certified produced videos accumulated from the
// streaming fan-out, in submission order. The runner merges them into the run
// result once the stream joins so the produced MP4s are never orphaned from
// the run that produced them.
func (c *sceneReadyCoordinator) renderedVideos() []LocalizedRenderResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LocalizedRenderResult(nil), c.rendered...)
}

func (c *sceneReadyCoordinator) renderFailures() []LocalizedRenderFailure {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LocalizedRenderFailure(nil), c.failures...)
}
