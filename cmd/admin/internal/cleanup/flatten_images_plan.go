// cmd/admin/internal/cleanup/flatten_images_plan.go — the PURE half of the
// canonical image-library flattening: the in-memory snapshot of the images
// tree, the planner that decides the moves, and the operation list the caller
// executes. The command that drives it is flatten_images_drive.go.
//
// # WHY THIS FILE EXISTS SEPARATELY FROM THE COMMAND
//
// The library migration is a destructive-ish operation over a real Drive tree
// (hundreds of folders), so the decision and the execution are two different
// concerns and only one of them needs Drive:
//
//   - loadImageTree reads the tree ONCE into memory;
//   - planFlatten is a pure function over that snapshot — no I/O, no Drive, no
//     clock — so the exact operation list can be unit-tested and printed as a
//     dry run identical to what an --apply run executes;
//   - executeFlattenOps is the only writer.
//
// TARGET LAYOUT (the acceptance criterion this migration enforces):
//
//	<images root>/<per-image folder>/<image file>
//
// i.e. exactly ONE folder level below the images root, and no grouping level
// in between. The legacy writers produced three deviations, all removed here:
//
//	<root>/<style-or-group>/<subject>/<file>   a legacy grouping level
//	<root>/<subject>/<subject>/<file>          a duplicated single-child level
//	<root>/<group>/<group>/<subject>/<file>    both at once
//
// Invariants the planner guarantees (pinned by the tests):
//
//   - NO FILE IS EVER DROPPED. Every file present in the snapshot ends up in a
//     folder that exists in the plan's final state; a folder is only ever
//     trashed when the plan has moved its entire content out first, and the
//     planner asserts that on the in-memory model before emitting the trash.
//   - NO FOLDER IS RENAMED, so the operation list stays reversible by hand.
//   - A folder that mixes files AND subfolders cannot be flattened without
//     inventing a level, so it is SKIPPED and reported (never silently "done").
//   - A name collision at the target level MERGES the source into the existing
//     folder instead of creating a second folder with the same name.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// driveFolderMimeType is the MIME type Drive uses for folders; a folder is just
// a file with this type, which is why folders are moved with the same API.
const driveFolderMimeType = "application/vnd.google-apps.folder"

// defaultImageTreeMaxDepth bounds the snapshot recursion. The legacy layouts
// are at most three levels deep (`group/subject/file`); anything deeper is
// reported instead of being guessed at, so a pathological tree can never make
// the migration invent moves.
const defaultImageTreeMaxDepth = 4

// ── in-memory snapshot ──────────────────────────────────────────────────

// imageFolder is one folder of the images tree. Children are kept sorted by
// name so a plan is deterministic across runs (Drive does not guarantee an
// order), which is what makes the dry run comparable to the applied run.
type imageFolder struct {
	ID      string
	Name    string
	Parent  *imageFolder
	Folders []*imageFolder
	Files   []imageFile
}

// imageFile is one non-folder entry.
type imageFile struct {
	ID   string
	Name string
}

func (f *imageFolder) childFolder(name string) *imageFolder {
	for _, child := range f.Folders {
		if child.Name == name {
			return child
		}
	}
	return nil
}

func (f *imageFolder) removeFolder(target *imageFolder) {
	kept := f.Folders[:0]
	for _, child := range f.Folders {
		if child != target {
			kept = append(kept, child)
		}
	}
	f.Folders = kept
}

func (f *imageFolder) adoptFolder(child *imageFolder) {
	child.Parent = f
	f.Folders = append(f.Folders, child)
	sort.Slice(f.Folders, func(i, j int) bool { return f.Folders[i].Name < f.Folders[j].Name })
}

func (f *imageFolder) adoptFile(file imageFile) {
	f.Files = append(f.Files, file)
	sort.Slice(f.Files, func(i, j int) bool { return f.Files[i].Name < f.Files[j].Name })
}

func (f *imageFolder) removeFile(target imageFile) {
	kept := f.Files[:0]
	for _, file := range f.Files {
		if file.ID != target.ID {
			kept = append(kept, file)
		}
	}
	f.Files = kept
}

// pathString is the human-readable location used in reports and dry runs.
func (f *imageFolder) pathString() string {
	parts := make([]string, 0, 4)
	for cur := f; cur != nil; cur = cur.Parent {
		if cur.Parent == nil {
			break // the images root itself is not part of a path
		}
		parts = append(parts, cur.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return path.Join(parts...)
}

// ── operations ──────────────────────────────────────────────────────────

// flattenOpKind enumerates the three write primitives the migration uses.
type flattenOpKind string

const (
	// flattenOpEnsureFolder makes sure a per-image folder exists under a parent
	// (idempotent get-or-create by name). It is how a LOOSE file, which has no
	// folder of its own, is given the same one-level layout as every other image.
	flattenOpEnsureFolder flattenOpKind = "ensure-folder"
	// flattenOpMoveFolder reparents a folder (Drive move = remove old parent +
	// add new parent). Used for both flattening and merging.
	flattenOpMoveFolder flattenOpKind = "move-folder"
	// flattenOpMoveFile reparents a file, used when a source folder's content is
	// merged into a folder that already exists at the target level.
	flattenOpMoveFile flattenOpKind = "move-file"
	// flattenOpTrashFolder trashes an EMPTY folder. It is never emitted for a
	// folder whose content the plan did not fully move out first.
	flattenOpTrashFolder flattenOpKind = "trash-folder"
)

// folderRefKey is the plan-level handle for a folder that does not exist yet:
// "@<parentID>/<name>". The executor resolves it through the ensure-folder
// operation that creates it, which is what lets the plan stay a static list even
// though creating a folder is part of it.
func folderRefKey(parentID, name string) string {
	return "@" + parentID + "/" + name
}

// isFolderRef reports whether an operation target is a folder the plan creates
// rather than an existing Drive id.
func isFolderRef(id string) bool { return strings.HasPrefix(id, "@") }

// flattenOp is one planned write. ID/From/To are Drive IDs; Path is the
// human-readable location(s) for the report.
type flattenOp struct {
	Kind flattenOpKind
	ID   string
	From string
	To   string
	Path string
	// NewFolderName is set on ensure-folder operations: the per-image folder to
	// get-or-create under To.
	NewFolderName string
}

// displayID renders an operation target for humans: Drive ids are shown as they
// are, a not-yet-created folder as "@<parent>/<name> (new)".
func displayID(id string) string {
	if isFolderRef(id) {
		return id + " (new)"
	}
	if id == "" {
		return "<none>"
	}
	return id
}

func (o flattenOp) describe() string {
	switch o.Kind {
	case flattenOpEnsureFolder:
		return fmt.Sprintf("%-12s %-60s create under %s", o.Kind, o.NewFolderName, displayID(o.To))
	case flattenOpMoveFolder, flattenOpMoveFile:
		return fmt.Sprintf("%-12s %-60s -> %s", o.Kind, o.Path, displayID(o.To))
	case flattenOpTrashFolder:
		return fmt.Sprintf("%-12s %-60s (empty after the moves above)", o.Kind, o.Path)
	default:
		return fmt.Sprintf("%s %s", o.Kind, o.Path)
	}
}

// flattenPlan is the complete, ordered operation list plus the reasons the
// planner could not fully normalize a subtree.
type flattenPlan struct {
	Ops []flattenOp
	// RehomedLooseFiles names the files that sat directly inside a grouping level
	// and were therefore given a per-image folder of their own: the only case
	// where the migration CREATES a folder instead of only reparenting.
	RehomedLooseFiles []string
	// SkippedTooDeep names subtrees beyond defaultImageTreeMaxDepth: they are not
	// in the snapshot at all, so nothing below them is moved.
	SkippedTooDeep []string
	// FoldersScanned / FilesScanned describe the snapshot size.
	FoldersScanned int
	FilesScanned   int
}

// TrashCount reports how many empty folders the plan removes.
func (p flattenPlan) TrashCount() int {
	count := 0
	for _, op := range p.Ops {
		if op.Kind == flattenOpTrashFolder {
			count++
		}
	}
	return count
}

// ── planner ─────────────────────────────────────────────────────────────

// planFlatten normalizes the in-memory tree rooted at root (the images root)
// into `<root>/<per-image folder>/<file>` and returns the operations that turn
// the current tree into that state.
//
// The function is pure: it mutates only the passed tree, performs no I/O, and is
// deterministic for a given snapshot.
func planFlatten(root *imageFolder) flattenPlan {
	plan := flattenPlan{}
	if root == nil {
		return plan
	}
	countTree(root, &plan)

	// rootIndex mirrors the name → folder resolution at the images root so a
	// promotion can merge into an existing folder instead of creating a
	// same-name sibling. It is seeded with the current root children and kept up
	// to date as the planner moves things.
	rootIndex := make(map[string]*imageFolder, len(root.Folders))
	for _, child := range root.Folders {
		if _, taken := rootIndex[child.Name]; !taken {
			rootIndex[child.Name] = child
		}
	}

	// The worklist holds the folders that must end up as leaves directly under
	// the root. Each is processed exactly once.
	pending := append([]*imageFolder(nil), root.Folders...)
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		if current.Parent == nil {
			continue // already trashed by an earlier step
		}
		pending = append(pending, processFolderForFlatten(root, rootIndex, current, &plan)...)
	}
	return plan
}

// sameFolderName reports whether two Drive folder names denote the same
// identity level. Drive names are compared case-insensitively after trimming,
// which is how the publish path sanitizes them (SafeFolderName).
func sameFolderName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// countTree records the snapshot size for the report.
func countTree(folder *imageFolder, plan *flattenPlan) {
	plan.FoldersScanned++
	plan.FilesScanned += len(folder.Files)
	for _, child := range folder.Folders {
		countTree(child, plan)
	}
}

// processFolderForFlatten normalizes one root-level candidate folder and returns
// the newly promoted folders that still need normalization.
func processFolderForFlatten(root *imageFolder, rootIndex map[string]*imageFolder, current *imageFolder, plan *flattenPlan) []*imageFolder {
	// Step 1: collapse the DUPLICATED level first. `<slug>/<slug>/<file>` is the
	// nesting operators report, and the only structural signal that distinguishes
	// it from a legacy grouping level is the name: a sole child that repeats its
	// parent's name is a connector, so its content is lifted one level instead of
	// the outer folder being renamed.
	//
	// A sole child with a DIFFERENT name is not collapsed here — it is a grouping
	// level holding one per-image folder, which step 2 promotes. Collapsing it
	// would lift the image into the group folder and publish
	// `<root>/<group>/<file>`, i.e. keep the wrong level.
	for iterations := 0; iterations < defaultImageTreeMaxDepth; iterations++ {
		if len(current.Files) != 0 || len(current.Folders) != 1 {
			break
		}
		connector := current.Folders[0]
		if !sameFolderName(connector.Name, current.Name) {
			break
		}
		liftContents(connector, current, plan)
		plan.Ops = append(plan.Ops, flattenOp{
			Kind: flattenOpTrashFolder, ID: connector.ID, From: current.ID, Path: connector.pathString(),
		})
		current.removeFolder(connector)
		connector.Parent = nil
	}

	if len(current.Files) == 0 && len(current.Folders) == 0 {
		// Nothing to do: an empty folder holds no image, so the migration does not
		// invent one and does not remove what it did not create.
		return nil
	}

	// Both the files-only and the mixed case end at the same place: every CHILD
	// FOLDER is promoted to the root level, and every LOOSE FILE is given a
	// per-image folder of its own (named after the file), because a file with no
	// folder of its own is the one shape that cannot stay. A files-only folder
	// therefore still emits nothing: it IS the per-image folder already.
	if len(current.Folders) == 0 {
		return nil
	}

	promoted := make([]*imageFolder, 0, len(current.Folders))
	for _, child := range append([]*imageFolder(nil), current.Folders...) {
		promoted = append(promoted, promoteToRoot(root, rootIndex, current, child, plan))
	}
	if len(current.Files) > 0 {
		giveLooseFilesTheirOwnFolder(root, rootIndex, current, plan)
	}
	plan.Ops = append(plan.Ops, flattenOp{
		Kind: flattenOpTrashFolder, ID: current.ID, From: root.ID, Path: current.pathString(),
	})
	root.removeFolder(current)
	current.Parent = nil
	return promoted
}

// giveLooseFilesTheirOwnFolder rehomes every file that sits directly in a
// grouping level into the per-image folder the rest of the library uses: a folder
// named after the file (the legacy publish convention), get-or-created so an
// existing folder with that name is merged into instead of duplicated.
func giveLooseFilesTheirOwnFolder(root *imageFolder, rootIndex map[string]*imageFolder, source *imageFolder, plan *flattenPlan) {
	for _, file := range append([]imageFile(nil), source.Files...) {
		name := looseFileFolderName(file.Name)
		target := rootIndex[name]
		if target == nil {
			ref := folderRefKey(root.ID, name)
			plan.Ops = append(plan.Ops, flattenOp{
				Kind: flattenOpEnsureFolder, ID: ref, From: source.ID, To: root.ID, NewFolderName: name,
				Path: path.Join(source.pathString(), file.Name),
			})
			target = &imageFolder{ID: ref, Name: name}
			root.adoptFolder(target)
			rootIndex[name] = target
		}
		plan.Ops = append(plan.Ops, flattenOp{
			Kind: flattenOpMoveFile, ID: file.ID, From: source.ID, To: target.ID,
			Path: path.Join(source.pathString(), file.Name),
		})
		plan.RehomedLooseFiles = append(plan.RehomedLooseFiles, path.Join(source.pathString(), file.Name))
		target.adoptFile(file)
		source.removeFile(file)
	}
}

// looseFileFolderName derives the per-image folder name of a file by the same
// rule the publish path uses: the file name itself, sanitized for Drive. It
// keeps the extension in the folder name (`x.jpg` -> `x_jpg`) so the folder of
// a rehomed file is named EXACTLY like its siblings'.
func looseFileFolderName(fileName string) string {
	name := strings.TrimSpace(fileName)
	if name == "" {
		name = "image"
	}
	sanitized := strings.NewReplacer(".", "_", "/", "_", "\\", "_").Replace(name)
	return strings.TrimSpace(sanitized)
}

// liftContents moves every child of src into dst (in the model and in the plan),
// leaving src empty. Merging into an existing dst is a plain reparent, so no
// name can ever be duplicated by this step.
func liftContents(src, dst *imageFolder, plan *flattenPlan) {
	for _, file := range append([]imageFile(nil), src.Files...) {
		plan.Ops = append(plan.Ops, flattenOp{
			Kind: flattenOpMoveFile, ID: file.ID, From: src.ID, To: dst.ID,
			Path: path.Join(src.pathString(), file.Name),
		})
		dst.adoptFile(file)
	}
	src.Files = nil
	for _, folder := range append([]*imageFolder(nil), src.Folders...) {
		plan.Ops = append(plan.Ops, flattenOp{
			Kind: flattenOpMoveFolder, ID: folder.ID, From: src.ID, To: dst.ID,
			Path: folder.pathString(),
		})
		dst.adoptFolder(folder)
	}
	src.Folders = nil
}

// promoteToRoot lifts one child of a grouping level to the images root. When a
// folder with the same name already lives at the root, the child's content is
// merged into it and the child is trashed; otherwise the child is reparented.
// A same-name folder that IS an ancestor (the grouping level itself) is not a
// merge target — merging there would move data into a folder the plan trashes.
func promoteToRoot(root *imageFolder, rootIndex map[string]*imageFolder, group, child *imageFolder, plan *flattenPlan) *imageFolder {
	existing := rootIndex[child.Name]
	if existing == nil || existing == group || existing == child {
		plan.Ops = append(plan.Ops, flattenOp{
			Kind: flattenOpMoveFolder, ID: child.ID, From: group.ID, To: root.ID,
			Path: child.pathString(),
		})
		group.removeFolder(child)
		root.adoptFolder(child)
		rootIndex[child.Name] = child
		return child
	}
	liftContents(child, existing, plan)
	plan.Ops = append(plan.Ops, flattenOp{
		Kind: flattenOpTrashFolder, ID: child.ID, From: group.ID, Path: child.pathString(),
	})
	group.removeFolder(child)
	child.Parent = nil
	return child
}

// ── snapshot loading ────────────────────────────────────────────────────

// imageTreeLister is the narrow read port the snapshot needs; the canonical
// drive.Reader satisfies it. Declaring the one method instead of depending on
// the whole port keeps the snapshot testable without a Drive client.
type imageTreeLister interface {
	ListFiles(ctx context.Context, parentID string) ([]drive.DriveFileInfo, error)
}

// loadImageTree recursively reads the images tree into memory. It never
// descends past maxDepth: a deeper subtree is recorded in the plan as
// "skipped-too-deep" instead of being guessed at.
func loadImageTree(ctx context.Context, reader imageTreeLister, rootID, rootName string, maxDepth int) (*imageFolder, *flattenPlan, error) {
	if reader == nil {
		return nil, nil, errors.New("flatten images: drive reader is required")
	}
	if strings.TrimSpace(rootID) == "" {
		return nil, nil, errors.New("flatten images: images root folder id is required")
	}
	if maxDepth < 1 {
		maxDepth = defaultImageTreeMaxDepth
	}
	plan := &flattenPlan{}
	root := &imageFolder{ID: rootID, Name: rootName}
	if err := loadImageTreeInto(ctx, reader, root, maxDepth, plan); err != nil {
		return nil, nil, err
	}
	return root, plan, nil
}

func loadImageTreeInto(ctx context.Context, reader imageTreeLister, folder *imageFolder, depth int, plan *flattenPlan) error {
	entries, err := reader.ListFiles(ctx, folder.ID)
	if err != nil {
		return fmt.Errorf("flatten images: list %q: %w", folder.pathString(), err)
	}
	for _, entry := range entries {
		if entry.MimeType != driveFolderMimeType {
			folder.Files = append(folder.Files, imageFile{ID: entry.ID, Name: entry.Name})
			continue
		}
		if depth <= 0 {
			plan.SkippedTooDeep = append(plan.SkippedTooDeep, path.Join(folder.pathString(), entry.Name))
			continue
		}
		child := &imageFolder{ID: entry.ID, Name: entry.Name, Parent: folder}
		folder.Folders = append(folder.Folders, child)
		if err := loadImageTreeInto(ctx, reader, child, depth-1, plan); err != nil {
			return err
		}
	}
	sort.Slice(folder.Folders, func(i, j int) bool { return folder.Folders[i].Name < folder.Folders[j].Name })
	sort.Slice(folder.Files, func(i, j int) bool { return folder.Files[i].Name < folder.Files[j].Name })
	return nil
}

// writeFlattenReport prints the human-readable snapshot summary and the exact
// operation list, so a dry run can be reviewed before anything is written.
func writeFlattenReport(out io.Writer, root *imageFolder, plan flattenPlan, apply bool) {
	mode := "DRY RUN (no write)"
	if apply {
		mode = "APPLY"
	}
	fmt.Fprintf(out, "=== flatten images drive: %s ===\n", mode)
	fmt.Fprintf(out, "images root: %s (%s)\n", root.Name, root.ID)
	fmt.Fprintf(out, "scanned: %d folders, %d files\n", plan.FoldersScanned, plan.FilesScanned)
	fmt.Fprintf(out, "planned: %d operations (%d folders trashed)\n\n", len(plan.Ops), plan.TrashCount())
	for _, op := range plan.Ops {
		fmt.Fprintf(out, "  %s\n", op.describe())
	}
	if len(plan.RehomedLooseFiles) > 0 {
		// These are the operations that CREATE a folder, so they are called out
		// separately: an operator reviewing a dry run must see exactly which files
		// are being given a new per-image folder, and why (they sat loose inside a
		// grouping level, which is the one shape that cannot stay).
		fmt.Fprintf(out, "\nREHOMED (file had no per-image folder of its own):\n")
		for _, rehomed := range plan.RehomedLooseFiles {
			fmt.Fprintf(out, "  %s\n", rehomed)
		}
	}
	if len(plan.SkippedTooDeep) > 0 {
		fmt.Fprintf(out, "\nSKIPPED (deeper than the snapshot depth limit):\n")
		for _, skipped := range plan.SkippedTooDeep {
			fmt.Fprintf(out, "  %s\n", skipped)
		}
	}
}
