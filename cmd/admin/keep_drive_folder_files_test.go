package main

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

func TestFilterDriveNonFolders(t *testing.T) {
	t.Parallel()

	files := []drive.DriveFileInfo{
		{Name: "file1.wav", ID: "1", MimeType: "audio/wav"},
		{Name: "subfolder", ID: "2", MimeType: "application/vnd.google-apps.folder"},
		{Name: "file2.mp3", ID: "3", MimeType: "audio/mpeg"},
	}

	got := filterDriveNonFolders(files)

	if len(got) != 2 {
		t.Fatalf("expected 2 non-folders, got %d", len(got))
	}
	for _, f := range got {
		if f.MimeType == "application/vnd.google-apps.folder" {
			t.Errorf("filterDriveNonFolders must exclude folders, got %q", f.Name)
		}
	}
}

func TestFilterDriveNonFolders_Empty(t *testing.T) {
	t.Parallel()

	got := filterDriveNonFolders(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %d", len(got))
	}
}
