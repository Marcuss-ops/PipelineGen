package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/multilingual"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqtexttracks "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// cmd/admin/multilingual_benchmark.go — speedup benchmark for the
// multilingual render fan-out.
//
// Runs the SAME render fan-out (identical inputs, identical output profile)
// at each configured concurrency level (default 1,2,4,8) with the variant
// cache bypassed (Force) and publishing skipped (SkipPublish), so every run
// re-executes the real ffmpeg burn + ffprobe validation instead of reusing a
// completed variant. The result is the classic speedup table:
//
//	speedup(N)        = wall(serial) / wall(N)
//	parallel_efficiency(N) = speedup(N) / N
//
// which answers "does adding workers actually help, and by how much?" with
// numbers instead of eye-balling wall times. Requires that the .ass artifacts
// for every language already exist (run `multilingual-render` first, or any
// earlier ASS materialization): the benchmark measures RENDER ONLY — it never
// re-translates, never re-generates ASS, never uploads to Drive.
//
// Usage:
//
//	pipelinegen-admin multilingual-benchmark \
//	    --asset-ids=manual_9b8daf320154 \
//	    --languages=en,it,pl,ru,de,es,pt-BR,fr,tr,id \
//	    --concurrency=1,2,4,8 --repeats=3 --json
func runMultilingualBenchmark(args []string) error {
	fs := flag.NewFlagSet("multilingual-benchmark", flag.ContinueOnError)
	ids := fs.String("asset-ids", "", "comma-separated source clip asset IDs (required)")
	sourceLang := fs.String("source-lang", "", "BCP-47 source language (default: config multilingual.source_language)")
	langs := fs.String("languages", "", "BCP-47 csv, [0]=source [1:]=targets (default: config registry)")
	concs := fs.String("concurrency", "1,2,4,8", "csv of concurrency levels to benchmark")
	repeats := fs.Int("repeats", 1, "samples per concurrency level (results are averaged)")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	assetIDs := splitCSV(*ids)
	if len(assetIDs) == 0 {
		return fmt.Errorf("multilingual-benchmark: --asset-ids is required")
	}
	levels := parseIntList(*concs)
	if len(levels) == 0 {
		return fmt.Errorf("multilingual-benchmark: --concurrency must be a non-empty csv of positive ints")
	}
	for _, n := range levels {
		if n < 1 {
			return fmt.Errorf("multilingual-benchmark: concurrency levels must be >= 1, got %d", n)
		}
	}
	if *repeats < 1 {
		*repeats = 1
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

	variantRepo, err := sqtexttracks.NewRenderVariantRepository(root.DB.DB, log)
	if err != nil {
		return fmt.Errorf("multilingual-benchmark: new variant repo: %w", err)
	}
	renderer, err := multilingual.NewRenderer(variantRepo, root.Drive.Publisher, cfg.External.FfmpegPath, log)
	if err != nil {
		return fmt.Errorf("multilingual-benchmark: new renderer: %w", err)
	}

	srcLang, targetLangs := resolveLanguages(cfg, *sourceLang, *langs)

	type assetResult struct {
		AssetID string                         `json:"asset_id"`
		Samples []multilingual.BenchmarkSample `json:"samples"`
	}
	var out []assetResult
	for _, id := range assetIDs {
		inputs, err := buildBenchmarkInputs(cmdContext(), root.Repos.ClipsRepo, root.Repos.TextTrackRepo, root.Repos.SubtitleArtifactRepo, cfg, id, srcLang, targetLangs, log)
		if err != nil {
			return err
		}
		if len(inputs) == 0 {
			return fmt.Errorf("multilingual-benchmark: %s: no renderable languages resolved", id)
		}
		samples := make([]multilingual.BenchmarkSample, 0, len(levels))
		for _, n := range levels {
			sample := benchmarkLevel(cmdContext(), renderer, inputs, n, *repeats)
			samples = append(samples, sample)
		}
		multilingual.ComputeSpeedup(samples)
		out = append(out, assetResult{AssetID: id, Samples: samples})

		if !*jsonOut {
			fmt.Printf("\n=== Speedup benchmark: %s (languages=%d, repeat=%d, cache disabled) ===\n", id, len(inputs), *repeats)
			printBenchmarkTable(samples)
		}
	}

	if *jsonOut {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	}
	return nil
}

// benchmarkLevel runs the render fan-out `repeats` times at one concurrency
// level (cache bypassed, publish skipped) and averages wall/work/observed
// across the repeats into a single BenchmarkSample.
func benchmarkLevel(ctx context.Context, renderer *multilingual.Renderer, inputs []multilingual.VariantInput, concurrency, repeats int) multilingual.BenchmarkSample {
	var sumWall, sumWork int64
	var sumAvg float64
	maxObs := 0
	for i := 0; i < repeats; i++ {
		start := time.Now()
		report, err := renderer.RenderAll(ctx, inputs, concurrency)
		wall := time.Since(start).Milliseconds()
		if err != nil || report == nil {
			continue
		}
		sumWall += wall
		sumWork += report.Concurrency.TotalWorkMS
		sumAvg += report.Concurrency.AvgObserved
		if report.Concurrency.MaxObserved > maxObs {
			maxObs = report.Concurrency.MaxObserved
		}
	}
	if repeats > 1 {
		sumWall /= int64(repeats)
		sumWork /= int64(repeats)
		sumAvg /= float64(repeats)
	}
	return multilingual.BenchmarkSample{
		Concurrency: concurrency,
		WallMS:      sumWall,
		WorkMS:      sumWork,
		MaxObserved: maxObs,
		AvgObserved: sumAvg,
	}
}

// buildBenchmarkInputs resolves the render inputs for one clip WITHOUT any
// translation/ASS generation: it reads the existing READY .ass artifacts
// (SubtitleArtifactRepo.FindCurrent) + the timed track for each language, so
// the benchmark measures render cost only. Shared source identity (local path,
// sha, duration) is resolved once — never per language.
func buildBenchmarkInputs(
	ctx context.Context,
	clips *sqassets.ClipsRepository,
	trackRepo asset.TextTrackRepository,
	subRepo asset.SubtitleArtifactRepository,
	cfg *config.Config,
	id, srcLang string,
	targetLangs []string,
	log *zap.Logger,
) ([]multilingual.VariantInput, error) {
	item, err := clips.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("multilingual-benchmark: get asset %s: %w", id, err)
	}
	if item == nil {
		return nil, fmt.Errorf("multilingual-benchmark: asset %s not found", id)
	}
	sourcePath := item.LocalPath()
	if sourcePath == "" {
		sourcePath = item.Filename
	}
	sourceSHA := item.ContentHash()
	if sourceSHA == "" {
		sourceSHA = item.FileHash()
	}
	base := strings.TrimSuffix(filepath.Base(item.Filename), filepath.Ext(item.Filename))
	if base == "" {
		base = id
	}
	folder := resolveDriveFolder(cfg, item, "")
	order := append([]string{srcLang}, targetLangs...)

	inputs := make([]multilingual.VariantInput, 0, len(order))
	for _, lang := range order {
		art, err := subRepo.FindCurrent(ctx, id, lang, asset.SubtitleFormatASS)
		if err != nil || art == nil || art.LocalPath == "" {
			log.Warn("multilingual-benchmark: no READY ASS artifact, skipping language",
				zap.String("asset_id", id), zap.String("lang", lang))
			continue
		}
		track, _, err := trackRepo.FindReady(ctx, id, lang, asset.TextTrackTranscript)
		transcriptSHA := ""
		translationVersion := ""
		if err == nil && track != nil {
			transcriptSHA = track.TextHash
			translationVersion = track.ModelVersion
			if translationVersion == "" {
				translationVersion = track.SourceVersion
			}
		}
		inputs = append(inputs, multilingual.VariantInput{
			SourceClipID:         id,
			SourcePath:           sourcePath,
			SourceSHA256:         sourceSHA,
			SourceDuration:       item.Duration,
			Language:             lang,
			TranscriptSHA256:     transcriptSHA,
			TranslationVersion:   translationVersion,
			SubtitleStyleVersion: subtitleStyleVersion,
			ASSPath:              art.LocalPath,
			ASSHash:              art.FileHash,
			OutputFilename:       base + "." + lang + ".mp4",
			DriveFolderID:        folder,
			WorkDir:              filepath.Join("data", "media", "renders"),
			Force:                true, // bypass variant reuse → always re-render
			SkipPublish:          true, // benchmark measures render+validate only
		})
	}
	return inputs, nil
}

func printBenchmarkTable(samples []multilingual.BenchmarkSample) {
	fmt.Printf("%-12s %-10s %-10s %-10s %-11s %-9s %-11s\n",
		"concurrency", "wall_ms", "work_ms", "max_obs", "avg_obs", "speedup", "efficiency")
	for _, s := range samples {
		speedup := "—"
		eff := "—"
		if s.Speedup > 0 {
			speedup = fmt.Sprintf("%.2fx", s.Speedup)
			eff = fmt.Sprintf("%.0f%%", s.Efficiency*100)
		}
		fmt.Printf("%-12d %-10d %-10d %-10d %-11.2f %-9s %-11s\n",
			s.Concurrency, s.WallMS, s.WorkMS, s.MaxObserved, s.AvgObserved, speedup, eff)
	}
}

// parseIntList parses a csv of positive integers (e.g. "1,2,4,8") into an
// ordered slice. Empty/invalid entries are skipped; malformed tokens that
// parse as an error are ignored so a trailing comma or whitespace never
// aborts the benchmark. Callers validate that the result is non-empty.
func parseIntList(csv string) []int {
	var out []int
	for _, part := range splitCSV(csv) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil {
			out = append(out, n)
		}
	}
	return out
}
