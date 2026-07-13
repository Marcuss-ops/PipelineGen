// Package jobs — registry_integrity.go (PR-INTEGRITY-VERIFIER-EXTRACT, July 2026).
//
// Per-family split (mirrors registry_voiceover.go, registry_script.go,
// etc.). This file owns the canonical job-type identifiers and the
// Register entry shape for the IntegrityVerifier + Cleanup step
// (extracted from the coordinator — see
// internal/application/integrity/verifier.go).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// `integrity.verify` and `asset.cleanup` job-type identifiers. No
// other file may declare string constants with these values. A
package jobs

const (
	// TypeIntegrityVerify is the canonical jobType for the
	// integrity verifier. The coordinator enqueues this job
	// after AssetCommitter.Commit succeeds; the dispatcher
	// routes it to the IntegrityVerifier worker which produces
	// the VerificationResult that gates the next step.
	TypeIntegrityVerify = "integrity.verify"
	// TypeAssetCleanup is the canonical jobType for the
	// gated cleanup step. The integrity worker enqueues it
	// AFTER producing a successful VerificationResult; the
	// dispatcher routes it to the cleanup worker which
	// releases the staged source via SourceStager.Release.
	TypeAssetCleanup = "asset.cleanup"
)

// registerIntegrityEntries adds the canonical integrity + cleanup
// job-type entries to the registry. Called by Compose() at the
// end of the canonical construction path so future contributors
// adding a new job family follow the same pattern.
//
// Operational shape:
//   - Timeout: 10 minutes (inherited from DefaultTimeout) — the
//     duration is dominated by the worst-case Drive metadata
//     probe (network RTT + sparse pre-fetch). Aligns with the
//     asset.index.requested timeout surface (same wire family).
//   - MaxRetries: 3 (inherited from DefaultMaxRetries) —
//     transient network failures during the probe are
//     canonical-retryable per godlike/07.
//   - Queue: DefaultQueue — both jobs are CPU-light; they share
//     the queue with other low-CPU workers.
//   - Concurrency: DefaultConcurrency — both jobs are I/O-bound
//     (Drive HTTP + local fs); concurrency is bounded.
func registerIntegrityEntries(r *Registry) {
	r.Register(RegistryEntry{
		Type:              TypeIntegrityVerify,
		Timeout:           0, // 0 → canonical default (see JobTimeout accessor)
		DefaultMaxRetries: 0, // 0 → canonical default (see DefaultMaxRetries accessor)
		Queue:             DefaultQueue,
		Concurrency:       DefaultConcurrency,
		ProducesArtifacts: false,
	})
	r.Register(RegistryEntry{
		Type:              TypeAssetCleanup,
		Timeout:           0, // 0 → canonical default (see JobTimeout accessor)
		DefaultMaxRetries: 0, // 0 → canonical default (see DefaultMaxRetries accessor)
		Queue:             DefaultQueue,
		Concurrency:       DefaultConcurrency,
		ProducesArtifacts: false,
	})
}
