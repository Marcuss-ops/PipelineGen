package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestSceneIntelligenceShadowPathIsExplicitOptIn(t *testing.T) {
	cfg := &config.Config{}
	cfg.External.RustSceneIntelligencePath = "bin/scene_intelligence"
	if got := sceneIntelligenceShadowPath(cfg); got != "" {
		t.Fatalf("shadow analyzer enabled by binary path alone: %q", got)
	}
	cfg.External.RustSceneIntelligenceShadowEnabled = true
	if got := sceneIntelligenceShadowPath(cfg); got != "bin/scene_intelligence" {
		t.Fatalf("explicit shadow opt-in path = %q", got)
	}
	cfg.External.RustSceneIntelligencePath = "  "
	if got := sceneIntelligenceShadowPath(cfg); got != "" {
		t.Fatalf("empty shadow binary path = %q", got)
	}
}
