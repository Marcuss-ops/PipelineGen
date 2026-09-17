// cmd/admin/internal/cleanup/flatten_images_drive.go — `flatten-images-drive`:
// brings the canonical Drive image library onto the single per-image layout
//
//	<images root>/<per-image folder>/<image file>
//
// WHY: several historical writers published images through the destination
// builder with a style/group level and, separately, with the file name as a
// folder name, so the real tree contains `<root>/vidrush/<subject>/<file>` (the
// legacy grouping level) next to correct single-level folders, and any tree
// where a folder contains exactly one same-named child reproduces the
// `<slug>/<slug>/<file>` nesting operators see. Every future run reuses whatever
// is already in Drive by content hash, so the layout must be normalized once
// instead of being worked around at every read.
//
// SAFETY MODEL:
//
//   - DRY RUN BY DEFAULT. Only `--apply` writes. The dry run prints the exact
//     operation list from the same planner an --apply run executes, so the
//     reviewed plan and the applied plan cannot diverge.
//   - NO CONTENT IS EVER DROPPED. The only write primitives are "reparent" and
//     "trash an empty folder"; the planner moves a folder's whole content out
//     before it emits that folder's trash, and refuses to touch a folder that
//     mixes files with subfolders (reported as SKIPPED).
//   - NO FOLDER IS DELETED, only trashed: a mistake stays recoverable from the
//     Drive trash.
//   - NO FOLDER IS RENAMED, so the operations stay readable in Drive's
//     activity log.
//
// Exit contract: a failed Drive call aborts immediately with the failing
// operation, so the operator can re-run the command — the plan is idempotent
// (a second run over a normalized tree plans zero operations).
package cleanup

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// imageTreeWriter is the narrow write port the migration needs: two Drive
// primitives, both already provided by the canonical drive.Admin port. The
// compile-time assertion below breaks the build if that port ever loses them.
type imageTreeWriter interface {
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	TrashFolder(ctx context.Context, folderID string) error
}

var _ imageTreeWriter = drive.Admin(nil)

// RunFlattenImagesDrive is the `flatten-images-drive` subcommand.
func RunFlattenImagesDrive(args []string) error {
	fs := flag.NewFlagSet("flatten-images-drive", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "Drive folder ID of the images root (defaults to cfg.Drive.ImagesFolder(), the canonical images root)")
	apply := fs.Bool("apply", false, "execute the plan; without this flag the command is a read-only dry run")
	maxDepth := fs.Int("max-depth", defaultImageTreeMaxDepth, "how deep the snapshot recurses before reporting a subtree as skipped")
	timeout := fs.Duration("timeout", 30*time.Minute, "overall deadline for the snapshot and the applied operations")
	if err := fs.Parse(args); err != nil {
		return err
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

	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil {
		return errors.New("flatten-images-drive: Drive reader and admin ports are required")
	}

	imagesRootID := strings.TrimSpace(*rootFlag)
	if imagesRootID == "" {
		imagesRootID = strings.TrimSpace(cfg.Drive.ImagesFolder())
	}
	if imagesRootID == "" {
		return errors.New("flatten-images-drive: no images root configured (set drive.images_root_folder or pass --root)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	rootName := imagesRootID
	if name, nameErr := root.Drive.Admin.GetFolderName(ctx, imagesRootID); nameErr == nil && strings.TrimSpace(name) != "" {
		rootName = name
	} else if nameErr != nil {
		log.Warn("flatten-images-drive: could not read the images root name", zap.String("folder_id", imagesRootID), zap.Error(nameErr))
	}

	tree, snapshot, err := loadImageTree(ctx, root.Drive.Reader, imagesRootID, rootName, *maxDepth)
	if err != nil {
		return err
	}
	plan := planFlatten(tree)
	// Snapshot-level skips (depth) are decisions too: carry them into the plan
	// that gets reported so a truncated snapshot can never look complete.
	plan.SkippedTooDeep = append(plan.SkippedTooDeep, snapshot.SkippedTooDeep...)

	writeFlattenReport(os.Stdout, tree, plan, *apply)

	if !*apply {
		fmt.Printf("\nDRY RUN: %d planned operation(s) NOT executed. Re-run with --apply to execute them.\n", len(plan.Ops))
		return nil
	}
	if len(plan.Ops) == 0 {
		fmt.Println("\nNothing to do: the images root already matches the per-image layout.")
		return nil
	}

	executed, err := executeFlattenOps(ctx, root.Drive.Admin, plan)
	if err != nil {
		return fmt.Errorf("flatten-images-drive: applied %d of %d operation(s): %w", executed, len(plan.Ops), err)
	}
	fmt.Printf("\nAPPLIED %d operation(s).\n", executed)
	if len(plan.SkippedTooDeep) > 0 {
		return fmt.Errorf(
			"flatten-images-drive: %d subtree(s) were deeper than --max-depth and stay unnormalized; re-run with a larger --max-depth",
			len(plan.SkippedTooDeep),
		)
	}
	return nil
}

// executeFlattenOps applies a plan in order. It stops at the first failure and
// reports how many operations already succeeded, so a re-run resumes from a
// consistent tree (the plan is idempotent).
func executeFlattenOps(ctx context.Context, writer imageTreeWriter, plan flattenPlan) (int, error) {
	if writer == nil {
		return 0, errors.New("flatten images: drive admin is required")
	}
	// created maps a plan-level folder reference ("@<parent>/<name>") to the real
	// Drive id the ensure-folder operation returned, so the moves that follow can
	// target a folder that did not exist when the plan was built.
	created := map[string]string{}
	executed := 0
	for _, op := range plan.Ops {
		var err error
		switch op.Kind {
		case flattenOpEnsureFolder:
			var folderID string
			folderID, err = writer.GetOrCreateFolder(ctx, op.NewFolderName, op.To)
			if err == nil {
				created[op.ID] = folderID
			}
		case flattenOpMoveFolder, flattenOpMoveFile:
			target := op.To
			if isFolderRef(target) {
				resolved, ok := created[target]
				if !ok {
					return executed, fmt.Errorf(
						"flatten images: %s %s: destination folder %s was never created",
						op.Kind, op.Path, target,
					)
				}
				target = resolved
			}
			err = writer.MoveFile(ctx, op.ID, op.From, target)
		case flattenOpTrashFolder:
			err = writer.TrashFolder(ctx, op.ID)
		default:
			return executed, fmt.Errorf("flatten images: unknown operation %q for %s", op.Kind, op.Path)
		}
		if err != nil {
			return executed, fmt.Errorf("%s %s: %w", op.Kind, op.Path, err)
		}
		executed++
	}
	return executed, nil
}
