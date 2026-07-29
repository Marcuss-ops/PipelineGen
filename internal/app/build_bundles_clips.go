// Package app — clipindexer job handler late-binding.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// wireClipIndexerJobBinding registers the media_reindex handler into
// jobs.Service.
func wireClipIndexerJobBinding(process *wiring.ProcessBundle, jobs *wiring.JobsBundle) error {
	if process.ClipIndexerService != nil && jobs.Service != nil {
		if err := process.ClipIndexerService.RegisterJobHandler(jobs.Service); err != nil {
			return fmt.Errorf("clipindexer.media_reindex: %w", err)
		}
	}
	return nil
}

// appendClipIndexerCriticalValidator populates the critical-handler
// validators slice with the clipindexer.media_reindex binding.
func appendClipIndexerCriticalValidator(process *wiring.ProcessBundle, jobs *wiring.JobsBundle, validators *[]CriticalHandler) {
	if process.ClipIndexerService != nil && jobs.Service != nil {
		ci := process.ClipIndexerService
		*validators = append(*validators,
			CriticalHandler{
				Name: "clipindexer.media_reindex",
				Bind: func(svc *appjobs.Service) error {
					return ci.RegisterJobHandler(svc)
				},
			},
		)
	}
}
