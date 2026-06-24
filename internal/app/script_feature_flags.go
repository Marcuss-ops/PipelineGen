package app

import "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

func anyScriptFeatureEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Features.ScriptClipsEnabled || cfg.Features.ScriptDocsEnabled || cfg.Features.ImagesEnabled
}
