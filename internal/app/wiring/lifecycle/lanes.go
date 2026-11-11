// Package lifecycle owns scheduling policy shared by the composition root's
// worker pools. It has no dependency on the composition root itself.
package lifecycle

import (
	"os"
	"sort"
	"strconv"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	EnvInteractiveWorkers     = "PIPELINEGEN_INTERACTIVE_WORKERS"
	EnvFeederWorkers          = "PIPELINEGEN_FEEDER_WORKERS"
	DefaultInteractiveWorkers = 2
	DefaultFeederWorkers      = 2
)

// ClipRenderPhaseScope derives both sides of the general/settle pool split
// from the capability that writes the payload contract.
func ClipRenderPhaseScope() (job.PayloadMatch, job.PayloadNotMatch) {
	match := job.PayloadMatch{cliprender.PayloadKeyRenderPhase: string(cliprender.RenderPhaseSettle)}
	exclude := job.PayloadNotMatch{cliprender.PayloadKeyRenderPhase: string(cliprender.RenderPhaseSettle)}
	return match, exclude
}

func InteractiveJobTypes() []string {
	return []string{scriptpkg.TypeGenerate, scriptpkg.TypeGenerateItem}
}

func InteractiveWorkerBudget() int {
	return workerBudget(EnvInteractiveWorkers, DefaultInteractiveWorkers)
}

func FeederJobTypes() []string {
	return []string{job.TypeYouTubeClipExtract, job.TypeAssetTextMaterialize}
}

func FeederWorkerBudget() int {
	return workerBudget(EnvFeederWorkers, DefaultFeederWorkers)
}

func workerBudget(env string, defaultBudget int) int {
	budget := defaultBudget
	if raw := strings.TrimSpace(os.Getenv(env)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			budget = parsed
		}
	}
	if budget < 0 {
		return 0
	}
	if budget > 8 {
		return 8
	}
	return budget
}

// GeneralPoolJobTypes returns the positive registry complement of enabled
// dedicated lanes. An empty registry/complement preserves unfiltered claiming.
func GeneralPoolJobTypes() []string {
	all := appjobs.Compose().AllTypes()
	if len(all) == 0 {
		return nil
	}
	excluded := make(map[string]bool, 4)
	if InteractiveWorkerBudget() > 0 {
		for _, typ := range InteractiveJobTypes() {
			excluded[typ] = true
		}
	}
	if FeederWorkerBudget() > 0 {
		for _, typ := range FeederJobTypes() {
			excluded[typ] = true
		}
	}
	out := make([]string, 0, len(all))
	for _, typ := range all {
		if !excluded[typ] {
			out = append(out, typ)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
