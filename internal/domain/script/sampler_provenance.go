// Package script \u2014 sampler_provenance.go defines the audit
// trail for the FASE-8 single ClipSampler port.
//
// godlike/06 SSOT (one canonical owner per fact): the Sampler
// writes one GateProvenanceRecord per (candidate, gate) evaluation,
// regardless of pass/fail. The records form the full audit
// trail an operator can inspect post-hoc.
//
// Determinism: the type does NOT carry time.Time or any
// non-deterministic field. Same input \u2192 byte-identical Provenance.
// This matches the FASE-3 ClipPrePlanner contract pin.
package script

// GateProvenanceRecord is one (candidate, gate) evaluation. It is
// deterministic (no time.Time, no rand, no goroutine-ordering
// effects) so the same input yields byte-identical Provenance.
type GateProvenanceRecord struct {
	// SlotRef is the canonical slot identifier (e.g. "slot-3").
	SlotRef string

	// CandidateID is the canonical media_assets.id reference the
	// gate evaluated. Empty ClipIDs are skipped upstream so this
	// is always populated when a record exists.
	CandidateID string

	// GateName identifies the gate (e.g. "topic_relevance",
	// "duration", "no_duplicates", "format_compatible"). Stable
	// across runs and across the caller-tag namespace.
	GateName string

	// Passed is the gate's verdict. false = gate rejected the
	// candidate; the sampler drops it from the result. true =
	// gate accepted the candidate for THIS individual criterion;
	// the candidate is only emitted when ALL gates pass.
	Passed bool

	// Reason is a short human-readable explanation of the
	// verdict. Empty when Passed is true (most trivial passes).
	// MUST NOT embed source-text content (no source-text leak
	// through audit envelopes per godlike/06 SSOT).
	Reason string
}

// SamplerProvenance is the audit trail slice produced by a single
// Select(...) call. The slice is ordered by candidate iteration
// order (the order candidates were passed in by the caller), then
// by gate-execution order within each candidate (canonical
// 10-gate order).
//
// Operators inspect this slice to answer: "for slot-3, did the
// chosen candidate pass gate X? Why not?".
type SamplerProvenance struct {
	Records []GateProvenanceRecord
}
