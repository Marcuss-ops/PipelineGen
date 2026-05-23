package script

import (
	"testing"

	"velox/go-master/internal/media/association"
	"velox/go-master/internal/media/models"
)

func TestResolveArtlistDisplayLinkPrefersDriveAndIgnoresFolderFallback(t *testing.T) {
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

func TestFilterSpecialNamesDropsSentencesAndGenericDescriptors(t *testing.T) {
	input := []string{
		"Federico Fellini",
		"Rimini",
		"Hanno ucciso un uccellino",
		"Old Key, Vintage Object",
		"Film History, Cinematic Legacy",
		"Federico Fellini",
	}

	got := filterSpecialNames(input, "")
	if len(got) != 2 {
		t.Fatalf("expected only the two real entities to remain, got %#v", got)
	}
	if got[0] != "Federico Fellini" || got[1] != "Rimini" {
		t.Fatalf("unexpected filtered names: %#v", got)
	}
}
