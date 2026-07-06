// Package finalization defines the canonical domain contracts for the
// transactional job-finalization spine (Spina Dorsale, Fase 1, July 2026).
//
// Every pipeline capability (YouTube, stock, Artlist, images, voiceover,
// sound effects, uploads) converges on the same spine:
//
//	Capability
//	    ↓
//	Produce VerifiedArtifact (validate + hash)
//	    ↓
//	Publish via ArtifactPreparationService (publish idempotente)
//	    ↓
//	JobFinalizer.CompleteWithArtifacts (transazione atomica)
//	    ↓  BEGIN
//	    ↓    write asset canonico
//	    ↓    write asset location
//	    ↓    write source version
//	    ↓    write result manifest
//	    ↓    write job artifacts
//	    ↓    write outbox events
//	    ↓    job → SUCCEEDED
//	    ↓  COMMIT
//	    ↓
//	Outbox consumer → Qdrant / proiezioni esterne
//
// A job never becomes SUCCEEDED before all required artifacts are
// finalised. The completion, asset records, locations, and outbox events
// are written in the SAME SQLite transaction.
//
// File organisation (LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical
// band slice): the 674-LOC types.go has been split into 15 per-type files
// (each owned by ONE canonical type per godlike/06 SSOT
// one-canonical-owner-per-fact). The Package-level documentation lives
// here in doc.go; per-type godoc lives in the per-type files.
//
// Required vs Optional artifacts (P1.2, July 2026):
//
//   - Required artifacts block job completion. If any required artifact
//     is missing from the publish-side at completion time, the
//     JobFinalizer returns ErrRequiredArtifactMissing.
//   - Optional artifacts are non-blocking. JobFinalizer records every
//     optional artifact in FinalizationResult.OptionalArtifactReport
//     (a typed-data audit sidecar) regardless of outcome. The optional
//     artifact's status — Finalized / Missing / Failed — is preserved
//     for operator investigation. Optional failures DO NOT fail the job.
//
// Canonical reference: Piano d'Azione Completo § 4.
package finalization
