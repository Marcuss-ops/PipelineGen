// cmd/admin/backfill_clip_folder_path.go — realigns media_assets.folder_id /
// folder_path with the PHYSICAL Google Drive location of processed YouTube
// clips.
//
// Background (2026-08-06 audit): the per-clip upload path threads the
// request folder_id as the publisher ParentFolderID, so the canonical
// YouTubeClipPath builder creates a nested `{group-fallback}/{video_id}`
// subfolder inside it (e.g. `Tom Holland/youtube_uncategorized/uVoMqnwEdBQ`).
// The ClipAsset writer persisted the REQUEST folder (1omaKrmSHurA9y /
// "Tom Holland") instead of the physical leaf folder, so media_assets rows
// and the actual Drive tree diverged for every nested clip.
//
// This one-shot reconciliation resolves the physical location of each
// drive_file_id by walking its Drive parent chain up to the configured root
// (cfg.Drive.NormalClipsSourceFolder, overridable with --root), then writes
// the leaf folder ID into folder_id and the relative path into folder_path.
//
// Scope: canonical YouTube clips only (source='youtube' AND id LIKE 'yt_%').
// The planner/stock bindings that share source='youtube' but carry
// 'planner:*' / raw Drive-file ids live under a different Drive root and
// are intentionally excluded — their folder fields belong to the stock
// pipeline, not to clip extraction.
//
// godlike/07 NO-FAKE-AVAILABILITY: resolution failures are counted and
// surfaced per-run (never silently skipped); the command is dry-run by
// default and requires --apply to write. It is idempotent: rows whose
// folder_id/folder_path already match the resolved location are left
// untouched and reported as already_aligned.
package backfill

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// maxFolderDepth guards the parent-chain walk against Drive cycles (a
// malformed shared-drive or orphaned folder must not loop forever).
const maxFolderDepth = 20

// clipFolderPathResolver resolves the physical Drive location of a file.
// It returns the leaf folder ID (the file's immediate parent) and the
// relative path from the configured root (e.g. "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ").
type clipFolderPathResolver func(ctx context.Context, driveFileID string) (folderID, folderPath string, err error)

// clipFolderPathStats aggregates a backfill run outcome.
type clipFolderPathStats struct {
	Matched        int // rows with a non-empty drive_file_id (candidates)
	Updated        int // rows whose folder_id/folder_path were changed by this run
	AlreadyAligned int // rows already matching the resolved physical location
	Failed         int // rows whose Drive resolution failed (counted, surfaced)
}

// clipFolderSource is the narrow media read this backfill depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL reader
// (pgmedia.BackfillReader) because media_assets is PostgreSQL-owned and the
// operational mirror holds no committed media rows. The port is engine-specific
// so the candidate scan cannot drift back onto that mirror.
type clipFolderSource interface {
	ListYouTubeClipFolderCandidates(ctx context.Context, limit int) ([]pgmedia.ClipFolderCandidate, error)
}

// resolvedClipAssetRow pairs a candidate with its Drive resolution outcome.
type resolvedClipAssetRow struct {
	row        pgmedia.ClipFolderCandidate
	folderID   string
	folderPath string
	err        error
}

// runBackfillClipFolderPath implements the `backfill-clip-folder-path`
// subcommand. Dry-run by default; pass --apply to write.
func RunBackfillClipFolderPath(args []string) error {
	fs := flag.NewFlagSet("backfill-clip-folder-path", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Apply updates (default: dry-run, prints what WOULD change)")
	limit := fs.Int("limit", 0, "Maximum number of rows to process; zero means all")
	root := fs.String("root", "", "Drive root folder ID for relative paths (default: config drive.normal_clips_source_folder)")
	concurrency := fs.Int("concurrency", 8, "Bounded parallelism for Drive metadata resolution")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	if *concurrency < 1 {
		return fmt.Errorf("--concurrency must be at least 1")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	rootCtx, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if rootCtx == nil || rootCtx.DB == nil || rootCtx.DB.DB == nil {
		return fmt.Errorf("database is required")
	}
	if rootCtx.Drive == nil || rootCtx.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}

	rootFolderID := strings.TrimSpace(*root)
	if rootFolderID == "" {
		rootFolderID = strings.TrimSpace(cfg.Drive.NormalClipsSourceFolder)
	}
	if rootFolderID == "" {
		return fmt.Errorf("drive root folder is not configured (set config drive.normal_clips_source_folder or pass --root)")
	}

	reader := rootCtx.Drive.Reader
	resolve := func(ctx context.Context, driveFileID string) (string, string, error) {
		return resolveClipFolderPath(ctx, reader.GetFileMeta, driveFileID, rootFolderID)
	}

	mutator, ok := rootCtx.CanonicalAssetWriter.(persistence.AssetMutator)
	if !ok || mutator == nil {
		return fmt.Errorf("canonical asset mutator is not available")
	}
	// MEDIA-SSOT: the candidate scan reads media_assets, so it resolves from the
	// PostgreSQL media SSOT; the writes already went through the canonical
	// mutator, so both halves now agree on one engine.
	source := pgmedia.NewBackfillReader(rootCtx.MediaPostgres)
	if source == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for clip-folder-path backfill")
	}
	stats, err := backfillClipFolderPath(ctx, source, resolve, *limit, *concurrency, *apply, mutator)
	if err != nil {
		return err
	}

	mode := "dry-run"
	if *apply {
		mode = "apply"
	}
	fmt.Printf("backfill-clip-folder-path: matched=%d updated=%d already_aligned=%d failed=%d mode=%s root=%s\n",
		stats.Matched, stats.Updated, stats.AlreadyAligned, stats.Failed, mode, rootFolderID)
	return nil
}

// backfillClipFolderPath resolves the physical Drive folder for every
// candidate row and (when apply is true) rewrites folder_id/folder_path.
// Resolution runs with bounded concurrency; DB writes are sequential.
// Idempotent: already-aligned rows are never rewritten.
func backfillClipFolderPath(
	ctx context.Context,
	src clipFolderSource,
	resolve clipFolderPathResolver,
	limit, concurrency int,
	apply bool,
	mutators ...persistence.AssetMutator,
) (clipFolderPathStats, error) {
	var stats clipFolderPathStats

	if src == nil {
		return stats, fmt.Errorf("backfill-clip-folder-path: media SSOT reader is not wired")
	}
	candidates, err := src.ListYouTubeClipFolderCandidates(ctx, limit)
	if err != nil {
		return stats, fmt.Errorf("backfill-clip-folder-path: query: %w", err)
	}
	stats.Matched = len(candidates)

	// Phase 1 — bounded-concurrency Drive resolution.
	results := make([]resolvedClipAssetRow, len(candidates))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, r := range candidates {
		i, r := i, r
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			folderID, folderPath, rErr := resolve(ctx, r.DriveFileID)
			results[i] = resolvedClipAssetRow{row: r, folderID: folderID, folderPath: folderPath, err: rErr}
		}()
	}
	wg.Wait()

	// Phase 2 — sequential idempotent writes.
	for _, res := range results {
		if res.err != nil {
			stats.Failed++
			fmt.Fprintf(os.Stderr, "  SKIP %s: %v\n", res.row.ID, res.err)
			continue
		}
		if res.folderID == res.row.FolderID && res.folderPath == res.row.FolderPath {
			stats.AlreadyAligned++
			continue
		}
		stats.Updated++
		if !apply {
			fmt.Printf("  WOULD-UPDATE %s: folder_id=%s folder_path=%q\n",
				res.row.ID, res.folderID, res.folderPath)
			continue
		}
		if len(mutators) > 0 && mutators[0] != nil {
			folderID, folderPath := res.folderID, res.folderPath
			if err := mutators[0].PatchAsset(ctx, persistence.AssetPatch{
				AssetID: res.row.ID, FolderID: &folderID, FolderPath: &folderPath,
			}); err != nil {
				return stats, fmt.Errorf("backfill-clip-folder-path: canonical update %s: %w", res.row.ID, err)
			}
		} else {
			return stats, fmt.Errorf("backfill-clip-folder-path: canonical asset mutator is required")
		}
	}
	return stats, nil
}

// resolveClipFolderPath walks the Drive parent chain of driveFileID up to
// rootFolderID and returns:
//   - leafFolderID: the file's immediate parent (the physical folder that
//     actually contains the clip);
//   - relativePath: the human-readable path from rootFolderID down to the
//     leaf folder, e.g. "Tom Holland/youtube_uncategorized/uVoMqnwEdBQ".
//
// getMeta is injected so tests can stub Drive responses without a network.
func resolveClipFolderPath(
	ctx context.Context,
	getMeta func(context.Context, string) (*drive.FileMeta, error),
	driveFileID, rootFolderID string,
) (leafFolderID, relativePath string, err error) {
	if strings.TrimSpace(driveFileID) == "" {
		return "", "", fmt.Errorf("drive_file_id is empty")
	}
	if strings.TrimSpace(rootFolderID) == "" {
		return "", "", fmt.Errorf("root folder id is empty")
	}

	meta, err := getMeta(ctx, driveFileID)
	if err != nil {
		return "", "", fmt.Errorf("file %s: %w", driveFileID, err)
	}
	if meta == nil || meta.Trashed {
		return "", "", fmt.Errorf("file %s missing or trashed", driveFileID)
	}
	if len(meta.Parents) == 0 {
		return "", "", fmt.Errorf("file %s has no parent folder", driveFileID)
	}

	leafFolderID = meta.Parents[0]
	current := leafFolderID
	var segments []string
	for depth := 0; depth < maxFolderDepth; depth++ {
		if current == rootFolderID {
			reverseStrings(segments)
			return leafFolderID, strings.Join(segments, "/"), nil
		}
		parent, err := getMeta(ctx, current)
		if err != nil {
			return "", "", fmt.Errorf("folder %s: %w", current, err)
		}
		if parent == nil || parent.Trashed {
			return "", "", fmt.Errorf("folder %s missing or trashed", current)
		}
		segments = append(segments, parent.Name)
		if len(parent.Parents) == 0 {
			return "", "", fmt.Errorf("parent chain of %s does not reach root %s (stopped at %s)", driveFileID, rootFolderID, current)
		}
		current = parent.Parents[0]
	}
	return "", "", fmt.Errorf("parent chain of %s exceeds %d hops (possible Drive cycle)", driveFileID, maxFolderDepth)
}

func reverseStrings(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
