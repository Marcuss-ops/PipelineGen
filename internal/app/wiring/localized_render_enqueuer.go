package wiring

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// localizedLocalizer is the narrow seam the adapter needs from the
// LocalizationService (render + upload + doc assembly for one fan-out).
type localizedLocalizer interface {
	Localize(ctx context.Context, in LocalizeInput) (*localization.LocalizeResult, error)
}

type localizedArtifactUploader interface {
	UploadRendered(ctx context.Context, artifact localization.LocalizedClipArtifact, folderID string) (localization.LocalizedClipArtifact, error)
}

var _ localizedLocalizer = (*LocalizationService)(nil)
var _ localizedArtifactUploader = (*LocalizationService)(nil)

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
	tracks detail.TextTrackRepository
	cues   texttracks.TimedCueWriter
	cfg    LocalizedRenderEnqueuerConfig
	log    *zap.Logger
	// cueState accumulates every language's full-span cues before the
	// wholesale transcript-cue replacement: ReplaceTranscriptCues deletes
	// and re-inserts the complete cue set per source clip, so all languages
	// of a scene are staged before the rewrite.
	cueState          map[string]map[string][]detail.TimedCue
	assets            cliprender.AssetResolver
	material          cliprender.AssetMaterializer
	transcript        cliprender.TranscriptResolver
	subtitleArtifacts detail.SubtitleArtifactRepository
	folderMu          sync.Mutex
	folderCache       map[string]string
}

func newLocalizedRenderEnqueuerAdapter(svc localizedLocalizer, tracks detail.TextTrackRepository, cues texttracks.TimedCueWriter, cfg LocalizedRenderEnqueuerConfig, log *zap.Logger, extras ...interface{}) *localizedRenderEnqueuerAdapter {
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
	var subtitleArtifacts detail.SubtitleArtifactRepository
	if len(extras) > 3 {
		subtitleArtifacts, _ = extras[3].(detail.SubtitleArtifactRepository)
	}
	return &localizedRenderEnqueuerAdapter{
		svc:               svc,
		tracks:            tracks,
		cues:              cues,
		cfg:               cfg,
		log:               log,
		cueState:          make(map[string]map[string][]detail.TimedCue),
		assets:            assets,
		material:          material,
		transcript:        transcript,
		subtitleArtifacts: subtitleArtifacts,
		folderCache:       make(map[string]string),
	}
}

var _ scriptgeneration.LocalizedRenderEnqueuer = (*localizedRenderEnqueuerAdapter)(nil)

// ReplaceTranscriptCues keeps the policy adapter compatible with the
// canonical cue-writer port while preserving the concrete writer ownership in
// the composition root.  The policy refactor uses the adapter as a writer
// boundary; it must not silently drop cue replacements.
func (a *localizedRenderEnqueuerAdapter) ReplaceTranscriptCues(ctx context.Context, assetID string, byLang map[string][]detail.TimedCue) error {
	if a == nil || a.cues == nil {
		return fmt.Errorf("localized render: cue writer not wired")
	}
	return a.cues.ReplaceTranscriptCues(ctx, assetID, byLang)
}

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
	built, err := a.buildLocalizedRenderRequest(ctx, in)
	if err != nil {
		return err
	}
	assetID = built.identity.assetID
	clipIDForChild = built.identity.clipIDChild
	clipID := built.identity.clipID
	res, err := a.svc.Localize(ctx, a.localizeInput(in, built))
	if err != nil {
		return fmt.Errorf("localized render: scene %q lang %q: %w", in.SceneID, built.identity.targetLang, err)
	}
	if len(res.Failures) > 0 {
		for _, failure := range res.Failures {
			failureText := ""
			if failure.Err != nil {
				failureText = failure.Err.Error()
			}
			a.log.Error("localized render child failed",
				zap.String("scene_id", in.SceneID),
				zap.String("language", built.identity.targetLang),
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
					SceneID: in.SceneID, Language: scriptgeneration.Language(built.identity.targetLang), ClipID: clipID,
					ErrorCode: code, Error: failureText,
				}); sinkErr != nil {
					return fmt.Errorf("localized render: record failure for scene %q: %w", in.SceneID, sinkErr)
				}
			}
		}
		return fmt.Errorf("localized render: scene %q lang %q produced %d failure(s)", in.SceneID, built.identity.targetLang, len(res.Failures))
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
				return fmt.Errorf("localized render: record produced video for scene %q lang %q: %w", in.SceneID, built.identity.targetLang, err)
			}
		}
	}
	return nil
}

// UploadRendered publishes a durable local RENDERED checkpoint directly.
// It deliberately does not call build/render code beyond resolving the
// destination folder, so a retry after a crash cannot rerun Chronon.
func (a *localizedRenderEnqueuerAdapter) UploadRendered(ctx context.Context, in scriptgeneration.LocalizedRenderInput, staged scriptgeneration.LocalizedRenderResult) error {
	if a == nil || a.svc == nil {
		return fmt.Errorf("localized render recovery: service is not wired")
	}
	if strings.TrimSpace(staged.LocalPath) == "" || strings.TrimSpace(staged.SHA256) == "" {
		return fmt.Errorf("localized render recovery: staged artifact is incomplete")
	}
	info, err := os.Stat(staged.LocalPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("localized render recovery: staged local artifact is unavailable: %s", staged.LocalPath)
	}
	computed, _, err := digest.SHA256File(staged.LocalPath)
	if err != nil || !strings.EqualFold(computed, staged.SHA256) {
		return fmt.Errorf("localized render recovery: staged sha256 mismatch for %s", staged.LocalPath)
	}
	clipID := strings.TrimSpace(staged.ClipID)
	if clipID == "" {
		clipID = strings.TrimSpace(in.ClipID)
	}
	destination, _, err := a.resolveRenderFolders(ctx, in, clipID)
	if err != nil {
		return fmt.Errorf("localized render recovery: resolve destination: %w", err)
	}
	artifact := localization.LocalizedClipArtifact{
		Version: localization.LocalizedClipArtifactVersion, JobID: in.RunID,
		SceneID: staged.SceneID, ClipID: clipID, Language: string(staged.Language),
		AssetID: staged.AssetID, LocalPath: staged.LocalPath, SHA256: staged.SHA256,
		DurationMS: staged.DurationMS, Status: localization.LocalizedClipRendered,
	}
	uploader, ok := a.svc.(localizedArtifactUploader)
	if !ok {
		return fmt.Errorf("localized render recovery: upload-only service is not wired")
	}
	published, err := uploader.UploadRendered(ctx, artifact, destination)
	if err != nil {
		return fmt.Errorf("localized render recovery: upload: %w", err)
	}
	if in.OnRendered != nil {
		return in.OnRendered(scriptgeneration.LocalizedRenderResult{
			SceneID: published.SceneID, SceneIndex: in.SceneIndex, Language: scriptgeneration.Language(published.Language),
			ClipID: published.ClipID, AssetID: published.AssetID, SHA256: published.SHA256,
			DriveFileID: published.DriveFileID, DriveLink: published.DriveLink, DurationMS: published.DurationMS,
			LocalPath: published.LocalPath, Status: string(published.Status), StartedAt: staged.StartedAt,
			FinishedAt: time.Now().UTC(),
		})
	}
	return nil
}

func (a *localizedRenderEnqueuerAdapter) resolveExistingSubtitleLanguage(ctx context.Context, assetID, requested string) (string, error) {
	track, cues, err := a.tracks.FindReady(ctx, assetID, requested, detail.TextTrackTranscript)
	if err != nil {
		return "", fmt.Errorf("localized render: find source subtitles for %q: %w", assetID, err)
	}
	if track != nil && len(cues) > 0 && !invalidSubtitleText(track.TextContent, cues) {
		return requested, nil
	}
	languages, err := a.tracks.ListReadyLanguages(ctx, assetID, detail.TextTrackTranscript)
	if err != nil {
		return "", fmt.Errorf("localized render: list source subtitle languages for %q: %w", assetID, err)
	}
	for _, language := range languages {
		candidate, candidateCues, findErr := a.tracks.FindReady(ctx, assetID, language, detail.TextTrackTranscript)
		if findErr != nil {
			return "", fmt.Errorf("localized render: find fallback subtitles for %q/%q: %w", assetID, language, findErr)
		}
		if candidate != nil && len(candidateCues) > 0 && !invalidSubtitleText(candidate.TextContent, candidateCues) {
			return language, nil
		}
	}
	return "", nil
}

func (a *localizedRenderEnqueuerAdapter) ensureDatabaseSubtitles(ctx context.Context, assetID, sourceLang, targetLang string, in scriptgeneration.LocalizedRenderInput) (bool, error) {
	if a.tracks == nil {
		return false, fmt.Errorf("localized render: text track repository not wired")
	}
	// A durable ASS artifact is an existing input, never a newly generated
	// subtitle for this render. Keep this fact separate from the text-track
	// lookup: an ASS may already be on Drive even when an older database row
	// has incomplete cue materialization. In that case fail closed; do not
	// invoke Whisper and do not create a duplicate Drive artifact.
	existingSubtitle := false
	if a.subtitleArtifacts != nil {
		artifact, artifactErr := a.subtitleArtifacts.FindCurrent(ctx, assetID, sourceLang, detail.SubtitleFormatASS)
		if artifactErr != nil {
			return false, fmt.Errorf("localized render: find existing subtitle artifact for %q: %w", assetID, artifactErr)
		}
		existingSubtitle = artifact != nil && artifact.Status == detail.SubtitleStatusReady && artifact.IsCurrent &&
			strings.TrimSpace(artifact.DriveFileID) != "" && strings.TrimSpace(artifact.DriveURL) != ""
	}
	track, cues, err := a.tracks.FindReady(ctx, assetID, sourceLang, detail.TextTrackTranscript)
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
		if existingSubtitle {
			return false, fmt.Errorf("localized render: existing subtitle artifact for %q has no READY timed transcript; refusing ASR regeneration", assetID)
		}
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
		track, cues, err = a.tracks.FindReady(ctx, assetID, sourceLang, detail.TextTrackTranscript)
		if err != nil || track == nil || len(cues) == 0 || invalidSubtitleText(track.TextContent, cues) {
			return false, fmt.Errorf("localized render: generated subtitles for %q were not readable from database", assetID)
		}
	}
	// Existing canonical subtitles are inputs to this render. They must never
	// activate the localization subtitle uploader, even when the caller's
	// request was assembled by a retry path.
	if existingSubtitle {
		generated = false
	}
	if targetLang != sourceLang {
		target, targetCues, err := a.tracks.FindReady(ctx, assetID, targetLang, detail.TextTrackTranscript)
		if err != nil {
			return false, fmt.Errorf("localized render: find translated subtitles for %q/%q: %w", assetID, targetLang, err)
		}
		if target == nil || len(targetCues) == 0 {
			return false, fmt.Errorf("localized render: no timed database subtitles for target language %q on %q", targetLang, assetID)
		}
	}
	return generated, nil
}

func invalidSubtitleText(trackText string, cues []detail.TimedCue) bool {
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
func wireLocalizedRenderEnqueuer(cfg *config.Config, root *ComposeRoot, log *zap.Logger, runner *scriptgeneration.Runner) {
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
	}, log, resolver, materializer, transcriptResolver, root.Repos.SubtitleArtifactRepo)
	runner.SetLocalizedRenderEnqueuer(adapter)
	log.Info("wireScriptFlow: localized render fan-out wired to LocalizationService (Rust render_clip)",
		zap.String("source_language", LocalizationConfigFromConfig(cfg).SourceLanguage),
		zap.String("clips_folder", cfg.Drive.ClipsFolder()))
}
