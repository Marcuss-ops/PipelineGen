package wiring

import (
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestValidateScriptFlowDependenciesRequiresConfiguredBundles(t *testing.T) {
	cfg := &config.Config{}
	cfg.Scripts.Capability.Enabled = true
	cfg.Scripts.Capability.RequireAI = true
	cfg.Security.DeliveryInsecureDev = true

	ready, err := validateScriptFlowDependencies(cfg, &ComposeRoot{}, zap.NewNop())
	if err != nil {
		t.Fatalf("development fallback should disable cleanly: %v", err)
	}
	if ready {
		t.Fatal("incomplete dependencies must not report ready")
	}
}
