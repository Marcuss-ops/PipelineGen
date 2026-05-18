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
		Drive: config.DriveConfig{
			ClipsRootFolder: "clips-root",
			StockRootFolder: "stock-root",
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
