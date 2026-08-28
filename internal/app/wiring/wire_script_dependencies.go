package wiring

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// validateScriptFlowDependencies applies the boot-time capability policy.
// It returns false when development mode intentionally disables the feature.
func validateScriptFlowDependencies(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (bool, error) {
	cap := cfg.Scripts.Capability
	aiPresent := root.AI != nil && root.AI.ScriptGen != nil && root.AI.ScriptEngine != nil
	audioPresent := root.Domains != nil && root.Domains.AudioProcessor != nil
	if cap.RequireAI {
		if !aiPresent {
			if cfg.Security.DeliveryInsecureDev {
				log.Warn("wireScriptFlow: script capability disabled in dev — missing AI bundle (ScriptEngine)")
				return false, nil
			}
			return false, fmt.Errorf("wireScriptFlow: required AI bundle (ScriptEngine) is missing")
		}
		if !audioPresent {
			if cfg.Security.DeliveryInsecureDev {
				log.Warn("wireScriptFlow: script capability disabled in dev — missing audio processor")
				return false, nil
			}
			return false, fmt.Errorf("wireScriptFlow: required audio processor is missing")
		}
	} else if !aiPresent || !audioPresent {
		log.Warn("wireScriptFlow: AI bundle incomplete — disabling ScriptFlow without registering routes")
		return false, nil
	}
	if cap.RequireDrive && root.Drive == nil {
		if cfg.Security.DeliveryInsecureDev {
			log.Warn("wireScriptFlow: script capability disabled in dev — missing Drive bundle")
			return false, nil
		}
		return false, fmt.Errorf("wireScriptFlow: required Drive bundle is missing")
	}
	if cap.RequireDatabase && root.DB == nil {
		if cfg.Security.DeliveryInsecureDev {
			log.Warn("wireScriptFlow: script capability disabled in dev — missing database")
			return false, nil
		}
		return false, fmt.Errorf("wireScriptFlow: required database is missing")
	}
	return true, nil
}
