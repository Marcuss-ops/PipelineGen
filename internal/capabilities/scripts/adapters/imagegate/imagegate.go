// Package imagegate re-exports the kernel-owned image-slot verdict.
//
// The implementation moved to internal/kernel/script/imagegate so platform
// adapters can consume it without importing a capability business package.
// This package keeps the existing vidrush call sites on one canonical
// implementation.
package imagegate

import (
	kernelimagegate "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/imagegate"
)

var (
	ReusablePersonImageVerdict          = kernelimagegate.ReusablePersonImageVerdict
	CandidateEligibleForSlot            = kernelimagegate.CandidateEligibleForSlot
	FilterVerifiedPersonImageCandidates = kernelimagegate.FilterVerifiedPersonImageCandidates
	AllCandidatesHaveReusableVerdict    = kernelimagegate.AllCandidatesHaveReusableVerdict
)
