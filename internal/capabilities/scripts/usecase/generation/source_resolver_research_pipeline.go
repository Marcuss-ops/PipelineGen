package generation

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ResearchPlan is the immutable execution plan produced before any external
// search or page fetch. It makes policy decisions visible to the I/O stages.
type ResearchPlan struct {
	Topic            string
	Language         string
	Queries          []string
	MaxPages         int
	MinSources       int
	MinFullPage      int
	MinEvidenceScore float64
	SearchEnabled    bool
	CacheMode        string
	ForceRefresh     bool
}

// ResearchEvidence is the normalized, provider-independent evidence unit.
type ResearchEvidence struct {
	Source scriptpkg.ResearchWebSource
	Claim  scriptpkg.ResearchClaim
}
