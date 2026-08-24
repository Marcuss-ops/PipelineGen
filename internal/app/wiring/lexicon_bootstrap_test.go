package wiring

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func testLexiconRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../config/lexicons"))
}

func TestInitLinguistics_FailsWhenRequiredLanguageMissing(t *testing.T) {
	cfg := &config.Config{
		Linguistics: config.LinguisticsConfig{
			LexiconRoot:       t.TempDir(),
			RequiredLanguages: []string{"zz"},
		},
	}
	if err := os.MkdirAll(filepath.Join(cfg.Linguistics.LexiconRoot, "fallback"), 0755); err != nil {
		t.Fatal(err)
	}

	err := initLinguistics(cfg, nil)
	if err == nil {
		t.Fatal("expected initLinguistics to fail when required language profile is missing")
	}
	if !strings.Contains(err.Error(), "required language profile \"zz\" missing") {
		t.Errorf("expected error to mention missing required language, got: %v", err)
	}
}
