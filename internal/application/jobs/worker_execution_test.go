package jobs

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

func TestExtractStagedArtifacts_PreservesRequirement(t *testing.T) {
	manifest := job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-1",
		JobID:         "job-1",
		Artifacts: []job.Artifact{
			{
				ID:       "job-1:script",
				Kind:     job.ArtifactKindScriptJSON,
				Filename: "script.json",
				MIMEType: "application/json",
				Required: true,
			},
			{
				ID:       "job-1:image:0",
				Kind:     job.ArtifactKindImage,
				Filename: "image.png",
				MIMEType: "image/png",
				Required: false,
			},
		},
	}

	raw := extractStagedArtifacts(map[string]any{
		job.ManifestKey: &manifest,
	})

	var got []finalization.PublishedArtifact
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal staged artifacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(got))
	}
	if got[0].Requirement != finalization.ArtifactRequirementRequired {
		t.Fatalf("artifact[0].Requirement = %v, want %v", got[0].Requirement, finalization.ArtifactRequirementRequired)
	}
	if got[1].Requirement != finalization.ArtifactRequirementOptional {
		t.Fatalf("artifact[1].Requirement = %v, want %v", got[1].Requirement, finalization.ArtifactRequirementOptional)
	}
}
