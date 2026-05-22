package script

import (
	"testing"

	"velox/go-master/internal/media/association"
	"velox/go-master/internal/media/models"
)

func TestResolveArtlistDisplayLinkPrefersDriveAndFolderFallback(t *testing.T) {
	if got := resolveArtlistDisplayLink(&models.MediaAsset{
		DriveLink: "https://drive.google.com/file/d/direct/view",
		ExternalURL: "https://artlist.io/clip/ignored",
	}); got != "https://drive.google.com/file/d/direct/view" {
		t.Fatalf("expected direct drive link, got %q", got)
	}

	if got := resolveArtlistDisplayLink(&models.MediaAsset{
		ExternalURL: "https://artlist.io/clip/ignored",
		FolderID:    "folder-123",
	}); got != "" {
		t.Fatalf("expected no folder drive fallback, got %q", got)
	}

	if got := resolveArtlistDisplayLink(&models.MediaAsset{
		ExternalURL: "https://artlist.io/clip/only",
	}); got != "" {
		t.Fatalf("expected no artlist url fallback, got %q", got)
	}
}

func TestResolveAssociatedDisplayLinkIgnoresFolderFallback(t *testing.T) {
	match := association.ScoredMatch{
		Title:      "Artlist Clip",
		FolderLink: "https://drive.google.com/drive/folders/drive-folder-id",
		Source:     "artlist_live_discovery",
	}

	if got := resolveAssociatedDisplayLink(match); got != "" {
		t.Fatalf("expected no folder fallback, got %q", got)
	}
}

