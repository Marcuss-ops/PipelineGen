package wiring

import (
	artifactfinalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifact_finalize"
	stagingsvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// StagingBundle owns the canonical staging store, repository and resolved
// workspace used by the publication saga.
type StagingBundle struct {
	Store      stagingsvc.Store
	Repository artifact.ArtifactStageRepository
	Workspace  string
}

// FinalizerBundle owns the typed finalization surface for the publication saga.
type FinalizerBundle struct {
	Finalizer artifactfinalize.Finalizer
}
