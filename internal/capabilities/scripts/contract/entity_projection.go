package contract

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GenerateArtifacts contains compatibility projections that are derived from
// the canonical durable result. It is not an independent entity source.
type GenerateArtifacts struct {
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`
}
