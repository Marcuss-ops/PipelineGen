package config

import (
	"testing"
)

func TestConfigValidateFailsWithoutAdminTokenWhenAuthEnabled(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: true,
			AdminToken: "",
		},
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when auth enabled without admin token")
	}
}

func TestConfigValidateFailsWithInvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"port zero", 0},
		{"port too high", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Security: SecurityConfig{
					EnableAuth: false,
				},
				Server: ServerConfig{
					Port:         tt.port,
					ReadTimeout:  600,
					WriteTimeout: 600,
				},
				External: ExternalConfig{
					OllamaURL: "http://localhost:11434",
				},
			}
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected error for port %d", tt.port)
			}
		})
	}
}

func TestConfigValidateFailsWithMissingOllamaURL(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: false,
		},
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when ollama url is empty")
	}
}

func TestConfigValidateFailsWithZeroReadTimeout(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: false,
		},
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  0,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when read timeout is zero")
	}
}

func TestConfigValidateFailsWithZeroWriteTimeout(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: false,
		},
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 0,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when write timeout is zero")
	}
}

// ── DriveConfig resolver tests ──

type driveFolderTestCase struct {
	name     string
	drive    DriveConfig
	expected string
}

func TestDriveConfigRootFolder(t *testing.T) {
	tests := []driveFolderTestCase{
		{"returns media root when set", DriveConfig{MediaRootFolder: "mediaRoot"}, "mediaRoot"},
		{"returns empty when unset", DriveConfig{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.drive.RootFolder(); got != tt.expected {
				t.Errorf("RootFolder() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDriveConfigResolveFolder(t *testing.T) {
	tests := []struct {
		name         string
		drive        DriveConfig
		specificRoot string
		expected     string
	}{
		// Priority 1: MediaRootFolder
		{"prefers media root over specific", DriveConfig{MediaRootFolder: "mediaRoot", StockRootFolder: "stockRoot"}, "stockRoot", "mediaRoot"},
		{"prefers media root when specific is empty", DriveConfig{MediaRootFolder: "mediaRoot"}, "", "mediaRoot"},
		{"trims spaces on media root", DriveConfig{MediaRootFolder: "  mediaRoot  "}, "", "mediaRoot"},
		// Priority 2: specific root
		{"falls back to specific when media root is empty", DriveConfig{StockRootFolder: "stockRoot"}, "stockRoot", "stockRoot"},
		{"trims spaces on specific root", DriveConfig{StockRootFolder: "  stockRoot  "}, "stockRoot", "stockRoot"},
		// Priority 3: empty
		{"returns empty when both unset", DriveConfig{}, "", ""},
		{"returns empty when both empty strings", DriveConfig{MediaRootFolder: "", StockRootFolder: ""}, "", ""},
		{"returns empty when both whitespace", DriveConfig{MediaRootFolder: "  ", StockRootFolder: "  "}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.drive.ResolveFolder(tt.specificRoot); got != tt.expected {
				t.Errorf("ResolveFolder(%q) = %q, want %q", tt.specificRoot, got, tt.expected)
			}
		})
	}
}

func TestDriveConfigConvenienceMethods(t *testing.T) {
	// Each convenience method is tested across 3 scenarios:
	// 1. MediaRootFolder set → returns MediaRootFolder
	// 2. Only specific root set → returns specific root
	// 3. Neither set → returns ""
	tests := []struct {
		name         string
		setSpecific  func(d *DriveConfig, val string)
		callMethod   func(d DriveConfig) string
		specificName string
	}{
		{"StockFolder", func(d *DriveConfig, v string) { d.StockRootFolder = v }, func(d DriveConfig) string { return d.StockFolder() }, "stockRoot"},
		{"ClipsFolder", func(d *DriveConfig, v string) { d.ClipsRootFolder = v }, func(d DriveConfig) string { return d.ClipsFolder() }, "clipsRoot"},
		{"VoiceoverFolder", func(d *DriveConfig, v string) { d.VoiceoverRootFolder = v }, func(d DriveConfig) string { return d.VoiceoverFolder() }, "voRoot"},
		{"ArtlistFolder", func(d *DriveConfig, v string) { d.ArtlistRootFolder = v }, func(d DriveConfig) string { return d.ArtlistFolder() }, "artlistRoot"},
		{"BooksFolder", func(d *DriveConfig, v string) { d.BooksRootFolder = v }, func(d DriveConfig) string { return d.BooksFolder() }, "booksRoot"},
		{"ScriptsFolder", func(d *DriveConfig, v string) { d.ScriptsRootFolder = v }, func(d DriveConfig) string { return d.ScriptsFolder() }, "scriptsRoot"},
		{"ImagesFolder", func(d *DriveConfig, v string) { d.ImagesRootFolder = v }, func(d DriveConfig) string { return d.ImagesFolder() }, "imagesRoot"},
		{"VideoAIFolder", func(d *DriveConfig, v string) { d.VideoAIRootFolder = v }, func(d DriveConfig) string { return d.VideoAIFolder() }, "videoAIRoot"},
		{"CopertineFolder", func(d *DriveConfig, v string) { d.CopertineRootFolder = v }, func(d DriveConfig) string { return d.CopertineFolder() }, "copertineRoot"},
		{"SoundEffectsFolder", func(d *DriveConfig, v string) { d.SoundEffectsRootFolder = v }, func(d DriveConfig) string { return d.SoundEffectsFolder() }, "sfxRoot"},
		{"OutroFolder", func(d *DriveConfig, v string) { d.OutroRootFolder = v }, func(d DriveConfig) string { return d.OutroFolder() }, "outroRoot"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/mediaRoot", func(t *testing.T) {
			d := DriveConfig{MediaRootFolder: "mediaRoot"}
			tt.setSpecific(&d, "specific")
			if got := tt.callMethod(d); got != "mediaRoot" {
				t.Errorf("%s() = %q, want %q when MediaRootFolder is set", tt.name, got, "mediaRoot")
			}
		})

		t.Run(tt.name+"/specificRoot", func(t *testing.T) {
			d := DriveConfig{}
			tt.setSpecific(&d, tt.specificName)
			if got := tt.callMethod(d); got != tt.specificName {
				t.Errorf("%s() = %q, want %q when only specific root is set", tt.name, got, tt.specificName)
			}
		})

		t.Run(tt.name+"/empty", func(t *testing.T) {
			d := DriveConfig{}
			if got := tt.callMethod(d); got != "" {
				t.Errorf("%s() = %q, want %q when nothing is set", tt.name, got, "")
			}
		})
	}
}

func TestConfigValidateAcceptsValidConfig(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: true,
			AdminToken: "valid-token",
		},
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config to pass, got error: %v", err)
	}
}
