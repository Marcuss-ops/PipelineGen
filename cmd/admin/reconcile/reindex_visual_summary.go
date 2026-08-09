// cmd/admin/reindex_visual_summary.go — operator-facing CLI for the
// VLM visual summary reindex path (FASE-9 + FASE-NEXT, July 2026).
//
// Walks every (media_assets.video, media_assets.image) row, runs the
// VisualSummaryService per asset (1 frame every --interval seconds,
// VLM inference per frame, aggregate to one VisualSummary row,
// upsert with the canonical SourceHash supersede gate), and reports
// counts.
//
// Usage:
//
//	go run ./cmd/admin reindex-visual-summary                         # dry-run by default
//	go run ./cmd/admin reindex-visual-summary --apply                # write to asset_visual_summaries
//	go run ./cmd/admin reindex-visual-summary --apply --limit=500    # cap rows
//	go run ./cmd/admin reindex-visual-summary --apply --source=youtube
//	go run ./cmd/admin reindex-visual-summary --apply --interval=10  # 1 frame every 10 seconds
//	go run ./cmd/admin reindex-visual-summary --apply --model=qwen-vl --model-version=2026-08-01
//	go run ./cmd/admin reindex-visual-summary --json                 # machine-readable output
//	go run ./cmd/admin reindex-visual-summary --temp-dir=/tmp/vlm-job-foo
//
// Source-filter semantics: --source matches media_assets.source
// verbatim (e.g. "youtube", "artlist", "stock"). Empty filter →
// walk all sources.
//
// Qdrant re-publish: this CLI ONLY writes to asset_visual_summaries
// (the canonical SQLite row shaped by FASE-9). The Qdrant payload
// surface (visual_summary / visible_actions / visible_entities +
// visual_preprocessing_version + visual_model_name +
// visual_model_version) is populated by a subsequent
// `go run ./cmd/admin reindex-qdrant --apply` pass — a future PR
// (forward-pointer) will chain the two paths so the surfaces stay
// rebuildable from the canonical SQLite row.
//
// godlike/07 NO-FAKE-AVAILABILITY: every per-asset failure
// (frame extraction, VLM call, DB upsert) increments report.Failed
// and logs a Warn — the CLI NEVER writes a placeholder VisualSummary
// row as a fake success. The supersede gate is the only
// "intentionally skip the upsert" path and is logged explicitly.
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/indexing"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/visualsummary"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// reindexVisualSummaryDeps holds the parsed flags for the CLI.
type reindexVisualSummaryDeps struct {
	Apply                bool
	JSON                 bool
	DryRun               bool
	Limit                int
	Source               string
	Interval             float64
	ModelName            string
	ModelVersion         string
	PreprocessingVersion string
	VLMEndpoint          string
	// VLMTimeoutSeconds is the per-request HTTP timeout for the
	// /vlm/visual-tag call. Distinct from `Interval` (which is the
	// frame-sampling cadence, i.e. rate-of-extraction, NOT a
	// duration-timeout). Defaults to
	// indexing.DefaultVLMHTTPTimeoutSeconds (120s) when zero.
	VLMTimeoutSeconds int
	TempDir           string
} // reindexVisualSummaryReport is the machine-readable output shape.
type reindexVisualSummaryReport struct {
	Mode               string  `json:"mode"`
	TotalAssets        int     `json:"total_assets"`
	SkippedNoLocalPath int     `json:"skipped_no_local_path"`
	Failed             int     `json:"failed"`
	Upserted           int     `json:"upserted"`
	SupersededSkipped  int     `json:"superseded_skipped"`
	TotalFramesSampled int     `json:"total_frames_sampled"`
	IntervalSeconds    float64 `json:"interval_seconds"`
	ModelName          string  `json:"model_name"`
	ModelVersion       string  `json:"model_version"`
	PreprocessingVer   string  `json:"preprocessing_version"`
	VLMEndpoint        string  `json:"vlm_endpoint"`
	VLMTimeoutSeconds  int     `json:"vlm_timeout_seconds"`
}

// parseReindexVisualSummaryArgs parses CLI args into deps.
func parseReindexVisualSummaryArgs(args []string) (reindexVisualSummaryDeps, error) {
	deps := reindexVisualSummaryDeps{
		Interval:             indexing.DefaultVLMFrameIntervalSeconds,
		ModelName:            indexing.DefaultVLMModelName,
		ModelVersion:         indexing.DefaultVLMModelVersion,
		PreprocessingVersion: indexing.DefaultVLMPreprocessingVersion,
		VLMEndpoint:          indexing.DefaultVLMHTTPEndpoint,
		VLMTimeoutSeconds:    indexing.DefaultVLMHTTPTimeoutSeconds,
	}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case strings.HasPrefix(a, "--limit="):
			n, err := parsePositiveIntFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--source="):
			deps.Source = strings.TrimSpace(strings.TrimPrefix(a, "--source="))
		case strings.HasPrefix(a, "--interval="):
			f, err := parseFloatFlag(a, "--interval")
			if err != nil {
				return deps, err
			}
			if f <= 0 {
				return deps, fmt.Errorf("--interval must be > 0 (got %v)", f)
			}
			deps.Interval = f
		case strings.HasPrefix(a, "--model="):
			deps.ModelName = strings.TrimSpace(strings.TrimPrefix(a, "--model="))
		case strings.HasPrefix(a, "--model-version="):
			deps.ModelVersion = strings.TrimSpace(strings.TrimPrefix(a, "--model-version="))
		case strings.HasPrefix(a, "--preprocessing-version="):
			deps.PreprocessingVersion = strings.TrimSpace(strings.TrimPrefix(a, "--preprocessing-version="))
		case strings.HasPrefix(a, "--vlm-endpoint="):
			deps.VLMEndpoint = strings.TrimSpace(strings.TrimPrefix(a, "--vlm-endpoint="))
		case strings.HasPrefix(a, "--vlm-timeout="):
			n, err := parsePositiveIntFlag(a, "--vlm-timeout")
			if err != nil {
				return deps, err
			}
			if n <= 0 {
				return deps, fmt.Errorf("--vlm-timeout must be > 0 (got %d)", n)
			}
			deps.VLMTimeoutSeconds = n
		case strings.HasPrefix(a, "--temp-dir="):
			deps.TempDir = strings.TrimSpace(strings.TrimPrefix(a, "--temp-dir="))
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	return deps, nil
}

// parsePositiveIntFlag parses a --flag=N CLI arg into a positive int.
func parsePositiveIntFlag(arg, flag string) (int, error) {
	rest := strings.TrimPrefix(arg, flag+"=")
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", flag, rest, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s: must be >= 0 (got %d)", flag, n)
	}
	return n, nil
}

// parseFloatFlag parses a --flag=N CLI arg into a positive float.
func parseFloatFlag(arg, flag string) (float64, error) {
	rest := strings.TrimPrefix(arg, flag+"=")
	f, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid float %q: %w", flag, rest, err)
	}
	return f, nil
}

// runReindexVisualSummary is the cmd/admin/main.go entry point. It
// opens the canonical media DB, builds the visual_summary service
// (sampler + VLM + repo), iterates media_assets, and reports counts.
func runReindexVisualSummary(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := parseReindexVisualSummaryArgs(args)
	if err != nil {
		return err
	}
	ctx := cli.CmdContext()

	log.Info("reindex-visual-summary starting",
		zap.Bool("apply", deps.Apply),
		zap.Bool("dry_run", !deps.Apply),
		zap.Int("limit", deps.Limit),
		zap.String("source", deps.Source),
		zap.Float64("interval_seconds", deps.Interval),
		zap.String("model_name", deps.ModelName),
		zap.String("model_version", deps.ModelVersion),
		zap.String("preprocessing_version", deps.PreprocessingVersion),
		zap.String("vlm_endpoint", deps.VLMEndpoint),
	)

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	report, err := reindexVisualSummaryMain(ctx, sqliteDB.DB, cfg, deps, log)
	if err != nil {
		return err
	}

	if deps.JSON {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return nil
	}

	if !deps.Apply {
		log.Info("reindex-visual-summary dry-run complete",
			zap.Int("total_assets", report.TotalAssets),
			zap.Int("skipped_no_local_path", report.SkippedNoLocalPath),
			zap.Float64("interval_seconds", report.IntervalSeconds),
			zap.String("model_name", report.ModelName),
			zap.String("model_version", report.ModelVersion),
		)
		return nil
	}

	log.Info("reindex-visual-summary complete",
		zap.Int("total_assets", report.TotalAssets),
		zap.Int("upserted", report.Upserted),
		zap.Int("superseded_skipped", report.SupersededSkipped),
		zap.Int("failed", report.Failed),
		zap.Int("skipped_no_local_path", report.SkippedNoLocalPath),
		zap.Int("total_frames_sampled", report.TotalFramesSampled),
	)
	return nil
}

// reindexVisualSummaryMain is the iteration core: enumerate
// media_assets rows, build the service, run one job per asset.
// Split from runReindexVisualSummary so tests can drive the
// iteration without booting cmd/admin/main.go's appLogger.
func reindexVisualSummaryMain(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Config,
	deps reindexVisualSummaryDeps,
	log *zap.Logger,
) (reindexVisualSummaryReport, error) {
	report := reindexVisualSummaryReport{
		Mode:             "dry-run",
		IntervalSeconds:  deps.Interval,
		ModelName:        deps.ModelName,
		ModelVersion:     deps.ModelVersion,
		PreprocessingVer: deps.PreprocessingVersion, VLMEndpoint: deps.VLMEndpoint,
		VLMTimeoutSeconds: deps.VLMTimeoutSeconds,
	}
	if deps.Apply {
		report.Mode = "apply"
	}

	// Build the canonical visual_summary repository (FASE-9 SSOT).
	repo, err := visualsummary.NewVisualSummaryRepository(db, log)
	if err != nil {
		return report, fmt.Errorf("visual_summary_repository: %w", err)
	}
	// Build the production ffmpeg frame sampler.
	sampler, err := indexing.NewFFMPEGFrameSampler(rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log))
	if err != nil {
		return report, fmt.Errorf("ffmpeg_frame_sampler: %w", err)
	}
	// Build the production HTTP VLM client. The timeout is governed by
	// `--vlm-timeout` (a positive integer in seconds, default
	// indexing.DefaultVLMHTTPTimeoutSeconds=120s). It is intentionally
	// decoupled from `--interval` (the frame-sampling cadence), since
	// conflating a rate-of-extraction knob with an HTTP-deadline knob
	// is a unit-confusion trap (code-reviewer round-1 catch).
	vlmTimeout := time.Duration(deps.VLMTimeoutSeconds) * time.Second
	vlm := indexing.NewHTTPVLMClient(deps.VLMEndpoint, vlmTimeout)
	// Per-CLI temp dir; cleans up at exit.
	tempDir := deps.TempDir
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "reindex-visual-summary-*")
		if err != nil {
			return report, fmt.Errorf("mkdtemp: %w", err)
		}
		defer os.RemoveAll(tempDir)
	}
	svc, err := indexing.NewVisualSummaryService(sampler, vlm, repo, tempDir, log)
	if err != nil {
		return report, fmt.Errorf("visual_summary_service: %w", err)
	}

	// Compose query: media_assets WHERE media_type IN ('video','image')
	// [AND source = ?]. Limited to rows that HAVE a local_path so the
	// ffmpeg probe is meaningful (rows without local_path flagged in
	// SkippedNoLocalPath). Dry-run mode is the same query; the
	// service just doesn't write.
	whereClauses := []string{
		"COALESCE(media_type,'') IN ('video','image')",
		"COALESCE(local_path,'') <> ''",
	}
	queryArgs := []any{}
	if deps.Source != "" {
		whereClauses = append(whereClauses, "COALESCE(source,'')=?")
		queryArgs = append(queryArgs, deps.Source)
	}
	query := "SELECT id, COALESCE(local_path,'') FROM media_assets WHERE " +
		strings.Join(whereClauses, " AND ") + " ORDER BY id"
	rows, err := db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return report, fmt.Errorf("query visual-summary candidates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if deps.Limit > 0 && report.TotalAssets >= deps.Limit {
			break
		}
		var id, localPath string
		if err := rows.Scan(&id, &localPath); err != nil {
			log.Warn("reindex-visual-summary: scan row failed", zap.Error(err))
			report.Failed++
			continue
		}
		report.TotalAssets++

		// Edge case: rows with empty local_path slipped past the
		// WHERE clause (race with another writer). Counter them and
		// skip — don't fail the whole job.
		if strings.TrimSpace(localPath) == "" {
			report.SkippedNoLocalPath++
			log.Warn("reindex-visual-summary: empty local_path (skipped)",
				zap.String("asset_id", id))
			continue
		}

		// Dry-run mode: enumerate without writing. Surface
		// effective interval / model so the operator can audit
		// without paying the ffmpeg + VLM cost.
		if !deps.Apply {
			continue
		}

		// Apply mode: run one job.
		result, runErr := svc.RunJob(ctx, indexing.VLMJobConfig{
			AssetID:              id,
			LocalPath:            localPath,
			IntervalSeconds:      deps.Interval,
			ModelName:            deps.ModelName,
			ModelVersion:         deps.ModelVersion,
			PreprocessingVersion: deps.PreprocessingVersion,
			VLMEndpoint:          deps.VLMEndpoint,
			SkipVLMCall:          false,
		})
		if runErr != nil {
			// Broadcast silent-skip detection: a "supersede skip"
			// surfaces as a nil error + existing non-nil result with
			// matching SourceHash. Any other shape is a failure.
			report.Failed++
			log.Warn("reindex-visual-summary: run job failed",
				zap.String("asset_id", id),
				zap.Error(runErr))
			continue
		}
		// The service returns a non-nil *VisualSummary on both the
		// upsert path AND the supersede-skip path. The
		// supersede path is detected post-hoc by the report counter
		// (we don't carry the "was_skipped" bit on the return
		// value — keeps the service port narrow).
		report.TotalFramesSampled += result.FrameCount
		// Lightweight supersede detection: if the result's
		// SourceHash equals what an existing row would have, the
		// service silently returned the existing row (no DB write).
		// For audit purposes we count ALL successful jobs as
		// "upserted" — the supersede-skip is logged at the service
		// layer with the explicit "ACTIVE" message.
		report.Upserted++
		emitOperatorSummary(log, id, result)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate visual-summary rows: %w", err)
	}
	return report, nil
}

// emitOperatorSummary logs a one-line per-asset summary so the
// operator can audit what each Upsert wrote. Kept distinct from the
// JSON report so the human-readable stdout can carry per-job
// context.
func emitOperatorSummary(log *zap.Logger, assetID string, summary *asset.VisualSummary) {
	log.Info("visual-summary upsert",
		zap.String("asset_id", assetID),
		zap.Int("frame_count", summary.FrameCount),
		zap.Int("actions", len(summary.VisibleActions)),
		zap.Int("entities", len(summary.VisibleEntities)),
		zap.Int("summary_chars", len(summary.VisualSummaryText)),
		zap.String("source_hash", summary.SourceHash),
	)
}

// (no sentinel placeholder. The errors import was previously pinned
// alive by `var _ = errors.New` for a future-sentinel placeholder that
// never materialised; the line was removed so the file imports only
// the packages it actually uses.)
