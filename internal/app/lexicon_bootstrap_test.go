package app

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestInitLinguistics_FailsWhenRequiredLanguageMissing(t *testing.T) {
	cfg := &config.Config{
		Linguistics: config.LinguisticsConfig{
			LexiconRoot:       t.TempDir(),
			RequiredLanguages: []string{"zz"},
		},
	}

	err := initLinguistics(cfg, nil)
	if err == nil {
		t.Fatal("expected initLinguistics to fail when required language profile is missing")
	}
	if !strings.Contains(err.Error(), "required language profile \"zz\" missing") {
		t.Errorf("expected error to mention missing required language, got: %v", err)
	}
}
