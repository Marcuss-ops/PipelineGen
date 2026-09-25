package drive

import (
	"testing"

	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ── rawListFlag ──────────────────────────────────────────────────────

func TestRawListFlag_DoesNotSplitOnCommas(t *testing.T) {
	var f rawListFlag
	if err := f.Set("id1=Name, with comma"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := f.Set("id2=Other"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(f) != 2 {
		t.Fatalf("rawListFlag = %v, want 2 entries", f)
	}
	if f[0] != "id1=Name, with comma" {
		t.Fatalf("entry = %q, want the raw pair (comma preserved)", f[0])
	}
}

// ── parseDriveRenameArgs ─────────────────────────────────────────────

func TestParseDriveRenameArgs_SingleFile(t *testing.T) {
	targets, err := parseDriveRenameArgs([]string{" f1 "}, nil, "", "", " New Name ")
	if err != nil {
		t.Fatalf("parseDriveRenameArgs: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want 1", targets)
	}
	if targets[0].ID != "f1" || targets[0].NewName != "New Name" {
		t.Fatalf("target = %+v, want trimmed f1/New Name", targets[0])
	}
}

func TestParseDriveRenameArgs_ByName(t *testing.T) {
	targets, err := parseDriveRenameArgs(nil, nil, " parent ", " old ", "New")
	if err != nil {
		t.Fatalf("parseDriveRenameArgs: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want 1", targets)
	}
	got := targets[0]
	if got.ID != "" || got.From != "parent" || got.CurrentName != "old" || got.NewName != "New" {
		t.Fatalf("target = %+v, want unresolved name target", got)
	}
}

func TestParseDriveRenameArgs_Pairs(t *testing.T) {
	targets, err := parseDriveRenameArgs(nil, []string{"a=Alpha", " b = Beta "}, "", "", "")
	if err != nil {
		t.Fatalf("parseDriveRenameArgs: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %+v, want 2", targets)
	}
	if targets[0].ID != "a" || targets[0].NewName != "Alpha" {
		t.Fatalf("target[0] = %+v", targets[0])
	}
	if targets[1].ID != "b" || targets[1].NewName != "Beta" {
		t.Fatalf("target[1] = %+v", targets[1])
	}
}

func TestParseDriveRenameArgs_PairWithSpacesInNameIsPreserved(t *testing.T) {
	targets, err := parseDriveRenameArgs(nil, []string{"a=Canelo Álvarez Jr"}, "", "", "")
	if err != nil {
		t.Fatalf("parseDriveRenameArgs: %v", err)
	}
	if targets[0].NewName != "Canelo Álvarez Jr" {
		t.Fatalf("NewName = %q, want the name verbatim", targets[0].NewName)
	}
}

func TestParseDriveRenameArgs_Errors(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		pairs   []string
		from    string
		old     string
		newName string
	}{
		{name: "nothing to rename"},
		{name: "new-name without target", newName: "x"},
		{name: "file without new-name", files: []string{"f1"}},
		{name: "name without from", old: "a", newName: "x"},
		{name: "from without name", from: "p", newName: "x"},
		{name: "multiple files with one new-name", files: []string{"f1", "f2"}, newName: "x"},
		{name: "blank file id", files: []string{"  "}, newName: "x"},
		{name: "pairs mixed with file", files: []string{"f1"}, pairs: []string{"a=A"}, newName: "x"},
		{name: "pairs mixed with new-name", pairs: []string{"a=A"}, newName: "x"},
		{name: "pair without separator", pairs: []string{"aAlpha"}},
		{name: "pair with empty name", pairs: []string{"a="}},
		{name: "pair with empty id", pairs: []string{"=A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseDriveRenameArgs(tt.files, tt.pairs, tt.from, tt.old, tt.newName); err == nil {
				t.Fatalf("expected error, got %+v", got)
			}
		})
	}
}

// ── decideRename ─────────────────────────────────────────────────────

func siblings() []platformdrive.DriveFileInfo {
	return []platformdrive.DriveFileInfo{
		{ID: "self", Name: "Old Name"},
		{ID: "other", Name: "Taken"},
	}
}

func TestDecideRename_CleanRenameIsPlanned(t *testing.T) {
	d := decideRename("Old Name", false, siblings(), "self", "New Name", false)
	if d.Status != "planned" || d.Collisions != 0 {
		t.Fatalf("decision = %+v, want planned with no collisions", d)
	}
}

func TestDecideRename_AlreadyNamedIsSkipped(t *testing.T) {
	d := decideRename("Same", false, siblings(), "self", "Same", false)
	if d.Status != "skipped" {
		t.Fatalf("decision = %+v, want skipped", d)
	}
}

func TestDecideRename_TrashedIsSkipped(t *testing.T) {
	d := decideRename("Old Name", true, siblings(), "self", "New Name", false)
	if d.Status != "skipped" {
		t.Fatalf("decision = %+v, want skipped (trashed)", d)
	}
}

func TestDecideRename_EmptyNewNameIsError(t *testing.T) {
	d := decideRename("Old Name", false, siblings(), "self", "", false)
	if d.Status != "error" {
		t.Fatalf("decision = %+v, want error", d)
	}
}

func TestDecideRename_CollisionFailsClosed(t *testing.T) {
	d := decideRename("Old Name", false, siblings(), "self", "Taken", false)
	if d.Status != "collision" || d.Collisions != 1 {
		t.Fatalf("decision = %+v, want collision count=1", d)
	}
}

func TestDecideRename_CollisionAllowedWithOptIn(t *testing.T) {
	d := decideRename("Old Name", false, siblings(), "self", "Taken", true)
	if d.Status != "planned" || d.Collisions != 1 {
		t.Fatalf("decision = %+v, want planned with collision reported", d)
	}
}

func TestDecideRename_SelfNameIsNotACollision(t *testing.T) {
	// The target's own entry must never count as its own sibling.
	d := decideRename("Old Name", false, siblings(), "self", "Old Name", false)
	if d.Collisions != 0 {
		t.Fatalf("decision = %+v, want no collision from self", d)
	}
}

// ── countSiblingNameCollisions ───────────────────────────────────────

func TestCountSiblingNameCollisions(t *testing.T) {
	list := []platformdrive.DriveFileInfo{
		{ID: "self", Name: "Taken"},
		{ID: "a", Name: "Taken"},
		{ID: "b", Name: "Taken"},
		{ID: "c", Name: "Other"},
	}
	if got := countSiblingNameCollisions(list, "self", "Taken"); got != 2 {
		t.Fatalf("collisions = %d, want 2", got)
	}
	if got := countSiblingNameCollisions(list, "self", "Missing"); got != 0 {
		t.Fatalf("collisions = %d, want 0", got)
	}
}
