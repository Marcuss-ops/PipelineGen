package app

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
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
	// DocFolderID is the Drive folder the localization manifest doc publishes
	// into. Empty disables doc assembly routing (Localize still runs render +
	// upload; the doc publish fails closed if the localization service is
	// built with a doc publisher and no folder).
	DocFolderID string
	// Concurrency is the render fan-out parallelism per Localize call (a
	// single-language call needs only one slot). <1 is clamped to
	// localization.DefaultRenderConcurrency.
	Concurrency int
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
	cueMu    sync.Mutex
	cueState map[string]map[string][]asset.TimedCue
	assets   cliprender.AssetResolver
	material cliprender.AssetMaterializer
}

func newLocalizedRenderEnqueuerAdapter(svc localizedLocalizer, tracks asset.TextTrackRepository, cues texttracks.TimedCueWriter, cfg LocalizedRenderEnqueuerConfig, log *zap.Logger, extras ...interface{}) *localizedRenderEnqueuerAdapter {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = localization.DefaultRenderConcurrency
	}
	var assets cliprender.AssetResolver
	var material cliprender.AssetMaterializer
	if len(extras) > 0 {
		assets, _ = extras[0].(cliprender.AssetResolver)
	}
	if len(extras) > 1 {
		material, _ = extras[1].(cliprender.AssetMaterializer)
	}
	return &localizedRenderEnqueuerAdapter{
		svc:      svc,
		tracks:   tracks,
		cues:     cues,
		cfg:      cfg,
		log:      log,
		cueState: make(map[string]map[string][]asset.TimedCue),
		assets:   assets,
		material: material,
	}
}

var _ scriptgeneration.LocalizedRenderEnqueuer = (*localizedRenderEnqueuerAdapter)(nil)

// EnqueueLocalizedRender persists the source transcript + translated subtitle
// text tracks (with full-span cues), then runs a single-language Localize.
func (a *localizedRenderEnqueuerAdapter) EnqueueLocalizedRender(ctx context.Context, in scriptgeneration.LocalizedRenderInput) error {
	if a == nil || a.svc == nil {
		return nil // render not registered: legitimate no-op
	}
	assetID := strings.TrimSpace(in.ClipAssetID)
	if assetID == "" {
		assetID = strings.TrimSpace(in.ClipID)
	}
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

	// 1. Persist the source transcript + translated subtitle text tracks so
	//    the localization plan builder can resolve them by (asset_id, language).
	if err := a.persistTracks(ctx, assetID, sourceLang, targetLang, in); err != nil {
		return err
	}
	// 2. Persist the full-span timed cues (the subtitle wire rejects a track
	//    with no cues).
	if err := a.persistCues(ctx, assetID, sourceLang, targetLang, in); err != nil {
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
	_, err := a.svc.Localize(ctx, LocalizeInput{
		AssetID:           assetID,
		JobID:             in.RunID,
		SceneID:           in.SceneID,
		ClipID:            clipID,
		SourceLanguage:    sourceLang,
		Request:           req,
		FolderID:          a.cfg.FolderID,
		DocTitle:          fmt.Sprintf("Localized — %s (%s)", clipID, targetLang),
		DocFolderID:       a.cfg.DocFolderID,
		DocIdempotencyKey: in.RunID + ":" + in.SceneID + ":" + targetLang,
		Watermark:         watermark,
		WatermarkSpec:     watermarkSpec,
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
	return nil
}

// persistTracks upserts the source transcript + translated subtitle text
// tracks as READY. Idempotent: the UNIQUE(asset_id, language_code, text_kind)
// upsert reuses an existing row, so a crash/restart never duplicates tracks.
func (a *localizedRenderEnqueuerAdapter) persistTracks(ctx context.Context, assetID, sourceLang, targetLang string, in scriptgeneration.LocalizedRenderInput) error {
	if a.tracks == nil {
		return fmt.Errorf("localized render: text track repository not wired")
	}
	var tracks []asset.TextTrack
	// The source-language render still needs a READY transcript. When source
	// and target language are equal, `Text` is the canonical narration and
	// `SourceText` is its provenance; use either value, but never skip the
	// source track entirely.
	sourceText := strings.TrimSpace(in.SourceText)
	if sourceText == "" && sourceLang == targetLang {
		sourceText = strings.TrimSpace(in.Text)
	}
	if sourceText != "" {
		tracks = append(tracks, asset.TextTrack{
			AssetID:            assetID,
			LanguageCode:       sourceLang,
			TextKind:           asset.TextTrackTranscript,
			TextContent:        sourceText,
			SourceType:         asset.TextSourceProvided,
			SourceLanguageCode: sourceLang,
			IsOriginal:         true,
			Provider:           "script-generation",
			TextHash:           asset.TextHash(sourceText, sourceLang, asset.TextTrackTranscript),
			Status:             asset.TextTrackReady,
		})
	}
	// The translated subtitle track is additional only when target language
	// differs from the source language; the source track above is reused for
	// same-language subtitles by the plan builder.
	if strings.TrimSpace(in.Text) != "" && targetLang != sourceLang {
		tracks = append(tracks, asset.TextTrack{
			AssetID:            assetID,
			LanguageCode:       targetLang,
			TextKind:           asset.TextTrackTranscript,
			TextContent:        in.Text,
			SourceType:         asset.TextSourceTranslation,
			SourceLanguageCode: sourceLang,
			IsOriginal:         false,
			Provider:           "script-generation",
			TextHash:           asset.TextHash(in.Text, targetLang, asset.TextTrackTranscript),
			Status:             asset.TextTrackReady,
		})
	}
	if len(tracks) == 0 {
		return fmt.Errorf("localized render: no text to persist for scene %q lang %q", in.SceneID, targetLang)
	}
	if err := a.tracks.UpsertBatch(ctx, tracks); err != nil {
		return fmt.Errorf("localized render: persist text tracks for %q: %w", assetID, err)
	}
	return nil
}

// persistCues writes a full-span timed cue (0 → clip duration) for each
// persisted language. ReplaceTranscriptCues replaces the whole per-asset cue
// set, so the adapter accumulates every language's cue under a lock and
// re-writes the complete set — two concurrent languages of the same scene can
// never clobber each other's cues.
func (a *localizedRenderEnqueuerAdapter) persistCues(ctx context.Context, assetID, sourceLang, targetLang string, in scriptgeneration.LocalizedRenderInput) error {
	if a.cues == nil {
		return fmt.Errorf("localized render: timed cue writer not wired")
	}
	duration := in.ClipDurationMS
	if duration <= 0 {
		duration = 1 // a valid single-frame cue; the renderer re-audits real duration
	}

	a.cueMu.Lock()
	defer a.cueMu.Unlock()
	byLang := a.cueState[assetID]
	if byLang == nil {
		byLang = make(map[string][]asset.TimedCue)
		a.cueState[assetID] = byLang
	}
	sourceCueText := strings.TrimSpace(in.SourceText)
	if sourceCueText == "" && sourceLang == targetLang {
		sourceCueText = strings.TrimSpace(in.Text)
	}
	if sourceCueText != "" {
		byLang[sourceLang] = []asset.TimedCue{{StartMs: 0, EndMs: duration, Text: sourceCueText}}
	}
	if strings.TrimSpace(in.Text) != "" && targetLang != sourceLang {
		byLang[targetLang] = []asset.TimedCue{{StartMs: 0, EndMs: duration, Text: in.Text}}
	}
	if err := a.cues.ReplaceTranscriptCues(ctx, assetID, byLang); err != nil {
		return fmt.Errorf("localized render: persist cues for %q: %w", assetID, err)
	}
	return nil
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
	resolver := &clipRenderAssetResolver{assets: root.Repos.Assets}
	materializer := &clipRenderMaterializer{drive: root.Drive.Reader, scratchDir: filepath.Join(cfg.Storage.TempPath(), "localization")}
	adapter := newLocalizedRenderEnqueuerAdapter(svc, root.Repos.TextTrackRepo, root.Domains.CueWriter, LocalizedRenderEnqueuerConfig{
		SourceLanguage: LocalizationConfigFromConfig(cfg).SourceLanguage,
		FolderID:       cfg.Drive.ClipsFolder(),
		DocFolderID:    cfg.Scripts.ScriptDocsFolderID,
	}, log, resolver, materializer)
	runner.SetLocalizedRenderEnqueuer(adapter)
	log.Info("wireScriptFlow: localized render fan-out wired to LocalizationService (Rust render_clip)",
		zap.String("source_language", LocalizationConfigFromConfig(cfg).SourceLanguage),
		zap.String("clips_folder", cfg.Drive.ClipsFolder()))
}
