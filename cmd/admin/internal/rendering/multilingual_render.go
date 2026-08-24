package rendering

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/multilingual"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"go.uber.org/zap"
)

// subtitleStyleVersion is the canonical subtitle style + generator version
// baked into every ASS artifact by SubtitleArtifactMaterializer. It is a
// fingerprint input: changing the ASS style bumps every variant fingerprint.
// v2 = larger font (Fontsize 56, Outline 3, MarginV 24) vs the v1 24px style.
const subtitleStyleVersion = "vidrush-default:vidrush-ass-v2"

// runMultilingualRender is the idempotent end-to-end multilingual clip
// pipeline. For each source clip it:
//
//  1. Reuses/generates the canonical transcript (via BackfillService).
//  2. Translates each source cue per-cue (CueTranslator) so every target
//     language carries 1:1 timing with the source transcript.
//  3. Generates + uploads the .ass per language.
//  4. Renders + validates + uploads each language in parallel (Renderer),
//     reusing any fingerprinted variant already completed.
//
// Every run also confluences its job + per-clip metrics into the canonical
// performance registry (performance_runs / performance_steps /
// performance_operations) — not a second metrics system.
//
// Usage:
//
//	pipelinegen-admin multilingual-render \
//	    --asset-ids=manual_9b8daf320154 \
//	    --languages=en,it,pl,ru,de,es,pt-BR,fr,tr,id \
//	    --drive-folder-id=1I5XdK72kyYTUYdZYCV0ki6FnAwJxRfKI \
//	    --concurrency=4 --json
func processOneClip(
	ctx context.Context,
	svc *texttracks.BackfillService,
	renderer *multilingual.Renderer,
	subMat *texttracks.SubtitleArtifactMaterializer,
	cueRepair *texttracks.CueRepairService,
	cueTranslator *texttracks.CueTranslator,
	rec *multilingual.Recorder,
	clips *sqassets.ClipsRepository,
	trackRepo asset.TextTrackRepository,
	cfg *config.Config,
	id, srcLang string,
	targetLangs []string,
	driveFolder string,
	docsFolder string,
	concurrency int,
	force bool,
	docClient drive.DocClient,
	overlays overlayAssets,
	log *zap.Logger,
) ([]langReport, multilingual.RunMetrics, *multilingual.LocalizationDocRef, error) {
	totalStart := time.Now()

	item, err := clips.Get(ctx, id)
	if err != nil {
		return nil, multilingual.RunMetrics{}, nil, fmt.Errorf("multilingual-render: get asset %s: %w", id, err)
	}
	if item == nil {
		return nil, multilingual.RunMetrics{}, nil, fmt.Errorf("multilingual-render: asset %s not found", id)
	}

	// Resolve destination folder + source identity up front (needed for the
	// per-operation measurements that follow).
	folder := resolveDriveFolder(cfg, item, driveFolder)
	sourcePath := item.LocalPath()
	if sourcePath == "" {
		sourcePath = item.Filename
	}
	sourceSHA := item.ContentHash()
	if sourceSHA == "" {
		sourceSHA = item.LegacyFileMD5()
	}
	if !isSHA256Hex(sourceSHA) && sourcePath != "" {
		if calculated, hashErr := hashLocalFile(sourcePath); hashErr == nil {
			sourceSHA = calculated
		}
	}
	// Expected fps = the source clip's frame rate (the burn profile never
	// changes it). Probed once per clip, best-effort: 0 disables the exact
	// match and leaves the renderer's sane-range check.
	sourceFPS, sourceProbed := probeSourceFPS(cfg.External.FfmpegPath, sourcePath)
	base := strings.TrimSuffix(filepath.Base(item.Filename), filepath.Ext(item.Filename))
	if base == "" {
		base = id
	}
	sourceDurationMS := item.Duration.Milliseconds()
	sourceSizeBytes := int64(0)
	if sourcePath != "" {
		if st, statErr := os.Stat(sourcePath); statErr == nil {
			sourceSizeBytes = st.Size()
		}
	}

	// 1-2. Reuse/generate transcript + full-text translation (BackfillService).
	bf, err := svc.ProcessAsset(ctx, item, texttracks.BackfillOptions{
		Source:                      string(item.Source),
		SourceLanguage:              srcLang,
		TargetLanguages:             targetLangs,
		TextKind:                    asset.TextTrackTranscript,
		SkipSubtitleMaterialization: true, // the renderer generates ASS from its own per-cue cues
	})
	if err != nil {
		return nil, multilingual.RunMetrics{}, nil, fmt.Errorf("multilingual-render: %s: backfill: %w", id, err)
	}

	// Zero-duplicate execution counters. Shared source acquisition runs ONCE for
	// the whole run, never per-language: the source clip is imported/materialized
	// once (Download) and probed once for fps (Probe); the transcript is reused
	// (Transcribe=0) or generated once (Transcribe=1). Fan-out work
	// (translate/ass/render/validate/upload) runs once per language. The
	// certified invariant is never download=2 / probe=2 / transcription=2.
	counters := multilingual.ExecCounters{}
	counters.Download = 1 // source imported/materialized once (single shared artifact)
	if sourceProbed {
		counters.Probe = 1
	}
	if bf.SourceAcquired {
		counters.Transcribe = 1
	}
	counters.TranslateFullText = int64(len(bf.CreatedLangs) + len(bf.Retranslated))

	// 3. Read the timed source cues (the single source of truth for timing).
	srcTrack, srcCues, err := trackRepo.FindReady(ctx, id, srcLang, asset.TextTrackTranscript)
	if err != nil {
		return nil, multilingual.RunMetrics{}, nil, fmt.Errorf("multilingual-render: %s: read source track: %w", id, err)
	}
	if srcTrack == nil || len(srcCues) == 0 {
		return nil, multilingual.RunMetrics{}, nil, fmt.Errorf("multilingual-render: %s: no timed source cues for language %q", id, srcLang)
	}

	// Streaming fan-out. EN (source, priority 0) is ready immediately and its
	// render is submitted BEFORE any target translation, so EN render_started_at
	// < ES completion — there is NO "translate-all + ass-all" barrier before
	// rendering. Each language submits its render as soon as its ASS is ready.
	order := append([]string{srcLang}, targetLangs...)
	pool := renderer.NewRenderPool(ctx, concurrency)

	transcriptStatus := "reused"
	if bf.SourceAcquired {
		transcriptStatus = "generated"
	}

	byLang := map[string][]asset.TimedCue{srcLang: srcCues}
	translationStatus := map[string]string{srcLang: "source"}
	translateMS := map[string]int64{srcLang: 0}
	var sumTranslateMS int64
	var translateAgg observability.ConcurrencyStats

	reports := make([]langReport, 0, len(order))
	inputs := make([]multilingual.VariantInput, 0, len(order))
	var sumASSMS, sumRenderMS, sumOutputBytes int64

	// buildAndSubmit generates the ASS for one language from in-memory cues and
	// submits its render to the pool. Returns the report row (render fields are
	// filled after Wait from the per-language render result).
	buildAndSubmit := func(lang string, cues []asset.TimedCue, priority int, textReadyAt time.Time) langReport {
		rep := langReport{
			Language:    lang,
			Transcript:  transcriptStatus,
			Translation: orDefault(translationStatus[lang], "failed"),
			TranslateMS: translateMS[lang],
			Priority:    priority,
			TextReadyAt: formatTS(textReadyAt),
		}
		track, tErr := trackRepo.Find(ctx, id, lang, asset.TextTrackTranscript)
		if tErr != nil || track == nil {
			rep.ASSStatus = "failed"
			rep.RenderStatus = "failed"
			rep.Validation = "no text track for language"
			return rep
		}
		assStart := time.Now()
		assOut, aErr := subMat.Materialize(ctx, texttracks.SubtitleMaterializerInput{
			AssetID:         id,
			DriveFilename:   item.Filename,
			LanguageCode:    lang,
			TextTrackID:     track.ID,
			ClipDurationMs:  item.Duration.Milliseconds(),
			TimedCues:       cues,
			SubtitleStyleID: "vidrush-default",
			ClipContentHash: sourceSHA,
			DriveFolderID:   folder,
		})
		rep.ASSMS = time.Since(assStart).Milliseconds()
		sumASSMS += rep.ASSMS
		counters.ASS++
		rec.RecordOperation(ctx, observability.MeasuredOperation{
			Operation:        "multilingual.ass",
			SourceSHA256:     sourceSHA,
			SourceDurationMS: sourceDurationMS,
			SourceSizeBytes:  sourceSizeBytes,
			ElapsedMS:        rep.ASSMS,
			CacheHit:         false,
			MetadataJSON:     opMetadata(id, lang, nil),
		})
		if aErr != nil {
			rep.ASSStatus = "failed"
			rep.RenderStatus = "failed"
			rep.Validation = fmt.Sprintf("ASS: %v", aErr)
			return rep
		}
		rep.ASSStatus = "ready"

		translationVersion := track.ModelVersion
		if translationVersion == "" {
			translationVersion = track.SourceVersion
		}
		in := multilingual.VariantInput{
			SourceClipID:           id,
			SourcePath:             sourcePath,
			SourceSHA256:           sourceSHA,
			SourceDuration:         item.Duration,
			SourceFPS:              sourceFPS,
			Language:               lang,
			Priority:               priority,
			TextReadyAt:            textReadyAt,
			TranscriptSHA256:       track.TextHash,
			TranslationVersion:     translationVersion,
			SubtitleStyleVersion:   subtitleStyleVersion,
			ASSPath:                assOut.LocalPath,
			ASSHash:                assOut.LegacyFileMD5,
			OutputFilename:         base + "." + lang + ".mp4",
			DriveFolderID:          folder,
			WorkDir:                filepath.Join("data", "media", "renders"),
			Force:                  force,
			BackgroundAssetID:      overlays.BackgroundAssetID,
			BackgroundPath:         overlays.BackgroundPath,
			BackgroundSHA256:       overlays.BackgroundSHA256,
			WatermarkAssetID:       overlays.WatermarkAssetID,
			WatermarkPath:          overlays.WatermarkPath,
			WatermarkSHA256:        overlays.WatermarkSHA256,
			WatermarkPosition:      overlays.Position,
			WatermarkOpacity:       overlays.Opacity,
			WatermarkMarginPX:      overlays.MarginPX,
			ForegroundScalePercent: overlays.ScalePercent,
			RenderProfileVersion:   overlays.ProfileVersion(),
		}
		inputs = append(inputs, in)
		pool.Submit(in)
		return rep
	}

	// submitReusable is the fast path: when the translated TextTrack and the
	// variant fingerprint already match, do not call the translator or build a
	// new ASS file. RenderOne still performs its own authoritative cache check.
	submitReusable := func(lang string, track *asset.TextTrack, priority int, textReadyAt time.Time) langReport {
		translationVersion := track.ModelVersion
		if translationVersion == "" {
			translationVersion = track.SourceVersion
		}
		rep := langReport{
			Language: lang, Transcript: transcriptStatus, Translation: "translated",
			Priority: priority, TextReadyAt: formatTS(textReadyAt), ASSStatus: "ready",
		}
		in := multilingual.VariantInput{
			SourceClipID: id, SourcePath: sourcePath, SourceSHA256: sourceSHA,
			SourceDuration: item.Duration, SourceFPS: sourceFPS, Language: lang,
			Priority: priority, TextReadyAt: textReadyAt,
			TranscriptSHA256: track.TextHash, TranslationVersion: translationVersion,
			SubtitleStyleVersion: subtitleStyleVersion,
			OutputFilename:       base + "." + lang + ".mp4", DriveFolderID: folder,
			WorkDir: filepath.Join("data", "media", "renders"), Force: false,
			BackgroundAssetID: overlays.BackgroundAssetID, BackgroundPath: overlays.BackgroundPath, BackgroundSHA256: overlays.BackgroundSHA256,
			WatermarkAssetID: overlays.WatermarkAssetID, WatermarkPath: overlays.WatermarkPath, WatermarkSHA256: overlays.WatermarkSHA256,
			WatermarkPosition: overlays.Position, WatermarkOpacity: overlays.Opacity, WatermarkMarginPX: overlays.MarginPX,
			ForegroundScalePercent: overlays.ScalePercent,
			RenderProfileVersion:   overlays.ProfileVersion(),
		}
		inputs = append(inputs, in)
		pool.Submit(in)
		return rep
	}

	// EN (source, priority 0): text ready immediately, render submitted BEFORE
	// any target translation starts.
	reports = append(reports, buildAndSubmit(srcLang, srcCues, 0, time.Now()))

	// Targets (priority 1..N): translate per-cue → ASS → submit render,
	// overlapping EN's render already in flight.
	for i, lang := range targetLangs {
		// Probe the existing translated track before invoking the provider. If
		// its variant is already READY, the whole language is reused: no
		// translation, no ASS generation, no Rust process, no Drive upload.
		if !force {
			if readyTrack, readyCues, readyErr := trackRepo.FindReady(ctx, id, lang, asset.TextTrackTranscript); readyErr == nil && readyTrack != nil && len(readyCues) > 0 {
				translationVersion := readyTrack.ModelVersion
				if translationVersion == "" {
					translationVersion = readyTrack.SourceVersion
				}
				if renderer.IsReusable(ctx, id, sourceSHA, lang, readyTrack.TextHash, translationVersion, subtitleStyleVersion, overlays.ProfileVersion()) {
					byLang[lang] = readyCues
					translationStatus[lang] = "translated"
					reports = append(reports, submitReusable(lang, readyTrack, i+1, time.Now()))
					continue
				}
			}
		}
		langStart := time.Now()
		translatedCues, tStats, tErr := cueTranslator.Translate(ctx, srcCues, lang)
		elapsed := time.Since(langStart).Milliseconds()
		translateMS[lang] = elapsed
		sumTranslateMS += elapsed
		// Aggregate per-language fan-out stats (translation is sequential
		// across languages, so the peak is the max over the per-language
		// per-cue fan-outs).
		translateAgg.Configured = tStats.Configured
		if tStats.MaxObserved > translateAgg.MaxObserved {
			translateAgg.MaxObserved = tStats.MaxObserved
		}
		translateAgg.WallMS += tStats.WallMS
		translateAgg.TotalWorkMS += tStats.TotalWorkMS
		translateAgg.TotalQueueMS += tStats.TotalQueueMS
		if tStats.MaxQueueMS > translateAgg.MaxQueueMS {
			translateAgg.MaxQueueMS = tStats.MaxQueueMS
		}
		counters.Translate++
		rec.RecordOperation(ctx, observability.MeasuredOperation{
			Operation:        "multilingual.translate",
			SourceSHA256:     sourceSHA,
			SourceDurationMS: sourceDurationMS,
			SourceSizeBytes:  sourceSizeBytes,
			ElapsedMS:        elapsed,
			CacheHit:         false, // per-cue translation has no cache today
			MetadataJSON:     opMetadata(id, lang, map[string]any{"concurrency": tStats}),
		})
		if tErr != nil {
			log.Warn("multilingual-render: per-cue translation failed",
				zap.String("asset_id", id), zap.String("lang", lang), zap.Error(tErr))
			translationStatus[lang] = "failed"
			reports = append(reports, langReport{
				Language:     lang,
				Transcript:   transcriptStatus,
				Translation:  "failed",
				TranslateMS:  elapsed,
				ASSStatus:    "failed",
				RenderStatus: "failed",
				Validation:   "translation failed",
				Priority:     i + 1,
				TextReadyAt:  formatTS(time.Now()),
			})
			continue
		}
		byLang[lang] = translatedCues
		translationStatus[lang] = "translated"
		reports = append(reports, buildAndSubmit(lang, translatedCues, i+1, time.Now()))
	}

	if translateAgg.WallMS > 0 {
		translateAgg.AvgObserved = float64(translateAgg.TotalWorkMS) / float64(translateAgg.WallMS)
	}

	// Persist cues atomically (single ReplaceTranscriptCues over ALL languages).
	if err := cueRepair.Repair(ctx, id, byLang); err != nil {
		return nil, multilingual.RunMetrics{}, nil, fmt.Errorf("multilingual-render: %s: align cues: %w", id, err)
	}

	// Wait for all submitted renders (EN was submitted before ES translation).
	renderReport := pool.Wait()
	totalRenderMS := renderReport.Concurrency.WallMS

	var successCount, failedCount int
	var renderHits, renderMisses int64
	for _, r := range renderReport.Variants {
		sumRenderMS += r.RenderMS
		sumOutputBytes += r.SizeBytes
		if r.Status == "reused" {
			renderHits++
		} else if r.Status == "ready" {
			renderMisses++
		}
		switch r.Status {
		case "ready":
			counters.Render++
			counters.Validate++
			counters.Upload++
		case "failed":
			counters.Render++ // ffmpeg burn was attempted
		}
		if r.Status == "ready" || r.Status == "reused" {
			successCount++
		} else {
			failedCount++
		}
		rec.RecordOperation(ctx, observability.MeasuredOperation{
			Operation:        "multilingual.render",
			SourceSHA256:     sourceSHA,
			SourceDurationMS: sourceDurationMS,
			SourceSizeBytes:  sourceSizeBytes,
			ElapsedMS:        r.RenderMS,
			OutputSizeBytes:  r.SizeBytes,
			CacheHit:         r.Status == "reused",
			MetadataJSON:     opMetadata(id, r.Language, map[string]any{"queue_ms": r.QueueMS, "worker_id": r.WorkerID}),
		})
	}

	for i := range reports {
		if reports[i].ASSStatus != "ready" {
			continue
		}
		for _, r := range renderReport.Variants {
			if r.Language == reports[i].Language {
				reports[i].RenderStatus = r.Status
				reports[i].RenderMS = r.RenderMS
				reports[i].RTF = multilingual.RTF(r.RenderMS, sourceDurationMS)
				reports[i].SizeBytes = r.SizeBytes
				reports[i].DurationMs = r.DurationMs
				reports[i].Fingerprint = r.Fingerprint
				reports[i].OutputHash = r.OutputHash
				reports[i].DriveLink = r.DriveLink
				reports[i].QueuedAt = formatTS(r.QueuedAt)
				reports[i].RenderStartedAt = formatTS(r.RenderStartedAt)
				reports[i].RenderCompletedAt = formatTS(r.RenderCompletedAt)
				reports[i].UploadCompletedAt = formatTS(r.UploadCompletedAt)
				reports[i].WorkerID = r.WorkerID
				if r.Validation != "" {
					reports[i].Validation = r.Validation
				} else {
					reports[i].Validation = "ok"
				}
				break
			}
		}
	}

	backfillHits := int64(len(bf.SkippedLangs))
	backfillMisses := int64(len(bf.CreatedLangs) + len(bf.Retranslated))
	if bf.SourceAcquired {
		backfillMisses++
	} else {
		backfillHits++
	}
	completedAt := time.Now()
	wallMS := completedAt.Sub(totalStart).Milliseconds()
	summary := multilingual.RunMetrics{
		JobID:                "asset:" + id,
		WorkloadID:           "multilingual-render",
		StartedAt:            totalStart,
		CompletedAt:          completedAt,
		WallMS:               wallMS,
		ClipCount:            len(order),
		SuccessCount:         successCount,
		FailedCount:          failedCount,
		TotalInputBytes:      sourceSizeBytes,
		TotalOutputBytes:     sumOutputBytes,
		CacheHits:            backfillHits + renderHits,
		CacheMisses:          backfillMisses + int64(len(targetLangs)) + renderMisses,
		WorkerLimit:          concurrency,
		SumOperationMS:       bf.DurationMs + sumTranslateMS + sumASSMS + sumRenderMS,
		RenderConcurrency:    renderReport.Concurrency,
		TranslateConcurrency: translateAgg,
		Throughput:           multilingual.ComputeThroughput(len(order), sourceDurationMS, wallMS, sumRenderMS),
		Operations:           counters,
		Steps: []multilingual.StepMetrics{
			{Name: "backfill", DurationMS: bf.DurationMs, CacheHits: backfillHits, CacheMisses: backfillMisses},
			{Name: "translate_cues", DurationMS: sumTranslateMS, OutputCount: int64(len(targetLangs)), CacheMisses: int64(len(targetLangs))},
			{Name: "ass", DurationMS: sumASSMS, OutputCount: int64(len(order)), CacheMisses: int64(len(order))},
			{Name: "render", DurationMS: totalRenderMS, OutputCount: int64(len(inputs)), OutputBytes: sumOutputBytes, CacheHits: renderHits, CacheMisses: renderMisses},
		},
	}

	log.Info("multilingual-render.clip_done",
		zap.String("asset_id", id),
		zap.Int("languages", len(order)),
		zap.Int64("total_render_ms", totalRenderMS),
		zap.Int64("sum_operation_ms", summary.SumOperationMS),
		zap.Int64("wall_ms", summary.WallMS),
	)

	// Assemble + publish the localization manifest Google Doc. Entries are in
	// REQUESTED order (source=0, targets=1..N) — never render completion order.
	// Fail-soft: no DocClient → no doc, but the ordered entries are still logged.
	docRef := publishLocalizationDoc(ctx, docClient, id, base, docsFolder, folder, renderReport.Variants, force, log)

	return reports, summary, docRef, nil
}
