package buildbundle

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

func chdirToProjectRoot(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join("..", "..", "..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func minimalConfig(dataDir string) *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{EnableAuth: false},
		Server:   config.ServerConfig{Port: 8080, ReadTimeout: 600, WriteTimeout: 600},
		External: config.ExternalConfig{
			OllamaURL:            "http://localhost:11434",
			OllamaModel:          "llama3.2",
			OllamaTimeoutSeconds: 30,
			// pgvector contract gate (PR-VECTOR-CONFIG-MISMATCH-GATE)
			// demands runtime_model == schema dense model when pgvector is
			// enabled; keep the fixture in the canonical default so
			// vector-enabled composition tests are not pre-broken.
			OllamaEmbedModel: "intfloat/multilingual-e5-base",
		},
		Paths:   config.PathsConfig{},
		Storage: config.StorageConfig{DataDir: dataDir},
		Media: config.MediaConfig{
			Multilingual: config.MultilingualConfig{SourceLanguage: "en"},
		},
		Features: config.FeaturesConfig{
			DriveEnabled:   false,
			YouTubeEnabled: false,
		},
		VLM:         config.VLMConfig{Enabled: false},
		ClipIndexer: config.ClipIndexerConfig{Enabled: false},
		Reranker:    config.RerankerConfig{Enabled: false},
		Drive:       config.DriveConfig{},
		Jobs: config.JobsConfig{
			EnableBackgroundJobs: false, // suppress lifecycle spawners
		},
	}
}

type (
	dummyStagingStore struct{ staging.Store }
	dummyArtifactRepo struct{ detail.ArtifactStageRepository }
	dummyPublisher    struct{ delivery.Publisher }
)

func qualifiedExprName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		prefix := qualifiedExprName(n.X)
		if prefix == "" {
			return n.Sel.Name
		}
		return prefix + "." + n.Sel.Name
	case *ast.ParenExpr:
		return qualifiedExprName(n.X)
	}
	return ""
}
