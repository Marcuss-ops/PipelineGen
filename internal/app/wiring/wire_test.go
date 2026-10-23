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
	defer func() {
		if chdirErr := os.Chdir(origDir); chdirErr != nil {
			t.Errorf("restore working directory: %v", chdirErr)
		}
	}()

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
			DriveEnabled: false,
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
	defer func() {
		if stopErr := deps.Runtime.Lifecycle.Stop(context.Background()); stopErr != nil {
			t.Errorf("lifecycle stop: %v", stopErr)
		}
	}()
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
	defer func() {
		if chdirErr := os.Chdir(origDir); chdirErr != nil {
			t.Errorf("restore working directory: %v", chdirErr)
		}
	}()

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
			DriveEnabled: false,
		},
	}
	cfg.Linguistics.LexiconRoot = testLexiconRoot()
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}

	// Call Cleanup multiple times; every idempotent stop must succeed.
	for attempt := 0; attempt < 3; attempt++ {
		if stopErr := deps.Runtime.Lifecycle.Stop(context.Background()); stopErr != nil {
			t.Fatalf("lifecycle stop %d: %v", attempt+1, stopErr)
		}
	}
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
	defer func() {
		if chdirErr := os.Chdir(origDir); chdirErr != nil {
			t.Errorf("restore working directory: %v", chdirErr)
		}
	}()

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
			YouTubeEnabled: false,
		},
	}
	cfg.Linguistics.LexiconRoot = testLexiconRoot()
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}
	defer func() {
		if stopErr := deps.Runtime.Lifecycle.Stop(context.Background()); stopErr != nil {
			t.Errorf("lifecycle stop: %v", stopErr)
		}
	}()

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
	defer func() {
		if chdirErr := os.Chdir(origDir); chdirErr != nil {
			t.Errorf("restore working directory: %v", chdirErr)
		}
	}()

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
			DriveEnabled: false,
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
	defer func() {
		if stopErr := deps.Runtime.Lifecycle.Stop(context.Background()); stopErr != nil {
			t.Errorf("lifecycle stop: %v", stopErr)
		}
	}()
}
