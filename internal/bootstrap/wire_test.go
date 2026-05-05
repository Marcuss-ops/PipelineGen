package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"velox/go-master/pkg/config"
)

func TestWireServicesDoesNotPanicWithoutDriveAndArtlist(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..")
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
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
		},
	}
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}
	if deps == nil {
		t.Fatal("expected non-nil deps")
	}
	defer deps.Cleanup()
}

func TestCleanupCanBeCalledMultipleTimesSafely(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..")
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
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
		},
	}
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}

	// Call Cleanup multiple times
	deps.Cleanup()
	deps.Cleanup()
	deps.Cleanup()
}

func TestWireServicesSkipsOptionalHandlersWhenDepsMissing(t *testing.T) {
	// Change to project root so migration paths resolve correctly
	projectRoot := filepath.Join("..", "..")
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
			Port:        8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: config.ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
		Storage: config.StorageConfig{
			DataDir: tmpDir,
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			ArtlistEnabled: false,
			YouTubeEnabled: false,
		},
	}
	log := zap.NewNop()

	deps, err := WireServices(cfg, log, "test")
	if err != nil {
		t.Fatalf("WireServices failed: %v", err)
	}
	defer deps.Cleanup()

	if deps.Handlers == nil {
		t.Fatal("expected non-nil Handlers")
	}
}
