package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/multilingual"
	sqtexttracks "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	obsinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
)

func runMultilingualRender(args []string) error {
	fs := flag.NewFlagSet("multilingual-render", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated source clip asset IDs (required)")
	sourceLang := fs.String("source-lang", "", "BCP-47 source language (default: config multilingual.source_language)")
	langs := fs.String("languages", "", "BCP-47 csv, [0]=source [1:]=targets (default: config registry)")
	driveFolder := fs.String("drive-folder-id", "", "destination Drive folder (default: asset folder_id or config clips folder)")
	concurrency := fs.Int("concurrency", 4, "render fan-out concurrency")
	translateConcurrency := fs.Int("translate-concurrency", 4, "per-cue translation concurrency")
	backgroundFileID := fs.String("background-file-id", "", "optional Drive file ID for a full-frame background")
	watermarkFileID := fs.String("watermark-file-id", "", "optional Drive file ID for a centered watermark")
	watermarkPosition := fs.String("watermark-position", "center", "watermark position: center, top_left, top_right, bottom_left, bottom_right")
	watermarkOpacity := fs.Float64("watermark-opacity", 1, "watermark opacity 0..1")
	watermarkMargin := fs.Int("watermark-margin-px", 0, "watermark margin in output pixels")
	foregroundScale := fs.Int("foreground-scale-percent", 100, "foreground clip scale on the output canvas, 1..100")
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
	if *foregroundScale < 1 || *foregroundScale > 100 {
		return fmt.Errorf("multilingual-render: --foreground-scale-percent must be within 1..100")
	}
	if *watermarkOpacity < 0 || *watermarkOpacity > 1 {
		return fmt.Errorf("multilingual-render: --watermark-opacity must be within 0..1")
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
	overlays, err := resolveOverlayAssets(cmdContext(), root.Drive.Reader, *backgroundFileID, *watermarkFileID)
	if err != nil {
		return err
	}
	overlays.Position, overlays.Opacity, overlays.MarginPX, overlays.ScalePercent = *watermarkPosition, *watermarkOpacity, *watermarkMargin, *foregroundScale

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
	var runObserver *observability.RunObserver
	if root.ObservabilityDB != nil && root.ObservabilityDB.DB != nil {
		runObserver = observability.NewRunObserver(obsinfra.NewSQLiteRecorderWithLogger(root.ObservabilityDB.DB, log))
	}

	// Process-level CPU/RSS sampled once around the whole run (the admin
	// process is dedicated to this single job).
	cpuStartUser, cpuStartSystem, _ := multilingual.ProcessResources()

	var all []langReport
	var summaries []multilingual.RunMetrics
	var validatedCounts []int
	var docRefs []*multilingual.LocalizationDocRef
	for _, id := range assetIDs {
		baseCtx := cmdContext()
		runCtx := baseCtx
		var run *observability.Run
		if runObserver != nil {
			run = runObserver.StartRun(baseCtx, observability.RunInfo{
				JobID:     "asset:" + id,
				JobType:   "multilingual-render",
				AttemptID: observability.NewObservationID(),
			})
			runCtx = observability.WithRun(baseCtx, run)
		}
		rep, summary, docRef, rErr := processOneClip(runCtx, svc, renderer, subMat, cueRepair, cueTranslator, rec,
			root.Repos.ClipsRepo, root.Repos.TextTrackRepo, cfg, id, srcLang, targetLangs, *driveFolder, *docsFolder, *concurrency, *force, root.Drive.DocClient, overlays, log)
		if run != nil {
			run.FinishWithError(rErr)
		}
		if rErr != nil {
			return rErr
		}
		cpuEndUser, cpuEndSystem, peakRSS := multilingual.ProcessResources()
		summary.CPUUserMS = cpuEndUser - cpuStartUser
		summary.CPUSystemMS = cpuEndSystem - cpuStartSystem
		summary.PeakRSSBytes = peakRSS
		rec.RecordRun(runCtx, summary)
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

type overlayAssets struct {
	BackgroundAssetID string
	BackgroundPath    string
	BackgroundSHA256  string
	WatermarkAssetID  string
	WatermarkPath     string
	WatermarkSHA256   string
	Position          string
	Opacity           float64
	MarginPX          int
	ScalePercent      int
}

func (o overlayAssets) ProfileVersion() string {
	if o.BackgroundSHA256 == "" && o.WatermarkSHA256 == "" && o.ScalePercent == 100 {
		return asset.RenderProfileFFmpegAss1080pV1
	}
	return fmt.Sprintf("%s|bg=%s|wm=%s|wm_pos=%s|wm_opacity=%.6f|wm_margin=%d|fg_scale=%d",
		asset.RenderProfileFFmpegAss1080pV1, o.BackgroundSHA256, o.WatermarkSHA256,
		o.Position, o.Opacity, o.MarginPX, o.ScalePercent)
}

func resolveOverlayAssets(ctx context.Context, reader drive.Reader, backgroundID, watermarkID string) (overlayAssets, error) {
	assets := overlayAssets{}
	if backgroundID == "" && watermarkID == "" {
		return assets, nil
	}
	if reader == nil {
		return assets, fmt.Errorf("multilingual-render: Drive reader is required for overlay assets")
	}
	materialize := func(id, label string) (string, string, error) {
		input, name, err := reader.DownloadFile(ctx, id)
		if err != nil {
			return "", "", fmt.Errorf("download %s %s: %w", label, id, err)
		}
		defer input.Close()
		dir := filepath.Join(os.TempDir(), "pipelinegen-overlays")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		ext := filepath.Ext(name)
		if ext == "" {
			ext = ".asset"
		}
		path := filepath.Join(dir, id+ext)
		out, err := os.Create(path)
		if err != nil {
			return "", "", err
		}
		h := sha256.New()
		if _, err = io.Copy(io.MultiWriter(out, h), input); err != nil {
			out.Close()
			return "", "", err
		}
		if err = out.Close(); err != nil {
			return "", "", err
		}
		return path, hex.EncodeToString(h.Sum(nil)), nil
	}
	if backgroundID != "" {
		path, sha, err := materialize(backgroundID, "background")
		if err != nil {
			return assets, err
		}
		assets.BackgroundAssetID, assets.BackgroundPath, assets.BackgroundSHA256 = backgroundID, path, sha
	}
	if watermarkID != "" {
		path, sha, err := materialize(watermarkID, "watermark")
		if err != nil {
			return assets, err
		}
		assets.WatermarkAssetID, assets.WatermarkPath, assets.WatermarkSHA256 = watermarkID, path, sha
	}
	return assets, nil
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
