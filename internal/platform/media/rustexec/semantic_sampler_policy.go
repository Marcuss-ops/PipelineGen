package rustexec

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// semantic_sampler_policy.go wires the Go-owned sampler selection policy
// (data-driven keyword tables + optional diversity weight) into the Rust
// mediasampler. Go owns the composition values; the worker still owns its
// curated V1 defaults — an EMPTY policy never travels on the wire, so the
// historical behaviour is byte-identical when the operator configures
// nothing.
//
// The policy replaces tables WHOLESALE on the Rust side (never merged), so
// a configured table is auditable end-to-end. DiversityWeight is carried
// as a pointer: nil means "unspecified", the worker's default (0.025)
// applies; a configured zero must NOT be distinguishable from the curated
// default here — pass nil to defer to the worker.

// mediaSamplerSelectionPolicy is the shared wire shape every sampler
// operation carries (the worker validates weight bounds and vector
// similarity fail-closed, so the adapter only forwards resolved values).
type mediaSamplerSelectionPolicy struct {
	MismatchedSubjects []string `json:"mismatched_subjects,omitempty"`
	CompatibleKeywords []string `json:"compatible_keywords,omitempty"`
	ActionVerbs        []string `json:"action_verbs,omitempty"`
	ContextKeywords    []string `json:"context_keywords,omitempty"`
	DiversityWeight    *float32 `json:"diversity_weight,omitempty"`
}

// SamplerPolicy is the wire-agnostic Go-owned selection policy.
type SamplerPolicy struct {
	tables mediaSamplerSelectionPolicy
}

// NewSamplerPolicy maps the platform configuration into the wire-agnostic
// policy. An entirely empty configuration maps to the zero policy (no
// table override, no weight override). A configured DiversityWeight of 0
// is deliberately treated as "unspecified" with the worker default — a
// configured weight is always positive.
func NewSamplerPolicy(cfg config.SamplerPolicyConfig) SamplerPolicy {
	policy := SamplerPolicy{}
	if len(cfg.MismatchedSubjects) > 0 {
		policy.tables.MismatchedSubjects = cfg.MismatchedSubjects
	}
	if len(cfg.CompatibleKeywords) > 0 {
		policy.tables.CompatibleKeywords = cfg.CompatibleKeywords
	}
	if len(cfg.ActionVerbs) > 0 {
		policy.tables.ActionVerbs = cfg.ActionVerbs
	}
	if len(cfg.ContextKeywords) > 0 {
		policy.tables.ContextKeywords = cfg.ContextKeywords
	}
	if cfg.DiversityWeight > 0 {
		weight := float32(cfg.DiversityWeight)
		policy.tables.DiversityWeight = &weight
	}
	return policy
}

func (p SamplerPolicy) empty() bool {
	return len(p.tables.MismatchedSubjects) == 0 &&
		len(p.tables.CompatibleKeywords) == 0 &&
		len(p.tables.ActionVerbs) == 0 &&
		len(p.tables.ContextKeywords) == 0 &&
		p.tables.DiversityWeight == nil
}

// NewMediaSamplerWithPolicy builds the sampler adapter with the Go-owned
// selection policy bound once at the composition root: both the `select`
// and `select_group` operations carry the same tables.
func NewMediaSamplerWithPolicy(executor *Executor, policy SamplerPolicy) (*MediaSamplerAdapter, error) {
	adapter, err := NewMediaSamplerAdapter(executor)
	if err != nil {
		return nil, err
	}
	adapter.samplerPolicy = policy
	return adapter, nil
}

// applyTo threads the policy tables into a request struct that embeds
// mediaSamplerSelectionPolicy. Empty policies leave the request untouched.
func (p SamplerPolicy) applyTo(target *mediaSamplerSelectionPolicy) {
	if p.empty() {
		return
	}
	*target = p.tables
}

// optionalSimilarity maps the kernel candidate's optional embedding signal
// onto the wire candidate. Zero/invalid values stay unset on the wire so
// the worker keeps the legacy behavior instead of recalibrating.
func optionalSimilarity(value float64) *float32 {
	if value <= 0 || value > 1 {
		return nil
	}
	similarity := float32(value)
	return &similarity
}
