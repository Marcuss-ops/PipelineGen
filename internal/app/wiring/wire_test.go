package wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func TestWireServicesDoesNotPanicWithoutDriveAndArtlist(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..", "..")
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to change to project root: %v", err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: false,
		},
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{
				SourceLanguage: "en",
			},
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
		},
	}
	cfg.Linguistics.LexiconRoot = testLexiconRoot()
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}
	if deps == nil {
		t.Fatal("expected non-nil deps")
	}
	defer deps.Runtime.Lifecycle.Stop(context.Background())
}

func TestCleanupCanBeCalledMultipleTimesSafely(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..", "..")
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to change to project root: %v", err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: false,
		},
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{
				SourceLanguage: "en",
			},
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
		},
	}
	cfg.Linguistics.LexiconRoot = testLexiconRoot()
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}

	// Call Cleanup multiple times
	deps.Runtime.Lifecycle.Stop(context.Background())
	deps.Runtime.Lifecycle.Stop(context.Background())
	deps.Runtime.Lifecycle.Stop(context.Background())
}

func TestWireServicesSkipsOptionalHandlersWhenDepsMissing(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..", "..")
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to change to project root: %v", err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: false,
		},
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{
				SourceLanguage: "en",
			},
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
			YouTubeEnabled: false,
		},
	}
	cfg.Linguistics.LexiconRoot = testLexiconRoot()
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}
	defer deps.Runtime.Lifecycle.Stop(context.Background())

	if deps.Handlers.Registry == nil {
		t.Fatal("expected non-nil Registry")
	}
}

func TestStartupIntegration(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..", "..")
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to change to project root: %v", err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: false,
		},
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{
				SourceLanguage: "en",
			},
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
		},
	}
	cfg.Linguistics.LexiconRoot = testLexiconRoot()
	log := zap.NewNop()

	// Ensure system starts up cleanly and registers all modules without error
	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("startup integration test failed: %v", err)
	}
	if deps == nil {
		t.Fatal("expected non-nil deps")
	}
	defer deps.Runtime.Lifecycle.Stop(context.Background())
}
