// Package scripts \u2014 clip_sampler_registry.go is the composition-
// root holder of the ClipSampler port.
//
// godlike/06 SSOT: the registry is the SINGLE wiring point for
// the sampler impl. The composition root instantiates it once
// (NewClipSamplerRegistry()) and passes it to every resolver
// that needs selection logic; the registry holds exactly ONE
// instance of NewDefaultClipSampler().
//
// godlike/06 SSOT (third bullet) + AGENTS.md Pattern 0: the
// registry is a single field on each resolver, NOT a per-source
// map. The user's "vietati tre sampler separati" constraint is
// enforced structurally: SamplerFor(caller) returns the SAME
// impl for every caller; distinct caller tags only drive audit
// logging, not selection behavior.
//
// godlike/07 NO-FAKE-AVAILABILITY: SamplerFor panics on a nil
// receiver rather than returning a degraded no-op sampler. A
// nil receiver means the composition root failed to wire the
// registry; surfacing it loudly avoids silent register-or-no-op
// fallbacks that would mask wiring bugs.
package usecase

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// ClipSamplerRegistry holds the SINGLE canonical sampler impl.
// Exposed to resolvers via the SamplerFor(caller) accessor; the
// caller tag is propagated to log output only.
type ClipSamplerRegistry struct {
	sampler ports.ClipSampler
}

// NewClipSamplerRegistry constructs the registry owning the
// default sampler. Composition root calls this exactly once at
// startup. The wire_script_resolvers.go factory instance is the
// canonical caller; tests construct their own per-test fixtures.
func NewClipSamplerRegistry() *ClipSamplerRegistry {
	return &ClipSamplerRegistry{
		sampler: NewDefaultClipSampler(),
	}
}

// SamplerFor returns the SINGLE canonical sampler instance; the
// caller tag is purely informational and propagates to the
// sampler's per-call logs. Distinct caller tags do NOT select
// different samplers \u2014 godlike/06 SSOT forbids it.
//
// Fail-closed (godlike/07): a nil receiver panics with a typed
// message so wiring bugs surface at first use rather than
// producing silent degraded selection.
func (r *ClipSamplerRegistry) SamplerFor(caller string) ports.ClipSampler {
	if r == nil {
		panic(fmt.Sprintf(
			"clip sampler registry: nil receiver for caller=%q (composition-root wiring missing; godlike/07 fail-closed)",
			caller))
	}
	if r.sampler == nil {
		panic(fmt.Sprintf(
			"clip sampler registry: sampler not wired for caller=%q (composition-root wiring missing; godlike/07 fail-closed)",
			caller))
	}
	// Caller-tag constants are exported below; the registry does
	// NOT validate the string \u2014 the caller is responsible for
	// using the canonical tag, and the per-call log carries the
	// value verbatim.
	return r.sampler
}

// Canonical caller tags. Resolver code references these consts
// instead of magic strings so a typo never reaches prod.
const (
	ClipSamplerCallerSearch  = "search"
	ClipSamplerCallerCatalog = "catalog"
	ClipSamplerCallerCurate  = "curate"
)
