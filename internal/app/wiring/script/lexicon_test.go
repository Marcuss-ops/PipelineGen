package script

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestInitLinguisticsFailsWhenRequiredLanguageMissing(t *testing.T) {
	cfg := &config.Config{
		Linguistics: config.LinguisticsConfig{
			RequiredLanguages: []string{"zz"},
		},
	}

	err := InitLinguistics(cfg, nil)
	if err == nil {
		t.Fatal("expected InitLinguistics to fail when required language profile is missing")
	}
	if !strings.Contains(err.Error(), `required language profile "zz" missing`) {
		t.Errorf("expected error to mention missing required language, got: %v", err)
	}
}
