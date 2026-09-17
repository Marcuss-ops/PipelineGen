package cleanup

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ── test tree builders ──────────────────────────────────────────────────

// testIDSeq mints unique folder ids inside a test binary. Drive ids are unique
// per folder, and two same-named folders in different branches (a legacy group
// and its promoted twin) MUST be distinguishable or a replay would confuse them.
var testIDSeq int

func nextTestID(name string) string {
	testIDSeq++
	return fmt.Sprintf("id:%s#%d", name, testIDSeq)
}

// mkFolder builds an in-memory folder node with a unique id.
func mkFolder(name string, children ...any) *imageFolder {
	folder := &imageFolder{ID: nextTestID(name), Name: name}
	for _, child := range children {
		switch typed := child.(type) {
		case *imageFolder:
			typed.Parent = folder
			folder.Folders = append(folder.Folders, typed)
		case string:
			folder.Files = append(folder.Files, imageFile{ID: "fid:" + nextTestID(typed), Name: typed})
		default:
			panic(fmt.Sprintf("unsupported child %T", child))
		}
	}
	sortFolders(folder)
	return folder
}

func sortFolders(folder *imageFolder) {
	sort.Slice(folder.Folders, func(i, j int) bool { return folder.Folders[i].Name < folder.Folders[j].Name })
	sort.Slice(folder.Files, func(i, j int) bool { return folder.Files[i].Name < folder.Files[j].Name })
}

func mkRoot(children ...any) *imageFolder {
	return mkFolder("Immagini", children...)
}

// cloneTree deep-copies a tree preserving ids, so a test can plan on one copy
// and replay the plan on an untouched one.
func cloneTree(source *imageFolder) *imageFolder {
	clone := &imageFolder{ID: source.ID, Name: source.Name}
	clone.Files = append(clone.Files, source.Files...)
	for _, child := range source.Folders {
		copied := cloneTree(child)
		copied.Parent = clone
		clone.Folders = append(clone.Folders, copied)
	}
	return clone
}

// ── assertions helpers ──────────────────────────────────────────────────

// finalLayout returns "folder/file" for every file reachable from the root.
// It IS the acceptance criterion: one folder level under the images root, then
// the file.
func finalLayout(root *imageFolder) []string {
	var out []string
	var walk func(f *imageFolder, prefix string)
	walk = func(f *imageFolder, prefix string) {
		for _, file := range f.Files {
			out = append(out, path.Join(prefix, file.Name))
		}
		for _, child := range f.Folders {
			walk(child, path.Join(prefix, child.Name))
		}
	}
	walk(root, "")
	sort.Strings(out)
	return out
}

// fileIdentities returns the path-INDEPENDENT identity of every file, so a plan
// can be proven not to lose or duplicate a file even though it rewrites every
// path on purpose.
func fileIdentities(root *imageFolder) []string {
	var out []string
	var walk func(f *imageFolder)
	walk = func(f *imageFolder) {
		for _, file := range f.Files {
			out = append(out, file.ID+"="+file.Name)
		}
		for _, child := range f.Folders {
			walk(child)
		}
	}
	walk(root)
	sort.Strings(out)
	return out
}

// folderNames indexes id → name for every folder that existed BEFORE planning.
// The plan prints Drive ids (an operator needs them); tests read names instead.
func folderNames(root *imageFolder) map[string]string {
	names := map[string]string{root.ID: "<root>"}
	var walk func(f *imageFolder)
	walk = func(f *imageFolder) {
		for _, child := range f.Folders {
			names[child.ID] = child.Name
			walk(child)
		}
	}
	walk(root)
	return names
}

// describePlan renders the plan as "<kind> <path> <fromName>-><toName>" so an
// expectation stays readable and id-format independent. A folder the plan
// creates is rendered as "@<name>" (its real Drive id does not exist yet).
func describePlan(plan flattenPlan, names map[string]string) []string {
	if len(plan.Ops) == 0 {
		return nil
	}
	label := func(id string) string {
		if name, ok := names[id]; ok {
			return name
		}
		if isFolderRef(id) {
			if idx := strings.LastIndex(id, "/"); idx >= 0 {
				return "@" + id[idx+1:]
			}
			return "@?"
		}
		return id
	}
	out := make([]string, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		out = append(out, fmt.Sprintf("%s %s %s->%s", op.Kind, op.Path, label(op.From), label(op.To)))
	}
	return out
}

// planAndExpect plans a copy of root, asserts the exact operation sequence and
// replays the plan on the untouched original. It returns the plan AND the tree
// the planner mutated, so a caller can assert both the operations and the
// resulting layout without re-planning.
func planAndExpect(t *testing.T, original *imageFolder, wantOps []string) (flattenPlan, *imageFolder) {
	t.Helper()
	names := folderNames(original)
	planned := cloneTree(original)
	plan := planFlatten(planned)
	if got := describePlan(plan, names); !reflect.DeepEqual(got, wantOps) {
		t.Fatalf("plan =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(wantOps, "\n  "))
	}
	assertPlanIsExecutable(t, original, plan)
	return plan, planned
}

// ── plan replay (the executable-plan proof) ─────────────────────────────

// modelState replays a plan against an in-memory tree. Content is derived from
// the parent map, not from the node slices, so a stale slice cannot hide a
// missing child.
type modelState struct {
	folders map[string]*imageFolder
	files   map[string]imageFile
	parent  map[string]string // fileID -> folderID
}

func newModelState(root *imageFolder) *modelState {
	state := &modelState{
		folders: map[string]*imageFolder{},
		files:   map[string]imageFile{},
		parent:  map[string]string{},
	}
	var walk func(f *imageFolder)
	walk = func(f *imageFolder) {
		state.folders[f.ID] = f
		for _, file := range f.Files {
			state.files[file.ID] = file
			state.parent[file.ID] = f.ID
		}
		for _, child := range f.Folders {
			walk(child)
		}
	}
	walk(root)
	return state
}

func (s *modelState) children(folderID string) ([]string, []string) {
	var folders, files []string
	for id, folder := range s.folders {
		if folder.Parent != nil && folder.Parent.ID == folderID {
			folders = append(folders, id)
		}
	}
	for id, parentID := range s.parent {
		if parentID == folderID {
			files = append(files, id)
		}
	}
	sort.Strings(folders)
	sort.Strings(files)
	return folders, files
}

func (s *modelState) apply(t *testing.T, op flattenOp) {
	t.Helper()
	switch op.Kind {
	case flattenOpEnsureFolder:
		if _, exists := s.folders[op.ID]; exists {
			t.Fatalf("ensure-folder %s: folder reference %q already exists", op.Path, op.ID)
		}
		parent, ok := s.folders[op.To]
		if !ok {
			t.Fatalf("ensure-folder %s: parent %q does not exist or was already trashed", op.Path, op.To)
		}
		if !isFolderRef(op.ID) {
			t.Fatalf("ensure-folder %s: id %q must be a folder reference, not a Drive id the plan cannot know", op.Path, op.ID)
		}
		s.folders[op.ID] = &imageFolder{ID: op.ID, Name: op.NewFolderName, Parent: parent}
	case flattenOpMoveFolder:
		folder, ok := s.folders[op.ID]
		if !ok {
			t.Fatalf("move-folder %s: source folder already trashed or unknown", op.Path)
		}
		if folder.Parent == nil {
			t.Fatalf("move-folder %s: source folder has no parent to move out of", op.Path)
		}
		if folder.Parent.ID != op.From {
			t.Fatalf("move-folder %s: source folder is in %q, the plan says %q", op.Path, folder.Parent.ID, op.From)
		}
		target, ok := s.folders[op.To]
		if !ok {
			t.Fatalf("move-folder %s: destination %q does not exist or was already trashed", op.Path, op.To)
		}
		folder.Parent = target
	case flattenOpMoveFile:
		current, ok := s.parent[op.ID]
		if !ok {
			t.Fatalf("move-file %s: file unknown", op.Path)
		}
		if current != op.From {
			t.Fatalf("move-file %s: file is in %q, the plan says %q", op.Path, current, op.From)
		}
		if _, ok := s.folders[op.To]; !ok {
			t.Fatalf("move-file %s: destination %q does not exist or was already trashed", op.Path, op.To)
		}
		s.parent[op.ID] = op.To
	case flattenOpTrashFolder:
		if _, ok := s.folders[op.ID]; !ok {
			t.Fatalf("trash-folder %s: folder already trashed or unknown", op.Path)
		}
		folders, files := s.children(op.ID)
		if len(folders) != 0 || len(files) != 0 {
			t.Fatalf("trash-folder %s: folder is NOT empty (folders=%v files=%v) — the plan would drop content", op.Path, folders, files)
		}
		delete(s.folders, op.ID)
	default:
		t.Fatalf("unknown operation kind %q", op.Kind)
	}
}

// assertPlanIsExecutable replays the plan on an untouched copy of the tree and
// proves three things at once: every step is applicable in order, no content is
// dropped, and the executed end state is exactly the target layout.
func assertPlanIsExecutable(t *testing.T, original *imageFolder, plan flattenPlan) {
	t.Helper()
	state := newModelState(original)
	before := len(state.files)
	for _, op := range plan.Ops {
		state.apply(t, op)
	}
	if len(state.files) != before {
		t.Fatalf("replaying the plan changed the file count: %d -> %d", before, len(state.files))
	}
	rootID := original.ID
	for id, folder := range state.folders {
		if id == rootID {
			continue
		}
		if folder.Parent == nil || folder.Parent.ID != rootID {
			t.Fatalf("folder %q survived outside the root level (parent=%v)", folder.Name, folder.Parent)
		}
		nested, _ := state.children(id)
		if len(nested) != 0 {
			t.Fatalf("folder %q still contains nested folders after the migration: %v", folder.Name, nested)
		}
	}
}

// ── planner tests ───────────────────────────────────────────────────────

// TestPlanFlattenRemovesLegacyGroupingLevel is the primary acceptance test for
// the real Drive tree: `<root>/vidrush/<per-image folder>/<file>` must become
// `<root>/<per-image folder>/<file>`, with every file preserved and the emptied
// group folder trashed.
func TestPlanFlattenRemovesLegacyGroupingLevel(t *testing.T) {
	root := mkRoot(
		mkFolder("vidrush",
			mkFolder("michael-jordan_jpg", "michael-jordan.jpg"),
			mkFolder("2c5be396_jpg", "2c5be396.jpg"),
		),
		mkFolder("already-flat_jpg", "already-flat.jpg"),
	)

	plan, tree := planAndExpect(t, root, []string{
		"move-folder vidrush/2c5be396_jpg vidrush-><root>",
		"move-folder vidrush/michael-jordan_jpg vidrush-><root>",
		"trash-folder vidrush <root>->",
	})

	if tree.childFolder("vidrush") != nil {
		t.Fatalf("legacy grouping level survived the plan: %#v", tree.Folders)
	}
	want := []string{
		"2c5be396_jpg/2c5be396.jpg",
		"already-flat_jpg/already-flat.jpg",
		"michael-jordan_jpg/michael-jordan.jpg",
	}
	sort.Strings(want)
	if got := finalLayout(tree); !reflect.DeepEqual(got, want) {
		t.Fatalf("final layout = %#v, want %#v", got, want)
	}
	// The already-correct folder must not be rewritten by the plan.
	for _, op := range describePlan(plan, folderNames(root)) {
		if strings.Contains(op, "already-flat") {
			t.Fatalf("an already-correct folder was rewritten: %s", op)
		}
	}
	if len(plan.RehomedLooseFiles) != 0 {
		t.Fatalf("no file should need rehoming here: %#v", plan.RehomedLooseFiles)
	}
	if len(plan.SkippedTooDeep) != 0 {
		t.Fatalf("nothing should be out of depth here: %#v", plan.SkippedTooDeep)
	}
}

// TestPlanFlattenCollapsesDuplicatedSingleChildLevel pins the
// `<slug>/<slug>/<file>` nesting operators report: the connector is emptied and
// trashed instead of the outer folder being renamed, so the plan stays
// reversible by hand.
func TestPlanFlattenCollapsesDuplicatedSingleChildLevel(t *testing.T) {
	root := mkRoot(
		mkFolder("scottie-pippen", mkFolder("scottie-pippen", "scottie-pippen.jpg")),
	)

	_, tree := planAndExpect(t, root, []string{
		"move-file scottie-pippen/scottie-pippen/scottie-pippen.jpg scottie-pippen->scottie-pippen",
		"trash-folder scottie-pippen/scottie-pippen scottie-pippen->",
	})

	if got := finalLayout(tree); !reflect.DeepEqual(got, []string{"scottie-pippen/scottie-pippen.jpg"}) {
		t.Fatalf("final layout = %#v, want [scottie-pippen/scottie-pippen.jpg]", got)
	}
}

// TestPlanFlattenHandlesGroupAndDuplicatedLevelTogether covers the combined
// legacy shape, which is also the case a naive implementation collapses the
// wrong way: the group level whose sole child repeats the group name.
func TestPlanFlattenHandlesGroupAndDuplicatedLevelTogether(t *testing.T) {
	root := mkRoot(
		mkFolder("vidrush", mkFolder("vidrush", mkFolder("subject_jpg", "subject.jpg"))),
	)

	_, tree := planAndExpect(t, root, []string{
		"move-folder vidrush/vidrush/subject_jpg vidrush->vidrush",
		"trash-folder vidrush/vidrush vidrush->",
		"move-folder vidrush/subject_jpg vidrush-><root>",
		"trash-folder vidrush <root>->",
	})

	if got := finalLayout(tree); !reflect.DeepEqual(got, []string{"subject_jpg/subject.jpg"}) {
		t.Fatalf("final layout = %#v, want [subject_jpg/subject.jpg]", got)
	}
}

// TestPlanFlattenMergesNameCollisionAtRoot pins collision handling: a promoted
// folder whose name already exists at the root merges INTO the existing folder
// (no second folder with the same name, no lost file).
func TestPlanFlattenMergesNameCollisionAtRoot(t *testing.T) {
	root := mkRoot(
		mkFolder("legacy-group", mkFolder("michael-jordan_jpg", "second.jpg")),
		mkFolder("michael-jordan_jpg", "first.jpg"),
	)

	_, tree := planAndExpect(t, root, []string{
		"move-file legacy-group/michael-jordan_jpg/second.jpg michael-jordan_jpg->michael-jordan_jpg",
		"trash-folder legacy-group/michael-jordan_jpg legacy-group->",
		"trash-folder legacy-group <root>->",
	})

	if got := finalLayout(tree); !reflect.DeepEqual(got, []string{
		"michael-jordan_jpg/first.jpg",
		"michael-jordan_jpg/second.jpg",
	}) {
		t.Fatalf("final layout = %#v", got)
	}
	names := 0
	for _, child := range tree.Folders {
		if child.Name == "michael-jordan_jpg" {
			names++
		}
	}
	if names != 1 {
		t.Fatalf("expected exactly one folder named michael-jordan_jpg at the root, got %d", names)
	}
}

// TestPlanFlattenRehomesLooseFilesBesidePromotedFolders covers the real shape
// found on Drive (`vidrush/` holds 353 per-image folders AND one loose file):
// the child folders are promoted and the loose file is given the per-image
// folder its siblings already have, so the grouping level can be emptied and
// trashed instead of being left half-migrated.
func TestPlanFlattenRehomesLooseFilesBesidePromotedFolders(t *testing.T) {
	root := mkRoot(
		mkFolder("legacy-group", mkFolder("nested", "nested.jpg"), "loose.jpg"),
	)

	plan, tree := planAndExpect(t, root, []string{
		"move-folder legacy-group/nested legacy-group-><root>",
		"ensure-folder legacy-group/loose.jpg legacy-group-><root>",
		"move-file legacy-group/loose.jpg legacy-group->@loose_jpg",
		"trash-folder legacy-group <root>->",
	})

	if !reflect.DeepEqual(plan.RehomedLooseFiles, []string{"legacy-group/loose.jpg"}) {
		t.Fatalf("RehomedLooseFiles = %#v, want [legacy-group/loose.jpg]", plan.RehomedLooseFiles)
	}
	// The loose file keeps the naming convention of its siblings: the per-image
	// folder is named after the file, extension included.
	if got := finalLayout(tree); !reflect.DeepEqual(got, []string{"loose_jpg/loose.jpg", "nested/nested.jpg"}) {
		t.Fatalf("final layout = %#v", got)
	}
}

// TestLooseFileFolderNameMirrorsTheFolderConvention pins the folder name a
// rehomed file receives, which is what makes its folder indistinguishable from
// the ones the publish path creates.
func TestLooseFileFolderNameMirrorsTheFolderConvention(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2c5be396a34aa655.jpg", "2c5be396a34aa655_jpg"},
		{"File_Scottie Pippen 5-2-22.jpg", "File_Scottie Pippen 5-2-22_jpg"},
		{"shot.png", "shot_png"},
		{"  ", "image"},
	}
	for _, tc := range cases {
		if got := looseFileFolderName(tc.in); got != tc.want {
			t.Errorf("looseFileFolderName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPlanFlattenIsIdempotent pins the re-run contract: a tree that already
// matches the per-image layout plans nothing, so running the migration twice (or
// after a partial failure) is safe.
func TestPlanFlattenIsIdempotent(t *testing.T) {
	root := mkRoot(
		mkFolder("a_jpg", "a.jpg"),
		mkFolder("b_jpg", "b.jpg", "b-alt.jpg"),
	)
	before := finalLayout(root)

	plan, tree := planAndExpect(t, root, nil)

	if got := finalLayout(tree); !reflect.DeepEqual(got, before) {
		t.Fatalf("normalized tree changed: %#v -> %#v", before, got)
	}
	if plan.TrashCount() != 0 {
		t.Fatalf("a normalized tree must trash nothing, got %d", plan.TrashCount())
	}
}

// TestPlanFlattenNeverLosesAFile is the safety invariant over a tree that
// exercises every branch at once.
func TestPlanFlattenNeverLosesAFile(t *testing.T) {
	root := mkRoot(
		mkFolder("vidrush",
			mkFolder("a_jpg", "a.jpg"),
			mkFolder("b_jpg", mkFolder("b_jpg", "b.jpg")),
		),
		mkFolder("group2", mkFolder("a_jpg", "a-second.jpg"), mkFolder("c_jpg", "c.jpg")),
		mkFolder("flat_jpg", "flat.jpg"),
		mkFolder("mixed", mkFolder("nested", "nested.jpg"), "loose.jpg"),
		mkFolder("empty-leaf"),
	)

	before := fileIdentities(root)
	planned := cloneTree(root)
	plan := planFlatten(planned)
	after := fileIdentities(planned)

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("files lost or duplicated by planning:\n before %#v\n after  %#v", before, after)
	}
	assertPlanIsExecutable(t, root, plan)

	// Every planned trash must remove a folder the plan itself emptied, and no
	// file may be moved out of an already-trashed folder (checked in the replay).
	if got := finalLayout(planned); !reflect.DeepEqual(got, []string{
		"a_jpg/a-second.jpg",
		"a_jpg/a.jpg",
		"b_jpg/b.jpg",
		"c_jpg/c.jpg",
		"flat_jpg/flat.jpg",
		"loose_jpg/loose.jpg",
		"nested/nested.jpg",
	}) {
		t.Fatalf("final layout = %#v", got)
	}
	if !reflect.DeepEqual(plan.RehomedLooseFiles, []string{"mixed/loose.jpg"}) {
		t.Fatalf("RehomedLooseFiles = %#v, want [mixed/loose.jpg]", plan.RehomedLooseFiles)
	}
	// The empty pre-existing folder holds no content and was not created by the
	// planner, so it must survive: the migration only removes folders it emptied.
	if planned.childFolder("empty-leaf") == nil {
		t.Fatal("an empty pre-existing folder must not be removed by the migration")
	}
}

// ── snapshot tests ──────────────────────────────────────────────────────

type stubTreeLister struct {
	tree map[string][]drive.DriveFileInfo
	err  map[string]error
	seen []string
}

func (s *stubTreeLister) ListFiles(_ context.Context, parentID string) ([]drive.DriveFileInfo, error) {
	s.seen = append(s.seen, parentID)
	if err := s.err[parentID]; err != nil {
		return nil, err
	}
	return s.tree[parentID], nil
}

func folderEntry(id, name string) drive.DriveFileInfo {
	return drive.DriveFileInfo{ID: id, Name: name, MimeType: driveFolderMimeType}
}

func fileEntry(id, name string) drive.DriveFileInfo {
	return drive.DriveFileInfo{ID: id, Name: name, MimeType: "image/jpeg"}
}

// TestLoadImageTreeSeparatesFilesFromFoldersAndBoundsDepth pins the snapshot:
// folders and files are classified by MIME type and a subtree deeper than the
// limit is reported instead of being silently truncated.
func TestLoadImageTreeSeparatesFilesFromFoldersAndBoundsDepth(t *testing.T) {
	lister := &stubTreeLister{
		tree: map[string][]drive.DriveFileInfo{
			"root": {fileEntry("f1", "root.jpg"), folderEntry("g", "group")},
			"g":    {folderEntry("a", "a_jpg"), fileEntry("f2", "g-loose.jpg")},
			"a":    {folderEntry("deep", "deeper")},
			"deep": {fileEntry("f3", "deep.jpg")},
		},
	}

	tree, plan, err := loadImageTree(context.Background(), lister, "root", "Immagini", 2)
	if err != nil {
		t.Fatalf("loadImageTree: %v", err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Name != "root.jpg" {
		t.Fatalf("root files = %#v", tree.Files)
	}
	group := tree.childFolder("group")
	if group == nil {
		t.Fatal("group folder missing from the snapshot")
	}
	if len(group.Files) != 1 || group.Files[0].Name != "g-loose.jpg" {
		t.Fatalf("group files = %#v", group.Files)
	}
	if !reflect.DeepEqual(plan.SkippedTooDeep, []string{"group/a_jpg/deeper"}) {
		t.Fatalf("SkippedTooDeep = %#v, want [group/a_jpg/deeper]", plan.SkippedTooDeep)
	}
}

// TestLoadImageTreeFailsClosedOnListError pins that a partial snapshot is an
// error, never a plan built on a half-read tree.
func TestLoadImageTreeFailsClosedOnListError(t *testing.T) {
	lister := &stubTreeLister{
		tree: map[string][]drive.DriveFileInfo{"root": {folderEntry("g", "group")}},
		err:  map[string]error{"g": errors.New("drive 500")},
	}
	if _, _, err := loadImageTree(context.Background(), lister, "root", "Immagini", 3); err == nil {
		t.Fatal("expected an error when a subtree cannot be read")
	}
	if _, _, err := loadImageTree(context.Background(), nil, "root", "Immagini", 3); err == nil {
		t.Fatal("expected an error when the reader is missing")
	}
	if _, _, err := loadImageTree(context.Background(), lister, "  ", "Immagini", 3); err == nil {
		t.Fatal("expected an error when the images root id is empty")
	}
}

// ── executor + report tests ─────────────────────────────────────────────

type recordingWriter struct {
	moves  []string
	trash  []string
	create []string
	failOn string
}

func (w *recordingWriter) GetOrCreateFolder(_ context.Context, name, parentID string) (string, error) {
	if w.failOn == name {
		return "", errors.New("drive: create failed")
	}
	w.create = append(w.create, name+"@"+parentID)
	return "new:" + name, nil
}

func (w *recordingWriter) MoveFile(_ context.Context, fileID, from, to string) error {
	if w.failOn == fileID {
		return errors.New("drive: move failed")
	}
	w.moves = append(w.moves, fileID+":"+from+"->"+to)
	return nil
}

func (w *recordingWriter) TrashFolder(_ context.Context, folderID string) error {
	if w.failOn == folderID {
		return errors.New("drive: trash failed")
	}
	w.trash = append(w.trash, folderID)
	return nil
}

// TestExecuteFlattenOpsAppliesThePlanInOrder pins the executor: the same plan
// reviewed in the dry run is what gets applied, in order, through the two Drive
// primitives.
func TestExecuteFlattenOpsAppliesThePlanInOrder(t *testing.T) {
	group := mkFolder("vidrush", mkFolder("a_jpg", "a.jpg"))
	root := mkRoot(group)
	plan := planFlatten(cloneTree(root))
	writer := &recordingWriter{}

	executed, err := executeFlattenOps(context.Background(), writer, plan)
	if err != nil {
		t.Fatalf("executeFlattenOps: %v", err)
	}
	if executed != len(plan.Ops) {
		t.Fatalf("executed %d of %d operations", executed, len(plan.Ops))
	}
	// The promoted folder is the group's only child; the group itself is trashed.
	wantMove := group.Folders[0].ID + ":" + group.ID + "->" + root.ID
	if !reflect.DeepEqual(writer.moves, []string{wantMove}) {
		t.Fatalf("moves = %#v, want [%s]", writer.moves, wantMove)
	}
	if !reflect.DeepEqual(writer.trash, []string{group.ID}) {
		t.Fatalf("trash = %#v, want [%s]", writer.trash, group.ID)
	}
}

// TestExecuteFlattenOpsStopsAtTheFirstFailure pins the resume contract: the
// number of applied operations is reported so a re-run can pick up from a
// consistent tree, and nothing is trashed after a failure.
func TestExecuteFlattenOpsStopsAtTheFirstFailure(t *testing.T) {
	root := mkRoot(
		mkFolder("vidrush", mkFolder("a_jpg", "a.jpg"), mkFolder("b_jpg", "b.jpg")),
	)
	plan := planFlatten(cloneTree(root))
	// Fail on the LAST promotion, so at least one operation has to succeed and the
	// group trash (which comes after every promotion) must not run.
	second := ""
	for _, op := range plan.Ops {
		if op.Kind == flattenOpMoveFolder {
			second = op.ID
		}
	}
	if second == "" {
		t.Fatalf("expected promotions in the plan: %#v", plan.Ops)
	}
	writer := &recordingWriter{failOn: second}

	executed, err := executeFlattenOps(context.Background(), writer, plan)
	if err == nil {
		t.Fatal("expected the failing operation to abort the run")
	}
	if executed == 0 || executed >= len(plan.Ops) {
		t.Fatalf("executed = %d, want a partial count between 1 and %d", executed, len(plan.Ops)-1)
	}
	if len(writer.trash) != 0 {
		t.Fatalf("the group folder must not be trashed after a failure, got %#v", writer.trash)
	}
	if _, err := executeFlattenOps(context.Background(), nil, plan); err == nil {
		t.Fatal("expected an error when no writer is wired")
	}
}

// TestFlattenReportRendersThePlan pins the dry-run output the operator reviews:
// it must state the mode, the root, the operation count and the skips, so a dry
// run and an apply run cannot be confused.
func TestFlattenReportRendersThePlan(t *testing.T) {
	root := mkRoot(
		mkFolder("vidrush", mkFolder("a_jpg", "a.jpg")),
		mkFolder("mixed", "loose.jpg", mkFolder("n", "n.jpg")),
	)
	plan := planFlatten(cloneTree(root))

	var dryRun strings.Builder
	writeFlattenReport(&dryRun, root, plan, false)
	out := dryRun.String()
	for _, want := range []string{
		"=== flatten images drive: DRY RUN (no write) ===",
		"images root: Immagini",
		"move-folder",
		"trash-folder",
		"REHOMED (file had no per-image folder of its own):",
		"mixed/loose.jpg",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run report is missing %q:\n%s", want, out)
		}
	}

	var applied strings.Builder
	writeFlattenReport(&applied, root, plan, true)
	if !strings.Contains(applied.String(), "=== flatten images drive: APPLY ===") {
		t.Fatalf("apply report does not announce the write mode:\n%s", applied.String())
	}
}

// TestFlattenPlanSummariesAreExact keeps the report arithmetic honest.
func TestFlattenPlanSummariesAreExact(t *testing.T) {
	root := mkRoot(
		mkFolder("vidrush", mkFolder("a_jpg", "a.jpg"), mkFolder("b_jpg", "b.jpg")),
	)
	plan := planFlatten(cloneTree(root))
	if plan.FoldersScanned == 0 || plan.FilesScanned != 2 {
		t.Fatalf("snapshot counters = %d folders / %d files", plan.FoldersScanned, plan.FilesScanned)
	}
	if plan.TrashCount() != 1 {
		t.Fatalf("TrashCount = %d, want 1", plan.TrashCount())
	}
	for _, op := range plan.Ops {
		if op.describe() == "" {
			t.Fatalf("operation %v has no description", op)
		}
	}
	if got := describePlan(flattenPlan{Ops: []flattenOp{{Kind: "unknown", Path: "p"}}}, map[string]string{}); len(got) != 1 {
		t.Fatalf("describePlan = %#v", got)
	}
}
