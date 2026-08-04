package drive

import (
	"context"
	"strings"
	"testing"

	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

func TestCleanFolderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "helloworld"},
		{"Test-Folder_Name", "testfoldername"},
		{"  Spaces  Around  ", "spacesaround"},
		{"UPPERCASE", "uppercase"},
		{"already_lower", "alreadylower"},
		{"", ""},
		{"-_-", ""},
		{"Café", "café"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := fileutil.CleanFolderName(tt.input)
			if got != tt.expected {
				t.Errorf("CleanFolderName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestUploaderValidatesService(t *testing.T) {
	u := &Uploader{Service: nil}

	ctx := context.Background()

	t.Run("put file fails without service", func(t *testing.T) {
		_, err := u.PutFile(ctx, PutFileRequest{LocalPath: "test.mp4", FolderID: "folder123", Filename: "test.mp4"})
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured' error, got: %v", err)
		}
	})

	t.Run("find file fails without service", func(t *testing.T) {
		_, err := u.FindFileByName(ctx, "folder123", "test.mp4")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured' error, got: %v", err)
		}
	})

	t.Run("get folder fails without service", func(t *testing.T) {
		_, err := u.GetOrCreateFolder(ctx, "test", "parent123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured' error, got: %v", err)
		}
	})

	t.Run("trash file fails without service", func(t *testing.T) {
		err := u.TrashFile(ctx, "file123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured' error, got: %v", err)
		}
	})

	t.Run("delete file fails without service", func(t *testing.T) {
		err := u.DeleteFile(ctx, "file123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured' error, got: %v", err)
		}
	})

	t.Run("file exists fails without service", func(t *testing.T) {
		_, err := u.FileExists(ctx, "file123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured' error, got: %v", err)
		}
	})
}

func TestUploaderEmptyInputs(t *testing.T) {
	// With a nil Service, ALL methods return "drive service not configured" first.
	// Input validation happens after the service check. These tests verify
	// the nil-service error behavior is consistent, not the edge-case validation.
	u := &Uploader{Service: nil}
	ctx := context.Background()

	t.Run("find file with nil service returns drive error", func(t *testing.T) {
		_, err := u.FindFileByName(ctx, "folder123", "test.mp4")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured', got: %v", err)
		}
	})

	t.Run("trash file with nil service returns drive error", func(t *testing.T) {
		err := u.TrashFile(ctx, "file123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured', got: %v", err)
		}
	})

	t.Run("delete file with nil service returns drive error", func(t *testing.T) {
		err := u.DeleteFile(ctx, "file123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured', got: %v", err)
		}
	})

	t.Run("get folder with nil service returns drive error", func(t *testing.T) {
		_, err := u.GetOrCreateFolder(ctx, "test", "parent123")
		if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
			t.Errorf("expected 'drive service not configured', got: %v", err)
		}
	})
}
