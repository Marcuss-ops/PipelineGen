package registry

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/document"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/documents"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/generation"
	domainvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	domainyoutube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ValidateRuntimeGraph constructs and validates the canonical C3 job runtime graph.
// Registry ownership lives here rather than in the composition-root facade.
func ValidateRuntimeGraph() error {
	mutableReg := job.NewMutableJobRegistry()

	additionalOwnerTypes := []string{
		domainyoutube.TypeClipExtract,
		script.TypeGenerate,
		documents.JobGenerate,
		domainvoiceover.TypeGenerate,
		media.TypeBulkUploadYouTubeClips,
	}
	for _, registerOwner := range []func(job.MutableJobRegistry) error{
		images.MustRegister,
		domainyoutube.MustRegister,
		scriptgeneration.MustRegister,
		documents.MustRegister,
		voiceover.MustRegister,
		clips.MustRegister,
	} {
		if err := registerOwner(mutableReg); err != nil {
			return fmt.Errorf("c3: owner must-register: %w", err)
		}
	}
	ownerRegisteredTypes := map[string]bool{
		domainyoutube.TypeClipExtract:    true,
		script.TypeGenerate:              true,
		documents.JobGenerate:            true,
		domainvoiceover.TypeGenerate:     true,
		media.TypeBulkUploadYouTubeClips: true,
	}

	for _, def := range job.CanonicalJobDefinitions {
		if ownerRegisteredTypes[def.Type] {
			continue
		}
		if err := mutableReg.RegisterDefinition(def); err != nil {
			return fmt.Errorf("register %s: %w", def.Type, err)
		}
		placeholder := func(_ context.Context, _ *job.Job, _ any) (any, error) {
			return nil, nil
		}
		if err := mutableReg.BindHandler(def.Type, placeholder); err != nil {
			return fmt.Errorf("bind handler %s: %w", def.Type, err)
		}
	}

	ownerPlaceholder := func(_ context.Context, _ *job.Job, _ any) (any, error) {
		return nil, nil
	}
	for _, ownerType := range additionalOwnerTypes {
		if err := mutableReg.BindHandler(ownerType, ownerPlaceholder); err != nil {
			return fmt.Errorf("bind owner handler %s: %w", ownerType, err)
		}
	}

	compiled, err := mutableReg.Freeze()
	if err != nil {
		return fmt.Errorf("freeze: %w", err)
	}
	workflowRefs := []string{
		script.TypeGenerate,
		images.TypeImagesGenerate,
		document.TypeGenerate,
		asset.TypeResolve,
		media.TypeClipRegister,
	}
	validator := job.DefaultStartupValidator{}
	return validator.ValidateRuntimeGraph(job.StartupValidationInput{
		Registry: compiled,
		Workflow: workflowRefs,
	})
}
