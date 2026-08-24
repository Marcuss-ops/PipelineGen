package capabilities

// localized_render_enqueuer.go bridges the script-generation runner's
// per-(scene, language) localized-render fan-out to the canonical
// LocalizationService (the Rust render_clip boundary). The runner emits a
// LocalizedRenderInput the moment one scene's translation + TTS for a language
// are final; this adapter turns it into a single-language LocalizeInput so
// Rust starts on that clip without waiting for the other scenes/languages.
//
// The LocalizationService resolves the source clip + transcript/subtitle text
// tracks from the canonical stores by (asset_id, language). The runner holds
// the freshly translated text in memory only, so the adapter persists the
// source transcript + translated subtitle text tracks (READY, idempotent)
// and their full-span timed cues BEFORE delegating to Localize — otherwise
// the plan builder would find no READY track and fail closed.
//
// godlike/07 fail-closed: an enqueue error is returned to the runner (which
// fails the run), never a silent skip. A scene with no source clip (audio-only)
// is a legitimate no-op: there is nothing to burn subtitles onto.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// localizedLocalizer is the narrow seam the adapter needs from the
// LocalizationService (render + upload + doc assembly for one fan-out).
type localizedLocalizer interface {
	Localize(ctx context.Context, in LocalizeInput) (*localization.LocalizeResult, error)
}

var _ localizedLocalizer = (*LocalizationService)(nil)

// LocalizedRenderEnqueuerConfig pins the deployment-scoped facts the adapter
// needs to map a LocalizedRenderInput into a LocalizeInput. It is resolved at
// composition time, never re-derived per enqueue.
type LocalizedRenderEnqueuerConfig struct {
	// SourceLanguage is the fallback source language when the input does not
	// carry one (the canonical clip source language).
	SourceLanguage string
	// FolderID is the Drive folder the rendered localized clips upload into.
	FolderID string
	// FolderAdmin creates/reuses a per-script child under FolderID.
	FolderAdmin drive.Admin
	JobBroker   job.JobBroker
	// DocFolderID is the Drive folder the localization manifest doc publishes
	// into. Empty disables doc assembly routing (Localize still runs render +
	// upload; the doc publish fails closed if the localization service is
	// built with a doc publisher and no folder).
	DocFolderID string
	// SubtitleFolderID is the Drive root for generated ASS subtitle artifacts.
	SubtitleFolderID string
	// Concurrency is the render fan-out parallelism per Localize call (a
	// single-language call needs only one slot). <1 is clamped to
	// localization.DefaultRenderConcurrency.
	Concurrency int
	// GlobalConcurrency bounds Localize calls across the runner. GPU rendering
	// starts at one slot until VRAM peak usage has been measured safely.
	GlobalConcurrency int
}

type inlineRenderChild struct {
	broker   job.JobBroker
	id       string
	worker   string
	lease    string
	revision int
}

func (a *localizedRenderEnqueuerAdapter) beginChild(ctx context.Context, in scriptgeneration.LocalizedRenderInput, clipID string) (*inlineRenderChild, error) {
	// The production worker claims queued jobs globally, even when a caller
	// supplies a narrow type filter. Creating an inline child here therefore
	// races with the worker (or reaches a worker with no child handler) and
	// causes a CAS-fence failure. Keep child-job persistence disabled until the
	// broker exposes an atomic claim-by-ID operation; the parent run already
	// records each certified render and its timing.
	return nil, nil
}

func (c *inlineRenderChild) finish(ctx context.Context, result any, renderErr error) {
	if c == nil || c.broker == nil {
		return
	}
	if renderErr != nil {
		_ = c.broker.Fail(ctx, c.id, c.worker, c.lease, c.revision, renderErr.Error())
		return
	}
	data, _ := json.Marshal(result)
	_ = c.broker.Complete(ctx, c.id, c.worker, c.lease, c.revision, data)
}

// localizedRenderEnqueuerAdapter implements scriptgeneration.LocalizedRenderEnqueuer
// over the canonical LocalizationService. It is safe for concurrent
// EnqueueLocalizedRender calls (the fan-out fires per language in parallel).
type localizedRenderEnqueuerAdapter struct {
	svc    localizedLocalizer
	tracks asset.TextTrackRepository
	cues   texttracks.TimedCueWriter
	cfg    LocalizedRenderEnqueuerConfig
	log    *zap.Logger

	// cueMu serializes the per-asset cue replacement. ReplaceTranscriptCues
	// REPLACES the whole transcript-cue set for an asset (delete-all +
	// insert), so two languages of the same scene (same source clip) must not
	// race: the adapter accumulates every language's full-span cue and re-writes
	// the complete set under the lock.
	cueMu       sync.Mutex
	childMu     sync.Mutex
	cueState    map[string]map[string][]asset.TimedCue
	assets      cliprender.AssetResolver
	material    cliprender.AssetMaterializer
	transcript  cliprender.TranscriptResolver
	folderMu    sync.Mutex
	folderCache map[string]string
	renderGate  chan struct{}
}

func newLocalizedRenderEnqueuerAdapter(svc localizedLocalizer, tracks asset.TextTrackRepository, cues texttracks.TimedCueWriter, cfg LocalizedRenderEnqueuerConfig, log *zap.Logger, extras ...interface{}) *localizedRenderEnqueuerAdapter {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = localization.DefaultRenderConcurrency
	}
	if cfg.GlobalConcurrency < 1 {
		cfg.GlobalConcurrency = 1
	}
	var assets cliprender.AssetResolver
	var material cliprender.AssetMaterializer
	if len(extras) > 0 {
		assets, _ = extras[0].(cliprender.AssetResolver)
	}
	if len(extras) > 1 {
		material, _ = extras[1].(cliprender.AssetMaterializer)
	}
	var transcript cliprender.TranscriptResolver
	if len(extras) > 2 {
		transcript, _ = extras[2].(cliprender.TranscriptResolver)
	}
	return &localizedRenderEnqueuerAdapter{
		svc:         svc,
		tracks:      tracks,
		cues:        cues,
		cfg:         cfg,
		log:         log,
		cueState:    make(map[string]map[string][]asset.TimedCue),
		assets:      assets,
		material:    material,
		transcript:  transcript,
		folderCache: make(map[string]string),
		renderGate:  make(chan struct{}, cfg.GlobalConcurrency),
	}
}

var _ scriptgeneration.LocalizedRenderEnqueuer = (*localizedRenderEnqueuerAdapter)(nil)

// EnqueueLocalizedRender persists the source transcript + translated subtitle
// text tracks (with full-span cues), then runs a single-language Localize.
func (a *localizedRenderEnqueuerAdapter) EnqueueLocalizedRender(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (err error) {
	if a == nil || a.svc == nil {
		return nil // render not registered: legitimate no-op
	}
	renderStartedAt := time.Now().UTC()
	assetID := strings.TrimSpace(in.ClipAssetID)
	if assetID == "" {
		assetID = strings.TrimSpace(in.ClipID)
	}
	clipIDForChild := strings.TrimSpace(in.ClipID)
	if clipIDForChild == "" {
		clipIDForChild = assetID
	}
	child, childErr := a.beginChild(ctx, in, clipIDForChild)
	if childErr != nil {
		return childErr
	}
	defer func() { child.finish(ctx, map[string]any{"scene_id": in.SceneID, "clip_id": clipIDForChild}, err) }()
	if assetID == "" {
		// Audio-only scene: no source clip to burn subtitles onto.
		return nil
	}
	sourceLang := string(in.SourceLanguage)
	if sourceLang == "" {
		sourceLang = strings.TrimSpace(a.cfg.SourceLanguage)
	}
	targetLang := string(in.Language)
	if targetLang == "" {
		targetLang = sourceLang
	}
	if sourceLang == "" || targetLang == "" {
		return fmt.Errorf("localized render: source and target language are required (scene %q)", in.SceneID)
	}

	// Subtitles are never derived from scene narration/clip descriptions. They
	// must come from the canonical timed transcript in SQLite. If it is absent,
	// acquire/transcribe it once, persist it, and let the next render reuse it.
	generatedSubtitles, err := a.ensureDatabaseSubtitles(ctx, assetID, sourceLang, targetLang, in)
	if err != nil {
		return err
	}

	// 3. Single-language fan-out: Rust renders this clip in this language now.
	var watermark *cliprender.MaterializedAsset
	var watermarkSpec *cliprender.WatermarkSpec
	if in.Render.Watermark != nil && in.Render.Watermark.Enabled {
		if strings.TrimSpace(in.Render.Watermark.Text) == "" && (strings.TrimSpace(in.Render.Watermark.AssetID) == "" || a.assets == nil || a.material == nil) {
			return fmt.Errorf("localized render: watermark requested but its asset resolver is not wired")
		}
		if strings.TrimSpace(in.Render.Watermark.AssetID) != "" {
			ref, err := a.assets.ResolveAsset(ctx, in.Render.Watermark.AssetID)
			if err != nil {
				return fmt.Errorf("localized render: resolve watermark %q: %w", in.Render.Watermark.AssetID, err)
			}
			watermark, err = a.material.Materialize(ctx, *ref)
			if err != nil {
				return fmt.Errorf("localized render: materialize watermark %q: %w", in.Render.Watermark.AssetID, err)
			}
		}
		watermarkSpec = &cliprender.WatermarkSpec{
			Enabled: true, AssetID: in.Render.Watermark.AssetID,
			Text:     in.Render.Watermark.Text,
			Position: in.Render.Watermark.Position, Opacity: in.Render.Watermark.Opacity,
			MarginPX: in.Render.Watermark.MarginPX,
		}
	}
	req := localization.LocalizationRequest{RenderConcurrency: a.cfg.Concurrency}
	req.Languages = []localization.LanguageRequest{{Language: targetLang, Priority: 0}}
	req.Normalize()

	clipID := strings.TrimSpace(in.ClipID)
	if clipID == "" {
		clipID = assetID
	}
	destinationFolderID := strings.TrimSpace(a.cfg.FolderID)
	if strings.TrimSpace(in.Render.DriveFolderID) != "" {
		destinationFolderID = strings.TrimSpace(in.Render.DriveFolderID)
	}
	subtitleFolderID := strings.TrimSpace(a.cfg.SubtitleFolderID)
	if subtitleFolderID != "" {
		if a.cfg.FolderAdmin == nil {
			return fmt.Errorf("localized render: subtitle folder admin is not wired")
		}
		cacheKey := "subtitle\x00" + subtitleFolderID + "\x00" + clipID
		a.folderMu.Lock()
		cached := a.folderCache[cacheKey]
		if cached != "" {
			a.folderMu.Unlock()
			subtitleFolderID = cached
		} else {
			resolved, err := a.cfg.FolderAdmin.GetOrCreateFolder(ctx, clipID, subtitleFolderID)
			if err != nil {
				a.folderMu.Unlock()
				return fmt.Errorf("localized render: ensure subtitle subfolder %q: %w", clipID, err)
			}
			a.folderCache[cacheKey] = resolved
			a.folderMu.Unlock()
			subtitleFolderID = resolved
		}
	}
	if subfolder := strings.TrimSpace(in.Render.DriveSubfolderName); subfolder != "" {
		if a.cfg.FolderAdmin == nil {
			return fmt.Errorf("localized render: Drive folder admin is not wired for subfolder %q", subfolder)
		}
		cacheKey := destinationFolderID + "\x00" + subfolder
		a.folderMu.Lock()
		cached := a.folderCache[cacheKey]
		if cached != "" {
			a.folderMu.Unlock()
			destinationFolderID = cached
		} else {
			var err error
			destinationFolderID, err = a.cfg.FolderAdmin.GetOrCreateFolder(ctx, subfolder, destinationFolderID)
			if err != nil {
				a.folderMu.Unlock()
				return fmt.Errorf("localized render: ensure Drive subfolder %q: %w", subfolder, err)
			}
			a.folderCache[cacheKey] = destinationFolderID
			a.folderMu.Unlock()
		}
	}
	a.renderGate <- struct{}{}
	defer func() { <-a.renderGate }()
	res, err := a.svc.Localize(ctx, LocalizeInput{
		AssetID:                assetID,
		JobID:                  in.RunID,
		SceneID:                in.SceneID,
		ClipID:                 clipID,
		SourceLanguage:         sourceLang,
		Request:                req,
		FolderID:               destinationFolderID,
		SubtitleFolderID:       subtitleFolderID,
		UploadSubtitleArtifact: generatedSubtitles,
		DocTitle:               fmt.Sprintf("Localized — %s (%s)", clipID, targetLang),
		DocFolderID:            a.cfg.DocFolderID,
		DocIdempotencyKey:      in.RunID + ":" + in.SceneID + ":" + targetLang,
		SkipDocument:           true,
		Watermark:              watermark,
		WatermarkSpec:          watermarkSpec,
		WatermarkText: func() string {
			if in.Render.Watermark == nil {
				return ""
			}
			return in.Render.Watermark.Text
		}(),
	})
	if err != nil {
		return fmt.Errorf("localized render: scene %q lang %q: %w", in.SceneID, targetLang, err)
	}
	if len(res.Failures) > 0 {
		for _, failure := range res.Failures {
			failureText := ""
			if failure.Err != nil {
				failureText = failure.Err.Error()
			}
			a.log.Error("localized render child failed",
				zap.String("scene_id", in.SceneID),
				zap.String("language", targetLang),
				zap.String("clip_id", clipID),
				zap.String("error", failureText),
			)
			code := "LOCALIZED_RENDER_FAILED"
			upper := strings.ToUpper(failureText)
			if strings.Contains(upper, "CUDA") || strings.Contains(upper, "OUT OF MEMORY") {
				code = "CUDA_OUT_OF_MEMORY"
			}
			if in.OnFailed != nil {
				if sinkErr := in.OnFailed(scriptgeneration.LocalizedRenderFailure{
					SceneID: in.SceneID, Language: scriptgeneration.Language(targetLang), ClipID: clipID,
					ErrorCode: code, Error: failureText,
				}); sinkErr != nil {
					return fmt.Errorf("localized render: record failure for scene %q: %w", in.SceneID, sinkErr)
				}
			}
		}
		return fmt.Errorf("localized render: scene %q lang %q produced %d failure(s)", in.SceneID, targetLang, len(res.Failures))
	}
	// Project the certified produced videos back to the runner so the final
	// MP4 (asset id, sha256, Drive link) is recorded on the run result — a
	// video that was rendered and uploaded must never be orphaned from the
	// run that produced it. Fail-closed: a sink error fails the enqueue.
	if in.OnRendered != nil {
		for _, artifact := range res.Artifacts {
			renderFinishedAt := time.Now().UTC()
			if err := in.OnRendered(scriptgeneration.LocalizedRenderResult{
				SceneID:     artifact.SceneID,
				SceneIndex:  in.SceneIndex,
				Language:    scriptgeneration.Language(artifact.Language),
				ClipID:      artifact.ClipID,
				AssetID:     artifact.AssetID,
				SHA256:      artifact.SHA256,
				DriveFileID: artifact.DriveFileID,
				DriveLink:   artifact.DriveLink,
				DurationMS:  artifact.DurationMS,
				LocalPath:   artifact.LocalPath,
				Status:      string(artifact.Status),
				StartedAt:   renderStartedAt,
				FinishedAt:  renderFinishedAt,
				WallMS:      renderFinishedAt.Sub(renderStartedAt).Milliseconds(),
			}); err != nil {
				return fmt.Errorf("localized render: record produced video for scene %q lang %q: %w", in.SceneID, targetLang, err)
			}
		}
	}
	return nil
}

func (a *localizedRenderEnqueuerAdapter) ensureDatabaseSubtitles(ctx context.Context, assetID, sourceLang, targetLang string, in scriptgeneration.LocalizedRenderInput) (bool, error) {
	if a.tracks == nil {
		return false, fmt.Errorf("localized render: text track repository not wired")
	}
	track, cues, err := a.tracks.FindReady(ctx, assetID, sourceLang, asset.TextTrackTranscript)
	if err != nil {
		return false, fmt.Errorf("localized render: find source subtitles for %q: %w", assetID, err)
	}
	generated := false
	if track != nil && invalidSubtitleText(track.TextContent, cues) {
		// Older runs incorrectly persisted the scene brief as transcript.
		// Treat it as a cache miss so the canonical clip transcriber replaces
		// both the track and its timed cues.
		track, cues = nil, nil
	}
	if track == nil || len(cues) == 0 {
		if a.transcript == nil || a.assets == nil || a.material == nil {
			return false, fmt.Errorf("localized render: no timed subtitles in database for %q and transcript generation is not wired", assetID)
		}
		ref, err := a.assets.ResolveAsset(ctx, assetID)
		if err != nil {
			return false, fmt.Errorf("localized render: resolve source for subtitle generation %q: %w", assetID, err)
		}
		source, err := a.material.Materialize(ctx, *ref)
		if err != nil {
			return false, fmt.Errorf("localized render: materialize source for subtitle generation %q: %w", assetID, err)
		}
		generatedResult, err := a.transcript.Generate(ctx, cliprender.TranscriptInput{
			AssetID: assetID, Language: sourceLang, Mode: "generate", Persist: true,
			SourceSHA256: source.SHA256,
		}, source)
		if err != nil || generatedResult == nil || len(generatedResult.Cues) == 0 {
			if err == nil {
				err = fmt.Errorf("empty timed transcript")
			}
			return false, fmt.Errorf("localized render: generate/persist subtitles for %q: %w", assetID, err)
		}
		generated = true
		track, cues, err = a.tracks.FindReady(ctx, assetID, sourceLang, asset.TextTrackTranscript)
		if err != nil || track == nil || len(cues) == 0 || invalidSubtitleText(track.TextContent, cues) {
			return false, fmt.Errorf("localized render: generated subtitles for %q were not readable from database", assetID)
		}
	}
	if targetLang != sourceLang {
		target, targetCues, err := a.tracks.FindReady(ctx, assetID, targetLang, asset.TextTrackTranscript)
		if err != nil {
			return false, fmt.Errorf("localized render: find translated subtitles for %q/%q: %w", assetID, targetLang, err)
		}
		if target == nil || len(targetCues) == 0 {
			return false, fmt.Errorf("localized render: no timed database subtitles for target language %q on %q", targetLang, assetID)
		}
	}
	return generated, nil
}

func invalidSubtitleText(trackText string, cues []asset.TimedCue) bool {
	text := strings.ToLower(strings.TrimSpace(trackText))
	if strings.Contains(text, "clip description:") || strings.Contains(text, "write a ") || strings.Contains(text, "source text:") {
		return true
	}
	for _, cue := range cues {
		cueText := strings.ToLower(strings.TrimSpace(cue.Text))
		if strings.Contains(cueText, "clip description:") || strings.Contains(cueText, "write a ") {
			return true
		}
	}
	return false
}

// wireLocalizedRenderEnqueuer wires the runner's localized-render fan-out to
// the canonical LocalizationService when the full localization stack is
// available. It is best-effort: a missing asset registry, Drive publisher/doc
// client, text-track store, or cue writer leaves the fan-out unwired (a
// legitimate no-op) rather than failing composition — the runner only treats a
// nil enqueuer as "render not registered".
func wireLocalizedRenderEnqueuer(cfg *config.Config, root *wiring.ComposeRoot, log *zap.Logger, runner *scriptgeneration.Runner) {
	if runner == nil {
		return
	}
	if cfg == nil || root == nil || root.Repos == nil || root.Repos.TextTrackRepo == nil || (root.Domains == nil || root.Domains.CueWriter == nil) {
		log.Warn("wireScriptFlow: localized render fan-out not wired (text-track store or cue writer missing)")
		return
	}
	svc, err := BuildLocalizationService(cfg, root, log)
	if err != nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (localization service unavailable)", zap.Error(err))
		return
	}
	resolver, resolverErr := clipadapters.NewClipRenderAssetResolver(root.Repos.Assets, log)
	if resolverErr != nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (asset resolver unavailable)", zap.Error(resolverErr))
		return
	}
	materializer, materializerErr := clipadapters.NewClipRenderMaterializer(root.Drive.Reader, filepath.Join(cfg.Storage.TempPath(), "localization"), log)
	if materializerErr != nil {
		log.Warn("wireScriptFlow: localized render fan-out not wired (asset materializer unavailable)", zap.Error(materializerErr))
		return
	}
	// Reuse the canonical clip-render transcript resolver. It first reads the
	// READY timed transcript from SQLite; only a cache miss may invoke Whisper,
	// and the generated result is persisted by that resolver.
	transcriptResolver := clipadapters.NewClipRenderTranscriptResolver(log)
	transcriptResolver.SetRepo(root.Repos.TextTrackRepo)
	if root.TextTracks != nil {
		transcriptResolver.SetAcquire(root.TextTracks.AcquireService)
	}
	if root.Domains != nil {
		transcriptResolver.SetCueWriter(root.Domains.CueWriter)
	}
	if streaming, streamErr := clipadapters.NewClipRenderStreamingTranscriber(cfg, log); streamErr == nil {
		transcriptResolver.SetStreaming(streaming)
	} else {
		log.Warn("wireScriptFlow: streaming subtitle generation unavailable; using acquisition fallback", zap.Error(streamErr))
	}
	adapter := newLocalizedRenderEnqueuerAdapter(svc, root.Repos.TextTrackRepo, root.Domains.CueWriter, LocalizedRenderEnqueuerConfig{
		SourceLanguage:    LocalizationConfigFromConfig(cfg).SourceLanguage,
		FolderID:          cfg.Drive.ClipsFolder(),
		FolderAdmin:       root.Drive.Admin,
		JobBroker:         root.Jobs.Repo,
		DocFolderID:       cfg.Scripts.ScriptDocsFolderID,
		SubtitleFolderID:  "1noSFMK_UeF_Xo-RRZWvH10U7tiL1jPP1",
		Concurrency:       cfg.Scripts.LocalizedRenderConcurrency,
		GlobalConcurrency: cfg.Scripts.LocalizedRenderGlobalConcurrency,
	}, log, resolver, materializer, transcriptResolver)
	runner.SetLocalizedRenderEnqueuer(adapter)
	log.Info("wireScriptFlow: localized render fan-out wired to LocalizationService (Rust render_clip)",
		zap.String("source_language", LocalizationConfigFromConfig(cfg).SourceLanguage),
		zap.String("clips_folder", cfg.Drive.ClipsFolder()))
}
