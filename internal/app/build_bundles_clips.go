// Package app — clipindexer job handler late-binding (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
package app

import (
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// wireClipIndexerJobBinding registers the media_reindex handler into
// jobs.Service. Extracted from NewComposition per PG-028.
func wireClipIndexerJobBinding(process *ProcessBundle, jobs *JobsBundle) error {
	if process.ClipIndexerService != nil && jobs.Service != nil {
		if err := process.ClipIndexerService.RegisterJobHandler(jobs.Service); err != nil {
			return err
		}
	}
	return nil
}

// appendClipIndexerCriticalValidator populates the critical-handler
// validators slice with the clipindexer.media_reindex binding.
// Extracted from NewComposition per PG-028.
func appendClipIndexerCriticalValidator(process *ProcessBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
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
