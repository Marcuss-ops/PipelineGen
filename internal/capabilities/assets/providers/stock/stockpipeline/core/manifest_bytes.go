package core

import (
	"encoding/json"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func ManifestBytes(manifest *job.ArtifactManifest) ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: nil manifest")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: marshal: %w", err)
	}
	return raw, nil
}
