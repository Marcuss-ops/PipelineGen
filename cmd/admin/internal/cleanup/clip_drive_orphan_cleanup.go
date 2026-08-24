// cmd/admin/clip_drive_orphan_cleanup.go — trash-first orphan cleanup for
// canonical YouTube clips.
//
// Lists (dry-run) or trashes (--apply, reversible) the TRUE orphan clip
// files on Drive: clip-like `yt_*.mp4` files that no media_assets row
// references — neither via drive_file_id NOR via drive_link/download_link.
//
// Trash-first safety model:
//   - Default is dry-run: prints the per-file list and does NOT touch Drive.
//   - --apply moves each still-unreferenced orphan to Drive trash (reversible).
//     It NEVER permanently deletes.
//   - Untracked uploads (files referenced by drive_link/download_link but not
//     drive_file_id) are the CURRENT canonical files — they are listed and
//     explicitly EXCLUDED from trashing.
//   - Each orphan is re-verified against the DB immediately before trashing:
//     a concurrent drive_file_id/link update between the audit and the apply
//     loop skips the file instead of trashing a now-referenced file.
package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func RunClipDriveOrphanCleanup(args []string) error {
	fs := flag.NewFlagSet("clip-drive-orphan-cleanup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "Drive root folder ID to walk (default: config drive.normal_clips_source_folder)")
	limit := fs.Int("limit", 0, "Maximum number of clip rows to audit; zero means all")
	apply := fs.Bool("apply", false, "Move true orphans to Drive trash (default: dry-run list only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	rootCtx, _, rootCleanup, err := app.InitComposition(cfg, log)
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
	if *apply && rootCtx.Drive.Admin == nil {
		return fmt.Errorf("drive admin port is required for --apply (trash)")
	}

	rootFolderID := strings.TrimSpace(*root)
	if rootFolderID == "" {
		rootFolderID = strings.TrimSpace(cfg.Drive.NormalClipsSourceFolder)
	}
	if rootFolderID == "" {
		return fmt.Errorf("drive root folder is not configured (set config drive.normal_clips_source_folder or pass --root)")
	}

	report, err := clipDriveAudit(ctx, rootCtx.DB.DB, rootCtx.Drive.Reader.ListFiles, rootFolderID, *limit)
	if err != nil {
		return err
	}

	fmt.Printf("clip-drive-orphan-cleanup: %d orphan clip file(s), %d untracked upload(s) excluded\n",
		len(report.OrphanFiles), len(report.UntrackedUploads))

	for _, o := range report.OrphanFiles {
		fmt.Printf("  orphan: %s (%s) @ %s\n", o.Name, o.FileID, o.FolderPath)
	}
	for _, u := range report.UntrackedUploads {
		fmt.Printf("  EXCLUDED (referenced by link): %s (%s)\n", u.Name, u.FileID)
	}

	if len(report.OrphanFiles) == 0 {
		fmt.Println("no true orphans to trash")
		return nil
	}

	if !*apply {
		fmt.Printf("\nDRY RUN: %d file(s) would be trashed. Re-run with --apply to trash (reversible).\n", len(report.OrphanFiles))
		return nil
	}

	// Fresh re-verification set, so a concurrent drive_file_id/link update
	// between the audit and this point skips the file instead of trashing a
	// now-referenced file.
	identity, err := loadAllClipDriveFileIDs(ctx, rootCtx.DB.DB)
	if err != nil {
		return err
	}
	linkIDs, err := loadAllClipLinkFileIDs(ctx, rootCtx.DB.DB)
	if err != nil {
		return err
	}
	referenced := make(map[string]struct{}, len(identity)+len(linkIDs))
	for id := range identity {
		referenced[id] = struct{}{}
	}
	for id := range linkIDs {
		referenced[id] = struct{}{}
	}

	trashed := 0
	skipped := 0
	failed := 0
	for _, o := range selectTrueOrphans(report.OrphanFiles, referenced) {
		if err := rootCtx.Drive.Admin.TrashFile(ctx, o.FileID); err != nil {
			fmt.Printf("  FAILED to trash %s (%s): %v\n", o.Name, o.FileID, err)
			failed++
			continue
		}
		fmt.Printf("  trashed: %s (%s)\n", o.Name, o.FileID)
		trashed++
	}
	skipped = len(report.OrphanFiles) - trashed - failed

	fmt.Printf("\nclip-drive-orphan-cleanup complete: trashed=%d skipped(now-referenced)=%d failed=%d\n", trashed, skipped, failed)
	return nil
}

// selectTrueOrphans filters the audit's orphan list against a freshly
// computed referenced set (drive_file_id ∪ drive_link/download_link file IDs).
// Any orphan that became referenced after the audit is skipped; the result is
// the set of files that are safe to trash. Untracked uploads are never
// candidates — they are a separate list, not part of the orphan list.
func selectTrueOrphans(orphans []clipDriveOrphan, referenced map[string]struct{}) []clipDriveOrphan {
	out := make([]clipDriveOrphan, 0, len(orphans))
	for _, o := range orphans {
		if _, nowRef := referenced[o.FileID]; nowRef {
			continue
		}
		out = append(out, o)
	}
	return out
}
