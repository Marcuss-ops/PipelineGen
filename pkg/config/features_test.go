package config

import (
	"testing"
)

func TestFeaturesConfigDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	
	if !cfg.Features.ArtlistEnabled {
		t.Error("ArtlistEnabled should default to true")
	}
	if !cfg.Features.YouTubeEnabled {
		t.Error("YouTubeEnabled should default to true")
	}
	if !cfg.Features.DriveEnabled {
		t.Error("DriveEnabled should default to true")
	}
	if !cfg.Features.HarvesterEnabled {
		t.Error("HarvesterEnabled should default to true")
	}
	if !cfg.Features.ScriptDocsEnabled {
		t.Error("ScriptDocsEnabled should default to true")
	}
	if !cfg.Features.ScriptClipsEnabled {
		t.Error("ScriptClipsEnabled should default to true")
	}
	if !cfg.Features.StockEnabled {
		t.Error("StockEnabled should default to true")
	}
}

func TestFeaturesConfigDisabled(t *testing.T) {
	cfg := &Config{
		Features: FeaturesConfig{
			ArtlistEnabled:    false,
			YouTubeEnabled:    false,
			DriveEnabled:      false,
			HarvesterEnabled:  false,
			ScriptDocsEnabled: false,
			ScriptClipsEnabled: false,
			StockEnabled:      false,
		},
	}

	if cfg.Features.ArtlistEnabled {
		t.Error("ArtlistEnabled should be false")
	}
	if cfg.Features.YouTubeEnabled {
		t.Error("YouTubeEnabled should be false")
	}
}
