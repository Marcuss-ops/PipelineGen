// Package submission — factory.go owns the canonical construction
// of operations.SubmitRequest for script-generation commands.
//
// PR-SUBMISSION-FACTORY (July 2026): the HTTP transport no longer
// decides scope, job type, priority, max retries, or request-hash
// policy. It only binds the command; this factory turns the command
// into the application-layer SubmitRequest.
package submission

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Factory errors. These are typed so the HTTP transport can map
// them to the correct status code without inspecting strings.
var (
	// ErrInvalidEnvelopeIdentity is returned when the envelope
	// identity fingerprint is empty, which means the envelope
	// cannot be used for idempotency.
	ErrInvalidEnvelopeIdentity = errors.New("submission: envelope identity is empty")
	// ErrMarshalEnvelope is returned when the envelope cannot be
	// marshalled to JSON. This is a structural error after a
	// successful bind, so it is treated as an internal error.
	ErrMarshalEnvelope = errors.New("submission: failed to marshal envelope")
)

// SubmitRequestFactory builds operations.SubmitRequest values from
// GenerateCommand values. It encapsulates the policy, scope, job
// type, and hash derivation rules.
type SubmitRequestFactory struct {
	policy *JobPolicyResolver
}

// NewSubmitRequestFactory constructs the factory with the canonical
// policy resolver. The resolver dependency is explicit at the
// composition root; the default helper wires the canonical resolver.
func NewSubmitRequestFactory() *SubmitRequestFactory {
	return &SubmitRequestFactory{
		policy: NewJobPolicyResolver(),
	}
}

// NewSubmitRequestFactoryWithResolver constructs the factory with a
// custom policy resolver. Useful for tests that want to inject
// explicit policies.
func NewSubmitRequestFactoryWithResolver(policy *JobPolicyResolver) *SubmitRequestFactory {
	if policy == nil {
		policy = NewJobPolicyResolver()
	}
	return &SubmitRequestFactory{
		policy: policy,
	}
}

// Build creates an operations.SubmitRequest from a GenerateCommand.
// It marshals the envelope, derives the canonical request hash from
// the envelope identity, and resolves the job policy for the
// script.generate job type.
func (f *SubmitRequestFactory) Build(cmd GenerateCommand) (opsapp.SubmitRequest, error) {
	if cmd.Envelope == nil {
		return opsapp.SubmitRequest{}, fmt.Errorf("submission: envelope is required")
	}

	payload, err := json.Marshal(cmd.Envelope)
	if err != nil {
		return opsapp.SubmitRequest{}, fmt.Errorf("%w: %v", ErrMarshalEnvelope, err)
	}

	if adapters.BuildEnvelopeIdentity(cmd.Envelope) == "" {
		return opsapp.SubmitRequest{}, ErrInvalidEnvelopeIdentity
	}
	// Idempotency compares the complete request payload. The generation
	// fingerprint intentionally omits transport/editorial fields such as
	// title, but a reused key with a changed title is still a different
	// HTTP payload and must conflict.
	sum := digest.SHA256Bytes(payload)
	requestHash := sum

	policy, err := f.policy.Resolve(scriptpkg.TypeGenerate)
	if err != nil {
		return opsapp.SubmitRequest{}, err
	}

	return opsapp.SubmitRequest{
		Scope:          domainops.ScopeScriptGenerate,
		IdempotencyKey: cmd.IdempotencyKey,
		RequestHash:    requestHash,
		ForceRefresh:   cmd.Envelope.ForceRefresh,
		JobType:        scriptpkg.TypeGenerate,
		JobPayload:     payload,
		JobPriority:    policy.Priority,
		JobMaxRetries:  policy.MaxRetries,
	}, nil
}
