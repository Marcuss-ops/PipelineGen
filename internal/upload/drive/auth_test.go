package drive

import (
	"testing"

	"velox/go-master/internal/config"
)

func TestResolveArtlistRootFolderIDUsesConfiguredHarvesterRoot(t *testing.T) {
	cfg := &config.Config{
		Harvester: config.HarvesterConfig{
			DriveFolderID: "harvester-root",
		},
	}

	if got := ResolveArtlistRootFolderID(cfg); got != "harvester-root" {
		t.Fatalf("expected harvester root, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDFallsBackToDocumentedDefault(t *testing.T) {
	cfg := &config.Config{}

	if got := ResolveArtlistRootFolderID(cfg); got != defaultArtlistRootFolderID {
		t.Fatalf("expected documented fallback root, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDPrefersMediaRoot(t *testing.T) {
	cfg := &config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder: "media-root",
			StockRootFolder: "stock-root",
		},
	}

	if got := ResolveArtlistRootFolderID(cfg); got != "media-root" {
		t.Fatalf("expected media root, got %q", got)
	}
}
