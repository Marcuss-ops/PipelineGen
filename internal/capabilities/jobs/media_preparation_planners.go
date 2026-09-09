package jobs

import (
	"context"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ManifestPreparationPlanner is a small deterministic planner for media
// operations whose inputs are supplied by the caller. It keeps canonical
// manifests in the unit, so fingerprints and persisted diagnostics include all
// result-affecting policies.
type ManifestPreparationPlanner struct {
	Kind             string
	ProcessorVersion string
	Manifest         job.CanonicalManifest
}

func (p ManifestPreparationPlanner) Plan(_ context.Context, j *job.Job) (PreparationPlan, error) {
	if j == nil || j.ID == "" {
		return PreparationPlan{}, fmt.Errorf("media preparation: job is required")
	}
	if p.Kind == "" {
		return PreparationPlan{}, fmt.Errorf("media preparation: kind is required")
	}
	manifest := p.Manifest.Fields
	if manifest == nil {
		manifest = job.InputManifest{}
	}
	manifestJSON, err := p.Manifest.JSON()
	if err != nil {
		return PreparationPlan{}, fmt.Errorf("media preparation manifest: %w", err)
	}
	fingerprint, err := PreparationUnitFingerprint(p.Kind, j.Type, manifestJSON, nil, nil, p.ProcessorVersion)
	if err != nil {
		return PreparationPlan{}, fmt.Errorf("media preparation fingerprint: %w", err)
	}
	return PreparationPlan{JobID: j.ID, Units: []PreparationUnit{{
		ID: "media." + p.Kind, Kind: p.Kind, Fingerprint: fingerprint,
		ResourceClass: "CPU_LIGHT", CostClass: "MEDIUM", Reusable: true,
		Inputs: manifest, ProcessorVersion: p.ProcessorVersion,
	}}}, nil
}

func NewClipPreparationPlanner() PreparationPlanner {
	return ManifestPreparationPlanner{Kind: "clip.process", ProcessorVersion: "clip-v1", Manifest: job.ClipManifest("", 0, 0, 0, 0, 0, 1, "", "", "", "", "", "clip-v1")}
}
