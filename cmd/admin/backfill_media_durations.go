package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// runBackfillMediaDurations measures existing video assets and republishes
// each changed asset through the canonical outbox. Duration resolution
// follows the canonical precedence (internal/kernel/asset.ResolveAssetDuration):
//
//  1. local binary present on disk  → probe (authoritative);
//  2. trusted provider metadata     → provider_metadata (already declared);
//  3. Drive binary materializable   → temporary probe;
//  4. otherwise                     → unknown (never a fabricated zero).
//
// It intentionally does not leave a local copy behind for the Drive-probe
// path: the catalog remains Drive-backed while duration and measured
// dimensions become durable metadata.
func runBackfillMediaDurations(args []string) error {
	fs := flag.NewFlagSet("backfill-media-durations", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", "", "Limit to assets in this Drive folder or its immediate catalog path")
	assetIDs := fs.String("asset-ids", "", "Comma-separated asset IDs to process (optional)")
	limit := fs.Int("limit", 0, "Maximum number of assets to process; zero means all")
	force := fs.Bool("force", false, "Re-probe selected assets even when duration_ms is already positive")
	retainDir := fs.String("retain-dir", "", "Persist downloaded probe files under this directory for physical render verification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*folderID) == "" && strings.TrimSpace(*assetIDs) == "" {
		return fmt.Errorf("one of --folder-id or --asset-ids is required")
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	if strings.TrimSpace(*retainDir) != "" {
		if err := os.MkdirAll(*retainDir, 0o755); err != nil {
			return fmt.Errorf("create retain directory: %w", err)
		}
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.Drive == nil || root.Drive.Reader == nil ||
		root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil ||
		root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("database, Drive reader, clips repository and outbox are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	rows, err := selectDurationBackfillRows(ctx, root.DB, *folderID, *assetIDs, *limit, *force)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("No ACTIVE/INDEXED video assets require duration backfill")
		return nil
	}

	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	probe := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	report := durationBackfillReport{AssetsTotal: len(rows)}
	var failed int
	for _, row := range rows {
		outcome, err := backfillOneMediaDuration(ctx, root, probe, row, *retainDir)
		if err != nil {
			failed++
			fmt.Printf("FAILED id=%s: %v\n", row.ID, err)
			continue
		}
		report.Count(outcome)
		fmt.Printf("%-22s id=%s duration_ms=%d width=%d height=%d\n",
			outcome.Kind, row.ID, outcome.DurationMS, outcome.Width, outcome.Height)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Println(report.String())
	if failed > 0 {
		return fmt.Errorf("duration backfill failed for %d of %d assets", failed, len(rows))
	}
	return nil
}

// durationBackfillRow identifies one eligible asset. Duration, provenance and
// the local path are read from the canonical clip (via ClipsRepo) rather than
// from the row, so classification always sees the post-load asset state.
type durationBackfillRow struct {
	ID string
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func selectDurationBackfillRows(ctx context.Context, db queryer, folderID, rawIDs string, limit int, force bool) ([]durationBackfillRow, error) {
	where := []string{
		"media_type IN ('video', 'clip')",
		"UPPER(COALESCE(lifecycle_state, '')) = 'ACTIVE'",
		"UPPER(COALESCE(index_state, '')) = 'INDEXED'",
		"(TRIM(COALESCE(drive_file_id, '')) <> '' OR TRIM(COALESCE(local_path, '')) <> '')",
	}
	if !force {
		where = append(where, "COALESCE(duration_ms, 0) <= 0")
	}
	args := make([]any, 0)
	if ids := splitBackfillCSV(rawIDs); len(ids) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		where = append(where, "id IN ("+marks+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if folderID = strings.TrimSpace(folderID); folderID != "" {
		where = append(where, "(parent_folder_id = ? OR drive_folder_id = ? OR folder_id = ?)")
		args = append(args, folderID, folderID, folderID)
	}
	query := "SELECT id FROM media_assets WHERE " + strings.Join(where, " AND ") + " ORDER BY id"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select duration backfill assets: %w", err)
	}
	defer rows.Close()
	var result []durationBackfillRow
	for rows.Next() {
		var row durationBackfillRow
		if err := rows.Scan(&row.ID); err != nil {
			return nil, fmt.Errorf("scan duration backfill asset: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duration backfill assets: %w", err)
	}
	return result, nil
}

// durationBackfillState classifies an asset's existing duration so the
// backfill can distinguish "already known" from "missing" from "corrupt
// (zero/negative that must never be fabricated away)".
type durationBackfillState int

const (
	durationStateMissing durationBackfillState = iota
	durationStateKnownProbe
	durationStateKnownProvider
	durationStateInvalidZero
	durationStateNegative
)

// classifyDurationAsset maps an asset's current duration + provenance onto the
// backfill decision state. A positive duration is known (probe vs provider
// provenance via DurationProvenance); a negative duration is corrupt; a zero
// duration carrying a known provenance tag is corrupt; a zero duration with no
// known tag is genuinely missing.
func classifyDurationAsset(clip *asset.Asset) durationBackfillState {
	if clip == nil {
		return durationStateMissing
	}
	if clip.Duration < 0 {
		return durationStateNegative
	}
	if clip.Duration > 0 {
		if clip.DurationProvenance() == asset.DurationProbe {
			return durationStateKnownProbe
		}
		return durationStateKnownProvider
	}
	if raw := strings.TrimSpace(clip.GetMetadataString("duration_source")); raw != "" {
		if asset.NormalizeDurationSource(raw) != asset.DurationUnknown {
			return durationStateInvalidZero
		}
	}
	return durationStateMissing
}

// durationBackfillOutcome reports what the backfill did for one asset. Kind is
// one of: already_known, provider_metadata, probed_local, probed_drive,
// still_unknown, invalid_zero_duration, negative_duration.
type durationBackfillOutcome struct {
	Kind       string
	DurationMS int64
	Width      int
	Height     int
}

// backfillOneMediaDuration resolves a single asset's total video duration with
// the canonical precedence and persists a probe measurement. "Unknown" is an
// explicit outcome (fail-closed), never a fabricated zero. A non-nil error is
// reserved for hard failures (asset load/persist), not for "no source".
func backfillOneMediaDuration(ctx context.Context, root *wiring.ComposeRoot, probe *rustexec.VideoProcessor, row durationBackfillRow, retainDir string) (durationBackfillOutcome, error) {
	clip, err := root.Repos.ClipsRepo.GetClip(ctx, row.ID)
	if err != nil {
		return durationBackfillOutcome{}, fmt.Errorf("load asset: %w", err)
	}
	if clip == nil {
		return durationBackfillOutcome{}, fmt.Errorf("asset disappeared from repository")
	}

	switch classifyDurationAsset(clip) {
	case durationStateKnownProbe:
		return durationBackfillOutcome{Kind: "already_known", DurationMS: clip.Duration.Milliseconds()}, nil
	case durationStateKnownProvider:
		return durationBackfillOutcome{Kind: "provider_metadata", DurationMS: clip.Duration.Milliseconds()}, nil
	case durationStateNegative:
		return durationBackfillOutcome{Kind: "negative_duration"}, nil
	case durationStateInvalidZero:
		return durationBackfillOutcome{Kind: "invalid_zero_duration"}, nil
	case durationStateMissing:
	}

	// Precedence 1: probe an existing local binary — authoritative, no download.
	if localPath := strings.TrimSpace(clip.LocalPath()); localPath != "" {
		if st, statErr := os.Stat(localPath); statErr == nil && st.Mode().IsRegular() {
			info, probeErr := probe.Probe(ctx, localPath)
			if probeErr == nil && info != nil && info.HasVideo && info.Duration > 0 && info.Width > 0 && info.Height > 0 {
				if err := persistMeasuredDuration(ctx, root, clip, info.Duration, info.Width, info.Height); err != nil {
					return durationBackfillOutcome{}, err
				}
				return durationBackfillOutcome{Kind: "probed_local", DurationMS: info.Duration.Milliseconds(), Width: info.Width, Height: info.Height}, nil
			}
		}
	}

	// Precedence 2: materialize the Drive binary and probe it.
	if driveFileID := strings.TrimSpace(clip.DriveFileID()); driveFileID != "" {
		duration, width, height, retainedPath, probeErr := probeDriveDuration(ctx, root, probe, driveFileID, clip.ID, retainDir)
		if probeErr == nil {
			if strings.TrimSpace(retainedPath) != "" {
				clip.SetLocalPath(retainedPath)
			}
			if err := persistMeasuredDuration(ctx, root, clip, duration, width, height); err != nil {
				return durationBackfillOutcome{}, err
			}
			return durationBackfillOutcome{Kind: "probed_drive", DurationMS: duration.Milliseconds(), Width: width, Height: height}, nil
		}
	}

	return durationBackfillOutcome{Kind: "still_unknown"}, nil
}

// persistMeasuredDuration writes a probe-derived duration + dimensions through
// the canonical outbox with the canonical provenance tag.
func persistMeasuredDuration(ctx context.Context, root *wiring.ComposeRoot, clip *asset.Asset, duration time.Duration, width, height int) error {
	clip.Duration = duration
	clip.SetMetadataString("duration_source", string(asset.DurationProbe))
	clip.SetMetadataInt("width", width)
	clip.SetMetadataInt("height", height)
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.LegacyFileMD5()); err != nil {
		return fmt.Errorf("persist and index measured asset: %w", err)
	}
	return nil
}

// probeDriveDuration downloads a Drive binary to a temp file (or the retain
// directory) and probes it. It returns the retained local path when retainDir
// is set, otherwise the empty string.
func probeDriveDuration(ctx context.Context, root *wiring.ComposeRoot, probe *rustexec.VideoProcessor, driveFileID, assetID, retainDir string) (time.Duration, int, int, string, error) {
	reader, _, err := root.Drive.Reader.DownloadFile(ctx, driveFileID)
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("download Drive file: %w", err)
	}
	defer reader.Close()
	tmpPath := ""
	if strings.TrimSpace(retainDir) != "" {
		tmpPath = filepath.Join(retainDir, assetID+".mp4")
	} else {
		tmp, createErr := os.CreateTemp("", "pipelinegen-duration-*.media")
		if createErr != nil {
			return 0, 0, 0, "", fmt.Errorf("create probe temp file: %w", createErr)
		}
		tmpPath = tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)
	}
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("open probe file: %w", err)
	}
	if _, err := io.Copy(tmp, reader); err != nil {
		_ = tmp.Close()
		return 0, 0, 0, "", fmt.Errorf("download Drive file bytes: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, 0, 0, "", fmt.Errorf("close probe temp file: %w", err)
	}
	info, err := probe.Probe(ctx, tmpPath)
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("ffprobe: %w", err)
	}
	if info == nil || !info.HasVideo || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return 0, 0, 0, "", fmt.Errorf("invalid video probe result duration=%s width=%d height=%d has_video=%t", info.Duration, info.Width, info.Height, info.HasVideo)
	}
	retained := ""
	if strings.TrimSpace(retainDir) != "" {
		retained = tmpPath
	}
	return info.Duration, info.Width, info.Height, retained, nil
}

// durationBackfillReport tallies the outcome of a duration backfill run.
type durationBackfillReport struct {
	AssetsTotal         int
	AlreadyKnown        int // positive duration, probe provenance
	ProviderMetadata    int // positive duration, provider provenance
	ProbedLocal         int // missing -> probed local binary
	ProbedDrive         int // missing -> probed materialized Drive binary
	StillUnknown        int // missing -> no reliable source (fail-closed)
	InvalidZeroDuration int // duration_ms == 0 with a known provenance tag
	NegativeDuration    int // duration_ms < 0
}

// Count folds one outcome into the report.
func (r *durationBackfillReport) Count(o durationBackfillOutcome) {
	switch o.Kind {
	case "already_known":
		r.AlreadyKnown++
	case "provider_metadata":
		r.ProviderMetadata++
	case "probed_local":
		r.ProbedLocal++
	case "probed_drive":
		r.ProbedDrive++
	case "still_unknown":
		r.StillUnknown++
	case "invalid_zero_duration":
		r.InvalidZeroDuration++
	case "negative_duration":
		r.NegativeDuration++
	}
}

func (r durationBackfillReport) String() string {
	return fmt.Sprintf(`VIDEO DURATION BACKFILL

assets_total             = %d
already_known            = %d
probed_local             = %d
probed_drive             = %d
provider_metadata        = %d
still_unknown            = %d
invalid_zero_duration    = %d
negative_duration        = %d`,
		r.AssetsTotal, r.AlreadyKnown, r.ProbedLocal, r.ProbedDrive,
		r.ProviderMetadata, r.StillUnknown, r.InvalidZeroDuration, r.NegativeDuration)
}

func splitBackfillCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}
