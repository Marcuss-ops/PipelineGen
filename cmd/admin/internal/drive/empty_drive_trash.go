package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

// trashPreviewLimit caps how many trashed entries the dry-run preview
// prints; the total count is always reported in full.
const trashPreviewLimit = 50

// RunEmptyDriveTrash implements the `drive-empty-trash` admin
// subcommand: it previews the contents of Drive's trash and, ONLY with
// the explicit --apply flag, permanently purges it through
// FileLifecycle.EmptyTrash (Drive files.emptyTrash).
//
// Fail-closed by design: the default invocation is a read-only dry run
// that lists what a purge would remove. There is no undo for a real
// purge, so the destructive path requires the operator to type the flag
// — an accidental `admin drive-empty-trash` can never delete anything.
func RunEmptyDriveTrash(args []string) error {
	apply := false
	for _, raw := range args {
		switch strings.TrimSpace(raw) {
		case "":
			continue
		case "--apply":
			apply = true
		default:
			return fmt.Errorf("drive-empty-trash: unknown flag %q (supported: --apply)", raw)
		}
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()

	if root == nil || root.Drive == nil || root.Drive.Lifecycle == nil {
		return fmt.Errorf("Drive lifecycle is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Preview first — always. Even the destructive path reports what it
	// is about to remove, so the operator's shell history is a record.
	items, err := root.Drive.Lifecycle.ListTrashed(ctx, trashPreviewLimit)
	if err != nil {
		return fmt.Errorf("list Drive trash: %w", err)
	}

	fmt.Printf("Drive trash: %d item(s)\n", len(items))
	for i, it := range items {
		if i >= trashPreviewLimit {
			break
		}
		fmt.Printf("  %s (%s) [%s]\n", it.Name, it.ID, it.MimeType)
	}
	if len(items) >= trashPreviewLimit {
		fmt.Printf("  ... (preview capped at %d; the purge removes all trashed items)\n", trashPreviewLimit)
	}

	if !apply {
		fmt.Println("DRY-RUN: no changes made. Re-run with --apply to permanently empty the trash.")
		return nil
	}

	if len(items) == 0 {
		fmt.Println("Drive trash is already empty; nothing to purge.")
		return nil
	}

	fmt.Printf("PERMANENT: emptying Drive trash (%d item(s) previewed, purge is not recoverable)...\n", len(items))
	if err := root.Drive.Lifecycle.EmptyTrash(ctx); err != nil {
		return fmt.Errorf("empty Drive trash: %w", err)
	}
	fmt.Println("Drive trash emptied.")
	return nil
}
