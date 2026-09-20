package script

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestInitLinguisticsFailsWhenRequiredLanguageMissing(t *testing.T) {
	cfg := &config.Config{
		Linguistics: config.LinguisticsConfig{
			LexiconRoot:       t.TempDir(),
			RequiredLanguages: []string{"zz"},
		},
	}
	if err := os.MkdirAll(filepath.Join(cfg.Linguistics.LexiconRoot, "fallback"), 0755); err != nil {
		t.Fatal(err)
	}

	err := InitLinguistics(cfg, nil)
	if err == nil {
		t.Fatal("expected InitLinguistics to fail when required language profile is missing")
	}
	if !strings.Contains(err.Error(), "required language profile \"zz\" missing") {
		t.Errorf("expected error to mention missing required language, got: %v", err)
	}
}
