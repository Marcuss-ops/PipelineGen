// Package app — images job handler late-binding (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
package app

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// wireImagesJobBinding registers the image generation handler into
// jobs.Service. Extracted from NewComposition per PG-028.
func wireImagesJobBinding(domains *wiring.DomainBundle, jobs *wiring.JobsBundle) error {
	if domains.ImageService != nil && jobs.Service != nil {
		if err := domains.ImageService.RegisterHandler(jobs.Service); err != nil {
			return fmt.Errorf("images.image_generate_google: %w", err)
		}
	}
	return nil
}

// appendImagesCriticalValidator populates the critical-handler validators
// slice with the images.image_generate_google binding.
// Extracted from NewComposition per PG-028.
func appendImagesCriticalValidator(domains *wiring.DomainBundle, jobs *wiring.JobsBundle, validators *[]CriticalHandler) {
	if domains.ImageService != nil && jobs.Service != nil {
		img := domains.ImageService
		*validators = append(*validators,
			CriticalHandler{
				Name: "images.image_generate_google",
				Bind: func(svc *appjobs.Service) error {
					return img.RegisterHandler(svc)
				},
			},
		)
	}
}
