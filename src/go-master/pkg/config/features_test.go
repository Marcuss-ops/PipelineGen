package config

import (
	"testing"
)

func TestFeaturesConfigDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	// Features default to false (must be explicitly enabled)
	if cfg.Features.ArtlistEnabled {
		t.Error("ArtlistEnabled should default to false")
	}
	if cfg.Features.YouTubeEnabled {
		t.Error("YouTubeEnabled should default to false")
	}
	if cfg.Features.DriveEnabled {
		t.Error("DriveEnabled should default to false")
	}
	if cfg.Features.ScriptDocsEnabled {
		t.Error("ScriptDocsEnabled should default to false")
	}
	if cfg.Features.ScriptClipsEnabled {
		t.Error("ScriptClipsEnabled should default to false")
	}
	if cfg.Features.VoiceoverEnabled {
		t.Error("VoiceoverEnabled should default to false")
	}
	if cfg.Features.WorkflowEnabled {
		t.Error("WorkflowEnabled should default to false")
	}
	if cfg.Features.ImagesEnabled {
		t.Error("ImagesEnabled should default to false")
	}
}

func TestFeaturesConfigDisabled(t *testing.T) {
	cfg := &Config{
		Features: FeaturesConfig{
			ArtlistEnabled:    false,
			YouTubeEnabled:    false,
			DriveEnabled:      false,
			ScriptDocsEnabled: false,
			ScriptClipsEnabled: false,
			VoiceoverEnabled:  false,
			WorkflowEnabled:   false,
			ImagesEnabled:     false,
		},
}

	if cfg.Features.ArtlistEnabled {
		t.Error("ArtlistEnabled should be false")
	}
	if cfg.Features.YouTubeEnabled {
		t.Error("YouTubeEnabled should be false")
	}
}
