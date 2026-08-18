package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/multilingual"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqtexttracks "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
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
func runMultilingualRender(args []string) error {
	fs := flag.NewFlagSet("multilingual-render", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated source clip asset IDs (required)")
	sourceLang := fs.String("source-lang", "", "BCP-47 source language (default: config multilingual.source_language)")
	langs := fs.String("languages", "", "BCP-47 csv, [0]=source [1:]=targets (default: config registry)")
	driveFolder := fs.String("drive-folder-id", "", "destination Drive folder (default: asset folder_id or config clips folder)")
	concurrency := fs.Int("concurrency", 4, "render fan-out concurrency")
	translateConcurrency := fs.Int("translate-concurrency", 4, "per-cue translation concurrency")
	force := fs.Bool("force", false, "bypass render variant reuse: always re-render + re-validate + re-upload")
	docsFolder := fs.String("docs-folder-id", "", "destination Drive folder for the localization manifest Google Doc (default: clip destination folder)")
	certifyJSON := fs.String("certify-json", "", "write the canonical certification report JSON to this path (\"-\" for stdout)")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	assetIDs := splitCSV(*ids)
	if len(assetIDs) == 0 {
		return fmt.Errorf("multilingual-render: --asset-ids is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return err
	}
	defer rootCleanup()

	svc, err := texttracks.NewBackfillService(texttracks.BackfillServiceDeps{
		Data: texttracks.BackfillDataDeps{
			Clips:      root.Repos.ClipsRepo,
			Repo:       root.Repos.TextTrackRepo,
			Cues:       root.Domains.CueWriter,
			SubArtRepo: root.Repos.SubtitleArtifactRepo,
		},
		Pipeline: texttracks.BackfillPipelineDeps{
			Materializer: root.TextTracks.Materializer,
			Acquirer:     root.TextTracks.AcquireService,
		},
		Delivery: texttracks.BackfillDeliveryDeps{
			Publisher:     root.Drive.Publisher,
			DriveFolderID: cfg.Drive.ClipsFolder(),
		},
		Log: log,
	})
	if err != nil {
		return fmt.Errorf("multilingual-render: new backfill service: %w", err)
	}

	variantRepo, err := sqtexttracks.NewRenderVariantRepository(root.DB.DB, log)
	if err != nil {
		return fmt.Errorf("multilingual-render: new variant repo: %w", err)
	}
	renderer, err := multilingual.NewRenderer(variantRepo, root.Drive.Publisher, cfg.External.FfmpegPath, log)
	if err != nil {
		return fmt.Errorf("multilingual-render: new renderer: %w", err)
	}
	mediaProfile := root.MediaExec.Profile.WithDefaults()
	rustExecutor := rustexec.NewExecutor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	rustClipRenderer := rustexec.NewClipRendererWithExecutor(rustExecutor, root.MediaExec.Policy, mediaProfile, log)
	renderer.WithRustRenderer(adminRustRenderer{renderer: rustClipRenderer}, mediaProfile.Width, mediaProfile.Height, mediaProfile.FPS)
	subMat := texttracks.NewSubtitleArtifactMaterializer(root.Repos.SubtitleArtifactRepo, "data/media/subtitles", root.Drive.Publisher)
	cueRepair, err := texttracks.NewCueRepairService(root.Domains.CueWriter)
	if err != nil {
		return fmt.Errorf("multilingual-render: new cue repair: %w", err)
	}

	srcLang, targetLangs := resolveLanguages(cfg, *sourceLang, *langs)

	// PR-ARGOS-TRANSLATION (Aug 2026): the CueTranslator routes through the
	// SAME provider chain as the materializer (Argos primary + Ollama
	// fallback, or Ollama-only per translation_provider) instead of reaching
	// Ollama directly, so the per-cue translation and the full-text
	// translation stay on one canonical provider stack.
	if root.TextTracks == nil || root.TextTracks.Translator == nil {
		return fmt.Errorf("multilingual-render: translation provider is not configured")
	}
	cueTranslator := texttracks.NewCueTranslator(
		root.TextTracks.Translator,
		srcLang,
		cfg.External.OllamaModel,
		*translateConcurrency,
		log,
	)

	// Metrics: confluent into the canonical performance registry.
	perfReg, err := perfstore.New(root.DB.DB)
	if err != nil {
		return fmt.Errorf("multilingual-render: performance registry: %w", err)
	}
	opStore, err := perfstore.NewOperationStore(root.DB.DB)
	if err != nil {
		return fmt.Errorf("multilingual-render: performance operation store: %w", err)
	}
	rec := multilingual.NewRecorder(perfReg, opStore, log)

	// Process-level CPU/RSS sampled once around the whole run (the admin
	// process is dedicated to this single job).
	cpuStartUser, cpuStartSystem, _ := multilingual.ProcessResources()

	var all []langReport
	var summaries []multilingual.RunMetrics
	var validatedCounts []int
	var docRefs []*multilingual.LocalizationDocRef
	for _, id := range assetIDs {
		rep, summary, docRef, rErr := processOneClip(cmdContext(), svc, renderer, subMat, cueRepair, cueTranslator, rec,
			root.Repos.ClipsRepo, root.Repos.TextTrackRepo, cfg, id, srcLang, targetLangs, *driveFolder, *docsFolder, *concurrency, *force, root.Drive.DocClient, log)
		if rErr != nil {
			return rErr
		}
		cpuEndUser, cpuEndSystem, peakRSS := multilingual.ProcessResources()
		summary.CPUUserMS = cpuEndUser - cpuStartUser
		summary.CPUSystemMS = cpuEndSystem - cpuStartSystem
		summary.PeakRSSBytes = peakRSS
		rec.RecordRun(cmdContext(), summary)
		summaries = append(summaries, summary)
		validatedCounts = append(validatedCounts, countValidated(rep))
		all = append(all, rep...)
		if docRef != nil {
			docRefs = append(docRefs, docRef)
		}
	}

	if *certifyJSON != "" {
		return writeCertification(*certifyJSON, summaries, validatedCounts)
	}

	if *jsonOut {
		b, _ := json.MarshalIndent(struct {
			SourceLanguage  string                            `json:"source_language"`
			TargetLanguages []string                          `json:"target_languages"`
			Variants        []langReport                      `json:"variants"`
			LocalizationDoc []multilingual.LocalizationDocRef `json:"localization_docs,omitempty"`
			Parallelism     []multilingual.RunMetrics         `json:"parallelism"`
		}{srcLang, targetLangs, all, derefDocRefs(docRefs), summaries}, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	printLangReport(all)
	printPerLangTiming(all)
	printLocalizationDocs(docRefs)
	printParallelism(summaries)
	return nil
}

// adminRustRenderer keeps the application renderer independent of the
// concrete Rust adapter while making this operational command use the same
// sealed render_clip boundary as the main clip-render capability.
type adminRustRenderer struct {
	renderer *rustexec.ClipRenderer
}

func (a adminRustRenderer) RenderClip(ctx context.Context, plan cliprender.ClipRenderPlanV1) (multilingual.RustRenderResult, error) {
	result, err := a.renderer.RenderClip(ctx, plan)
	if err != nil {
		return multilingual.RustRenderResult{}, err
	}
	return multilingual.RustRenderResult{OutputPath: result.OutputPath}, nil
}

// langReport is one row of the per-language report.
type langReport struct {
	Language     string  `json:"language"`
	Transcript   string  `json:"transcript"`    // reused | generated | missing
	Translation  string  `json:"translation"`   // source | translated | failed (per-cue)
	TranslateMS  int64   `json:"translate_ms"`  // per-cue translation wall time
	ASSStatus    string  `json:"ass_status"`    // ready | failed
	ASSMS        int64   `json:"ass_ms"`        // ASS generation + upload wall time
	RenderStatus string  `json:"render_status"` // ready | reused | failed
	RenderMS     int64   `json:"render_ms"`
	RTF          float64 `json:"render_rtf"` // render_ms / source_duration_ms
	SizeBytes    int64   `json:"size_bytes"`
	DurationMs   int64   `json:"duration_ms"`
	Validation   string  `json:"validation"` // ok | <error>
	Fingerprint  string  `json:"fingerprint"`
	OutputHash   string  `json:"output_hash"`
	DriveLink    string  `json:"drive_link,omitempty"`
	// Per-language lifecycle timing (RFC3339, empty = not recorded).
	Priority          int    `json:"priority"`
	TextReadyAt       string `json:"text_ready_at,omitempty"`
	QueuedAt          string `json:"queued_at,omitempty"`
	RenderStartedAt   string `json:"render_started_at,omitempty"`
	RenderCompletedAt string `json:"render_completed_at,omitempty"`
	UploadCompletedAt string `json:"upload_completed_at,omitempty"`
	WorkerID          int    `json:"worker_id"`
}

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
		sourceSHA = item.FileHash()
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
			SourceClipID:         id,
			SourcePath:           sourcePath,
			SourceSHA256:         sourceSHA,
			SourceDuration:       item.Duration,
			SourceFPS:            sourceFPS,
			Language:             lang,
			Priority:             priority,
			TextReadyAt:          textReadyAt,
			TranscriptSHA256:     track.TextHash,
			TranslationVersion:   translationVersion,
			SubtitleStyleVersion: subtitleStyleVersion,
			ASSPath:              assOut.LocalPath,
			ASSHash:              assOut.FileHash,
			OutputFilename:       base + "." + lang + ".mp4",
			DriveFolderID:        folder,
			WorkDir:              filepath.Join("data", "media", "renders"),
			Force:                force,
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

// opMetadata is a compact per-operation correlation payload (asset + language
// + any operation-specific fields like queue_ms / worker_id / concurrency).
func opMetadata(assetID, lang string, extra map[string]any) string {
	m := map[string]any{"asset_id": assetID, "language": lang}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// resolveLanguages returns the canonical (source, targets) pair. When
// --languages is provided, its first entry is the source and the rest are
// targets. Otherwise the source comes from --source-lang (or config) and the
// targets from the config registry (enabled + translate_clips).
func resolveLanguages(cfg *config.Config, sourceLangFlag, langsFlag string) (string, []string) {
	srcDefault := "en"
	if cfg.Media.Multilingual.SourceLanguage != "" {
		srcDefault = cfg.Media.Multilingual.SourceLanguage
	}
	if langsFlag != "" {
		parts := splitCSV(langsFlag)
		if len(parts) == 0 {
			return srcDefault, nil
		}
		src := parts[0]
		if src == "" {
			src = srcDefault
		}
		return src, parts[1:]
	}
	src := sourceLangFlag
	if src == "" {
		src = srcDefault
	}
	out := make([]string, 0)
	for _, spec := range cfg.Media.Multilingual.Languages {
		if !spec.Enabled || !spec.TranslateClips {
			continue
		}
		if spec.Code == src {
			continue
		}
		out = append(out, spec.Code)
	}
	sort.Strings(out)
	return src, out
}

func resolveDriveFolder(cfg *config.Config, item *asset.Asset, override string) string {
	if override != "" {
		return override
	}
	if item != nil && item.FolderID() != "" {
		return item.FolderID()
	}
	return cfg.Drive.ClipsFolder()
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// countValidated returns the number of language variants whose output-contract
// validation passed (Validation == "ok"). This is the "validated" count in the
// certification report — distinct from SuccessCount (rendered/reused).
func countValidated(reports []langReport) int {
	n := 0
	for _, r := range reports {
		if r.Validation == "ok" {
			n++
		}
	}
	return n
}

// writeCertification emits the canonical certification report JSON. One asset
// → a single object; multiple assets → a JSON array. AvoidedWorkMS is 0 here:
// a single run cannot measure what a warm re-run would have skipped (that is
// the cold-vs-warm comparison in renderer_cache_test.go).
func writeCertification(path string, summaries []multilingual.RunMetrics, validatedCounts []int) error {
	certs := make([]multilingual.CertificationReport, 0, len(summaries))
	for i, s := range summaries {
		validated := 0
		if i < len(validatedCounts) {
			validated = validatedCounts[i]
		}
		certs = append(certs, multilingual.BuildCertification(s, validated, 0))
	}
	var b []byte
	var err error
	if len(certs) == 1 {
		b, err = json.MarshalIndent(certs[0], "", "  ")
	} else {
		b, err = json.MarshalIndent(certs, "", "  ")
	}
	if err != nil {
		return err
	}
	if path == "-" {
		fmt.Println(string(b))
		return nil
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("certification report written to %s\n", path)
	return nil
}

// derefDocRefs flattens []*LocalizationDocRef into the value slice used by the
// JSON output (nil refs are dropped).
func derefDocRefs(refs []*multilingual.LocalizationDocRef) []multilingual.LocalizationDocRef {
	out := make([]multilingual.LocalizationDocRef, 0, len(refs))
	for _, r := range refs {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// printLocalizationDocs prints the localization manifest doc(s) and their
// ordered entries (already priority-sorted by AssembleLocalizationEntries).
func printLocalizationDocs(refs []*multilingual.LocalizationDocRef) {
	if len(refs) == 0 {
		return
	}
	fmt.Println("=== Localization manifest (Google Docs) ===")
	for _, r := range refs {
		if r == nil {
			continue
		}
		fmt.Printf("doc: %s (%d entries)\n", r.Link, len(r.Entries))
		for _, e := range r.Entries {
			fmt.Printf("  #%d %-6s %-9s %s\n", e.Priority, e.Language, e.Status, e.DriveLink)
		}
	}
}

// publishLocalizationDoc assembles the localization manifest Google Doc with
// entries in REQUESTED order (source=0, targets=1..N) — never render
// completion order — and publishes it idempotently. Fail-soft: no DocClient
// (or no destination folder) returns a nil-ID ref whose ordered entries are
// still populated, so the requested order survives even offline.
func publishLocalizationDoc(
	ctx context.Context,
	docClient drive.DocClient,
	id, base, docsFolder, fallbackFolder string,
	variants []multilingual.VariantResult,
	force bool,
	log *zap.Logger,
) *multilingual.LocalizationDocRef {
	entries := multilingual.AssembleLocalizationEntries(variants)
	ref := &multilingual.LocalizationDocRef{Entries: entries}
	folder := docsFolder
	if folder == "" {
		folder = fallbackFolder
	}
	if docClient == nil || folder == "" {
		log.Info("multilingual-render.localization_doc.skipped",
			zap.String("asset_id", id),
			zap.String("reason", "no doc client or no destination folder"),
			zap.Int("entries", len(entries)))
		return ref
	}

	title := "Localization — " + base
	if base == "" {
		title = "Localization — " + id
	}
	content := multilingual.RenderLocalizationDoc(title, entries)
	key := "localization:asset:" + id
	doc, err := docClient.CreateDocIdempotent(ctx, title, content, folder, key, force)
	if err != nil {
		log.Warn("multilingual-render.localization_doc.failed",
			zap.String("asset_id", id), zap.Error(err))
		return ref
	}
	if doc == nil {
		return ref
	}
	ref.ID = doc.ID
	ref.Link = doc.URL
	log.Info("multilingual-render.localization_doc.published",
		zap.String("asset_id", id),
		zap.String("doc_id", doc.ID),
		zap.String("link", doc.URL),
		zap.Int("entries", len(entries)))
	return ref
}

func printParallelism(summaries []multilingual.RunMetrics) {
	for _, s := range summaries {
		r := s.RenderConcurrency
		tr := s.TranslateConcurrency
		fmt.Println("=== Parallelism (observed) ===")
		fmt.Printf("render:    configured=%d max_observed=%d avg_observed=%.2f wall_ms=%d work_ms=%d queue_ms=%d (max %d)\n",
			r.Configured, r.MaxObserved, r.AvgObserved, r.WallMS, r.TotalWorkMS, r.TotalQueueMS, r.MaxQueueMS)
		fmt.Printf("translate: configured=%d max_observed=%d avg_observed=%.2f wall_ms=%d work_ms=%d queue_ms=%d (max %d)\n",
			tr.Configured, tr.MaxObserved, tr.AvgObserved, tr.WallMS, tr.TotalWorkMS, tr.TotalQueueMS, tr.MaxQueueMS)
		tp := s.Throughput
		fmt.Printf("throughput: clips/min=%.2f media_min/min=%.2f render_rtf=%.2f\n",
			tp.ClipsPerMinute, tp.MediaMinutesPerMinute, tp.RenderRTF)
		c := s.Operations
		fmt.Printf("exec:       download=%d probe=%d transcribe=%d translate=%d fulltext_translate=%d ass=%d render=%d validate=%d upload=%d\n",
			c.Download, c.Probe, c.Transcribe, c.Translate, c.TranslateFullText, c.ASS, c.Render, c.Validate, c.Upload)
	}
}

func printLangReport(reports []langReport) {
	fmt.Println("=== Multilingual Render Report ===")
	fmt.Printf("%-8s %-10s %-11s %-11s %-5s %-7s %-7s %-8s %-6s %-9s %-6s %s\n",
		"lang", "transcript", "translation", "translate_ms", "ass", "ass_ms", "render", "render_ms", "rtf", "size_mb", "valid", "drive_link")
	var totalTranslate, totalASS, totalRender int64
	var rendered, validated int
	for _, r := range reports {
		sizeMB := ""
		if r.SizeBytes > 0 {
			sizeMB = fmt.Sprintf("%.2f", float64(r.SizeBytes)/1024/1024)
		}
		fmt.Printf("%-8s %-10s %-11s %-11d %-5s %-7d %-7s %-8d %-6.2f %-9s %-6s %s\n",
			r.Language, r.Transcript, r.Translation, r.TranslateMS, r.ASSStatus, r.ASSMS,
			r.RenderStatus, r.RenderMS, r.RTF, sizeMB, r.Validation, r.DriveLink)
		totalTranslate += r.TranslateMS
		totalASS += r.ASSMS
		totalRender += r.RenderMS
		if r.RenderStatus == "ready" || r.RenderStatus == "reused" {
			rendered++
		}
		if r.Validation == "ok" {
			validated++
		}
	}
	fmt.Println("---")
	fmt.Printf("totals: translate_ms=%d ass_ms=%d render_ms=%d (per-language, summed) | rendered=%d\n",
		totalTranslate, totalASS, totalRender, rendered)
	fmt.Printf("validation: %d/%d PASS\n", validated, len(reports))
}

// printPerLangTiming prints the per-language lifecycle timing and certifies
// that the source (priority 0) starts rendering before the first target
// finishes — i.e. there is NO global "translate-all-then-render" barrier.
func printPerLangTiming(reports []langReport) {
	fmt.Println("=== Per-language timing ===")
	fmt.Printf("%-3s %-8s %-24s %-24s %-24s %-24s %-24s %-8s %-9s\n",
		"pri", "lang", "text_ready_at", "queued_at", "render_started_at", "render_completed_at", "upload_completed_at", "worker", "render_ms")
	var srcStarted time.Time
	var firstTargetReady, firstTargetCompleted time.Time
	for _, r := range reports {
		started := parseTS(r.RenderStartedAt)
		ready := parseTS(r.TextReadyAt)
		completed := parseTS(r.RenderCompletedAt)
		fmt.Printf("%-3d %-8s %-24s %-24s %-24s %-24s %-24s %-8d %-9d\n",
			r.Priority, r.Language, r.TextReadyAt, r.QueuedAt, r.RenderStartedAt, r.RenderCompletedAt, r.UploadCompletedAt, r.WorkerID, r.RenderMS)
		if r.Priority == 0 {
			srcStarted = started
		} else {
			if firstTargetReady.IsZero() || ready.Before(firstTargetReady) {
				firstTargetReady = ready
			}
			if firstTargetCompleted.IsZero() || completed.Before(firstTargetCompleted) {
				firstTargetCompleted = completed
			}
		}
	}
	fmt.Println("---")
	if !srcStarted.IsZero() && !firstTargetCompleted.IsZero() {
		fmt.Printf("certify: source render_started_at < first target render_completed_at => %v\n", srcStarted.Before(firstTargetCompleted))
	}
	if !srcStarted.IsZero() && !firstTargetReady.IsZero() {
		fmt.Printf("certify: source render_started_at < first target text_ready_at => %v\n", srcStarted.Before(firstTargetReady))
	}
}

// formatTS renders a timestamp as RFC3339Nano UTC (empty for the zero time).
func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTS parses an RFC3339Nano timestamp back into time.Time (zero on error).
func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// probeSourceFPS reads the source clip's frame rate via ffprobe so the
// renderer can verify the output kept it (the burn profile never changes
// fps). Best-effort: any failure returns (0, false), which disables the
// exact-match check and leaves only the renderer's sane-range check. The bool
// reports whether ffprobe actually ran, so the caller can count it as the
// single source probe (never per-language).
func probeSourceFPS(ffmpegPath, srcPath string) (float64, bool) {
	if srcPath == "" {
		return 0, false
	}
	ffprobe := "ffprobe"
	if ffmpegPath != "" && ffmpegPath != "ffmpeg" {
		ffprobe = filepath.Join(filepath.Dir(ffmpegPath), "ffprobe")
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=avg_frame_rate", "-of", "default=noprint_wrappers=1:nokey=1", srcPath).Output()
	if err != nil {
		return 0, false
	}
	return multilingual.ParseFPS(strings.TrimSpace(string(out))), true
}
