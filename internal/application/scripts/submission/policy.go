// Package submission — policy.go owns the canonical job policy
// resolution for script-generation submissions.
//
// PR-SUBMISSION-FACTORY (July 2026): priority and retry policy
// previously lived as hard-coded literals in the HTTP transport.
// This file extracts them into a typed resolver so the application
// layer owns the policy and the transport layer only forwards the
// resolved values.
package submission

import (
	"errors"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// JobPolicy captures the runtime policy for an enqueued job.
type JobPolicy struct {
	Priority   int
	MaxRetries int
}

// JobPolicyResolver resolves a JobPolicy for a canonical job type.
// It is intentionally small and stateless; future constructors may
// accept configuration to override defaults.
type JobPolicyResolver struct{}

// NewJobPolicyResolver returns the canonical resolver.
func NewJobPolicyResolver() *JobPolicyResolver {
	return &JobPolicyResolver{}
}

// ErrUnknownJobType is returned by Resolve when the job type has
// no defined policy.
var ErrUnknownJobType = errors.New("submission: unknown job type")

// Resolve returns the JobPolicy for the given job type.
// For any unknown type it returns ErrUnknownJobType so callers
// can fail-closed instead of silently using zero values.
func (r *JobPolicyResolver) Resolve(jobType string) (JobPolicy, error) {
	switch jobType {
	case scriptpkg.TypeGenerate:
		return JobPolicy{Priority: 0, MaxRetries: 3}, nil
	default:
		return JobPolicy{}, fmt.Errorf("%w: %s", ErrUnknownJobType, jobType)
	}
}
