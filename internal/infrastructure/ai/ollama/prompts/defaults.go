package prompts

import "github.com/Marcuss-ops/PipelineGen/internal/platform/config"

func resolvedWordsPerMinute(value int) int {
	if value > 0 {
		return value
	}
	return config.DefaultScriptDefaults().WordsPerMinute
}
