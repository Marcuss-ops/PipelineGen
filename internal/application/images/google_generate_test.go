package images

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func TestRealGoogleSlidesImageGen(t *testing.T) {
	if os.Getenv("RUN_REAL_SLIDES_TEST") != "true" {
		t.Skip("Skipping real Slides API test. Set RUN_REAL_SLIDES_TEST=true to run.")
	}

	cfg := config.Get()
	dir, _ := os.Getwd()
	repoRoot := filepath.Join(dir, "..", "..", "..")
	cfg.Paths.CredentialsFile = filepath.Join(repoRoot, "credentials.json")
	cfg.Paths.TokenFile = filepath.Join(repoRoot, "token.json")
	cfg.Storage.TempDir = filepath.Join(repoRoot, "tmp")
	cfg.Storage.DataDir = filepath.Join(repoRoot, "data")

	log, _ := zap.NewDevelopment()

	s := &Service{
		cfg:     cfg,
		log:     log,
		tempDir: cfg.Storage.TempPath(),
	}

	ctx := context.Background()
	// Run real generation. Set skipDrive=true to focus on Slides API + download.
	asset, err := s.GenerateSmartImage(ctx, "Automated Test Slide", "", "", nil, []string{"test-slides"}, 1920, 1080, "", true)
	if err != nil {
		t.Fatalf("Failed to generate real slides image: %v", err)
	}

	t.Logf("Real Slides Image Generated successfully!")
	t.Logf("Asset description: %s", asset.Description)
	t.Logf("Path: %s", asset.PathRel)
}
