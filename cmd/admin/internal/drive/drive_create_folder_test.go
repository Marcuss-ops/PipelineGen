package drive

import (
	"context"
	"testing"

	platformdrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// planReader builds a stub Drive tree for the dry-run planner.
func planReader() *stubDriveReader {
	return &stubDriveReader{children: map[string][]platformdrive.DriveFileInfo{
		"parent": {
			{ID: "ex-id", Name: "existing", MimeType: folderMime},
			// A FILE that shares a name with a planned segment must NOT be
			// treated as an existing folder.
			{ID: "file-id", Name: "notafolder", MimeType: "text/plain"},
			{ID: "dup-1", Name: "dup", MimeType: folderMime},
			{ID: "dup-2", Name: "dup", MimeType: folderMime},
			{ID: "sp", Name: "My Folder", MimeType: folderMime},
		},
		"ex-id": {
			{ID: "deep-id", Name: "child", MimeType: folderMime},
		},
	}}
}

func stepByPath(t *testing.T, plan folderPlan, path string) folderPlanStep {
	t.Helper()
	for _, s := range plan.Segments {
		if s.Path == path {
			return s
		}
	}
	t.Fatalf("plan has no step with path %q (steps=%+v)", path, plan.Segments)
	return folderPlanStep{}
}

func TestPlanFolderPath_ExistingReused(t *testing.T) {
	plan, err := planFolderPath(context.Background(), planReader(), "parent", []string{"existing"})
	if err != nil {
		t.Fatalf("planFolderPath: %v", err)
	}
	if len(plan.Segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(plan.Segments))
	}
	s := plan.Segments[0]
	if s.Status != "exists" || s.FolderID != "ex-id" {
		t.Fatalf("step = %+v, want exists/ex-id", s)
	}
}

func TestPlanFolderPath_NestedExistingReused(t *testing.T) {
	plan, err := planFolderPath(context.Background(), planReader(), "parent", []string{"existing", "child"})
	if err != nil {
		t.Fatalf("planFolderPath: %v", err)
	}
	s := stepByPath(t, plan, "existing/child")
	if s.Status != "exists" || s.FolderID != "deep-id" {
		t.Fatalf("nested step = %+v, want exists/deep-id", s)
	}
}

func TestPlanFolderPath_MissingThenCreate(t *testing.T) {
	plan, err := planFolderPath(context.Background(), planReader(), "parent", []string{"missing", "below"})
	if err != nil {
		t.Fatalf("planFolderPath: %v", err)
	}
	for _, s := range plan.Segments {
		if s.Status != "create" {
			t.Fatalf("step %q = %q, want create", s.Path, s.Status)
		}
	}
}

func TestPlanFolderPath_FileWithSameNameIsNotAFolder(t *testing.T) {
	plan, err := planFolderPath(context.Background(), planReader(), "parent", []string{"notafolder"})
	if err != nil {
		t.Fatalf("planFolderPath: %v", err)
	}
	if plan.Segments[0].Status != "create" {
		t.Fatalf("step = %+v, want create (a file is not a folder)", plan.Segments[0])
	}
}

func TestPlanFolderPath_AmbiguousIsReported(t *testing.T) {
	plan, err := planFolderPath(context.Background(), planReader(), "parent", []string{"dup"})
	if err != nil {
		t.Fatalf("planFolderPath: %v", err)
	}
	s := plan.Segments[0]
	if s.Status != "ambiguous" || s.Count != 2 {
		t.Fatalf("step = %+v, want ambiguous count=2", s)
	}
}

func TestPlanFolderPath_CanonicalisesSegments(t *testing.T) {
	// "My Folder" is already canonical; matching is exact on the sanitised
	// name, so the existing folder is reused.
	plan, err := planFolderPath(context.Background(), planReader(), "parent", []string{"My Folder"})
	if err != nil {
		t.Fatalf("planFolderPath: %v", err)
	}
	if plan.Segments[0].Status != "exists" || plan.Segments[0].FolderID != "sp" {
		t.Fatalf("step = %+v, want exists/sp", plan.Segments[0])
	}
}

func TestPlanFolderPath_ListErrorPropagates(t *testing.T) {
	reader := &stubDriveReader{errs: map[string]error{"parent": context.Canceled}}
	if _, err := planFolderPath(context.Background(), reader, "parent", []string{"x"}); err == nil {
		t.Fatal("planFolderPath: expected error when ListFiles fails, got nil")
	}
}

func TestFindChildFolderByName_CountsOnlyFolders(t *testing.T) {
	id, count, err := findChildFolderByName(context.Background(), planReader(), "parent", "dup")
	if err != nil {
		t.Fatalf("findChildFolderByName: %v", err)
	}
	if count != 2 || id == "" {
		t.Fatalf("id/count = %q/%d, want non-empty/2", id, count)
	}
}
