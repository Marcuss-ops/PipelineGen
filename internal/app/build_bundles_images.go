// Package app — images job handler late-binding (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
package app

import (
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// wireImagesJobBinding registers the image generation handler into
// jobs.Service. Extracted from NewComposition per PG-028.
func wireImagesJobBinding(domains *DomainBundle, jobs *JobsBundle) error {
	if domains.ImageService != nil && jobs.Service != nil {
		if err := domains.ImageService.RegisterHandler(jobs.Service); err != nil {
			return err
		}
	}
	return nil
}

// appendImagesCriticalValidator populates the critical-handler validators
// slice with the images.image_generate_google binding.
// Extracted from NewComposition per PG-028.
func appendImagesCriticalValidator(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
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
