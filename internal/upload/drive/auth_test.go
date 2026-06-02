package drive

import (
	"testing"

	"velox/go-master/internal/config"
)

func TestResolveArtlistRootFolderIDDelegatesToArtlistFolder(t *testing.T) {
	// DriveConfig.ArtlistFolder() returns MediaRootFolder if set, else ArtlistRootFolder
	cfg := &config.Config{
		Drive: config.DriveConfig{
			ArtlistRootFolder: "artlist-root",
		},
	}
	if got := ResolveArtlistRootFolderID(cfg); got != "artlist-root" {
		t.Fatalf("expected artlist-root, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDPrefersMediaRoot(t *testing.T) {
	cfg := &config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder:  "media-root",
			ArtlistRootFolder: "artlist-root",
		},
	}
	if got := ResolveArtlistRootFolderID(cfg); got != "media-root" {
		t.Fatalf("expected media-root, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDFallsBackToHarvester(t *testing.T) {
	cfg := &config.Config{
		Harvester: config.HarvesterConfig{
			DriveFolderID: "harvester-root",
		},
	}
	if got := ResolveArtlistRootFolderID(cfg); got != "harvester-root" {
		t.Fatalf("expected harvester-root, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDEmptyWhenUnconfigured(t *testing.T) {
	cfg := &config.Config{}
	if got := ResolveArtlistRootFolderID(cfg); got != "" {
		t.Fatalf("expected empty when unconfigured, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDNilConfig(t *testing.T) {
	if got := ResolveArtlistRootFolderID(nil); got != "" {
		t.Fatalf("expected empty for nil config, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDPrefersDriveOverHarvester(t *testing.T) {
	// Drive takes priority over Harvester fallback
	cfg := &config.Config{
		Drive: config.DriveConfig{
			ArtlistRootFolder: "artlist-root",
		},
		Harvester: config.HarvesterConfig{
			DriveFolderID: "harvester-root",
		},
	}
	if got := ResolveArtlistRootFolderID(cfg); got != "artlist-root" {
		t.Fatalf("expected artlist-root (drive priority), got %q", got)
	}
}

func TestResolveArtlistRootFolderIDHarvesterTrimsWhitespace(t *testing.T) {
	cfg := &config.Config{
		Harvester: config.HarvesterConfig{
			DriveFolderID: "  harvester-root  ",
		},
	}
	if got := ResolveArtlistRootFolderID(cfg); got != "harvester-root" {
		t.Fatalf("expected harvester-root with trimmed whitespace, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDHarvesterUnsetWhenDriveEmpty(t *testing.T) {
	cfg := &config.Config{
		Harvester: config.HarvesterConfig{
			DriveFolderID: "",
		},
	}
	if got := ResolveArtlistRootFolderID(cfg); got != "" {
		t.Fatalf("expected empty when both drive and harvester are empty, got %q", got)
	}
}

func TestResolveArtlistRootFolderIDFullPriorityChain(t *testing.T) {
	// Full chain: MediaRootFolder > ArtlistRootFolder > Harvester.DriveFolderID > ""
	tests := []struct {
		name     string
		cfg      *config.Config
		expected string
	}{
		{
			name: "media root wins over all",
			cfg: &config.Config{
				Drive: config.DriveConfig{
					MediaRootFolder:  "media-root",
					ArtlistRootFolder: "artlist-root",
				},
				Harvester: config.HarvesterConfig{DriveFolderID: "harvester-root"},
			},
			expected: "media-root",
		},
		{
			name: "artlist root wins over harvester",
			cfg: &config.Config{
				Drive: config.DriveConfig{
					ArtlistRootFolder: "artlist-root",
				},
				Harvester: config.HarvesterConfig{DriveFolderID: "harvester-root"},
			},
			expected: "artlist-root",
		},
		{
			name: "harvester fallback when drive empty",
			cfg: &config.Config{
				Harvester: config.HarvesterConfig{DriveFolderID: "harvester-root"},
			},
			expected: "harvester-root",
		},
		{
			name: "all empty returns empty",
			cfg:  &config.Config{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveArtlistRootFolderID(tt.cfg); got != tt.expected {
				t.Errorf("ResolveArtlistRootFolderID() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDriveConfigResolveFolder(t *testing.T) {
	t.Run("returns MediaRootFolder when set", func(t *testing.T) {
		d := config.DriveConfig{MediaRootFolder: "media-root", ImagesRootFolder: "images-root"}
		if got := d.ResolveFolder(d.ImagesRootFolder); got != "media-root" {
			t.Fatalf("expected media-root, got %q", got)
		}
	})
	t.Run("returns specific root when no MediaRoot", func(t *testing.T) {
		d := config.DriveConfig{ImagesRootFolder: "images-root"}
		if got := d.ResolveFolder(d.ImagesRootFolder); got != "images-root" {
			t.Fatalf("expected images-root, got %q", got)
		}
	})
	t.Run("convenience methods use ResolveFolder", func(t *testing.T) {
		d := config.DriveConfig{
			MediaRootFolder:     "media-root",
			StockRootFolder:     "stock",
			ClipsRootFolder:     "clips",
			VoiceoverRootFolder: "voiceover",
			ArtlistRootFolder:   "artlist",
			BooksRootFolder:     "books",
			ScriptsRootFolder:   "scripts",
			ImagesRootFolder:    "images",
			VideoAIRootFolder:   "video-ai",
			CopertineRootFolder: "copertine",
			SoundEffectsRootFolder: "sfx",
			OutroRootFolder:     "outro",
		}
		for name, got := range map[string]string{
			"StockFolder":        d.StockFolder(),
			"ClipsFolder":        d.ClipsFolder(),
			"VoiceoverFolder":    d.VoiceoverFolder(),
			"ArtlistFolder":      d.ArtlistFolder(),
			"BooksFolder":        d.BooksFolder(),
			"ScriptsFolder":      d.ScriptsFolder(),
			"ImagesFolder":       d.ImagesFolder(),
			"VideoAIFolder":      d.VideoAIFolder(),
			"CopertineFolder":    d.CopertineFolder(),
			"SoundEffectsFolder": d.SoundEffectsFolder(),
			"OutroFolder":        d.OutroFolder(),
		} {
			if got != "media-root" {
				t.Fatalf("%s expected media-root with MediaRoot set, got %q", name, got)
			}
		}
	})
}
