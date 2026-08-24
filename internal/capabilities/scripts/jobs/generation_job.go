// Package jobs provides the script.generate job-system handler.
//
// PR-GODOBJ-4-SCRIPTS-GENERATION-JOB (July 2026) splits the prior
// monolithic generation_job.go (713 LoC) into canonical single-owner
// files per godlike/06 SSOT (one owner per fact):
//
//   - generation_handler.go       — broker entry + Handle (decode +
//     dispatch) + handleSingle (single-item path) + handleBatch
//     (multi-item path) — KILL-K2: single + batch are 3 SEPARATE
//     methods, NOT inline `if len(env.Items) == 1` branching.
//
//   - generation_outcome.go       — PURE ClassifyGenerationOutcome +
//     ClassifySingleOutcome returning typed Outcome enum + Diagnostic
//     (no handlers, no log writes, no I/O).
//
//   - generation_result_mapper.go — envelope builders
//     (buildSingleSuccessEnvelope + buildSingleFailureEnvelope +
//     singleEnvelopeResult + buildEnvelopeResult) + the toMap marshal/
//     unmarshal bridge to the broker map contract.
//
//   - generation_manifest.go      — buildManifestFromArtifacts
//     PURE typed *job.ArtifactManifest constructor (no FS ops).
//
//   - generation_registration.go  — (h *GenerateJobHandler).RegisterJobs
//     fail-fast typed NPE on nil broker (godlike/07 P1 Issue 7).
//
//   - ../adapters/artifacts_persistence.go — KILL-K1 service-core
//     landing site: PersistGeneratedArtifacts(ctx, jobID, result)
//     owning ALL filesystem ops (os.MkdirAll / os.WriteFile /
//     os.TempDir / ComputeSHA256-on-file). The handler does NOT touch
//     the filesystem; it calls the adapter.
//
// This file is retained as a package-doc sentinel only. ZERO logic,
// ZERO declarations, ZERO imports. The legacy GenerateJobHandler
// struct + NewGenerateJobHandler ctor + Handle/RegisterJobs methods
// live in generation_handler.go (canonical broker-entrypoint owner
// per godlike/06 SSOT). External callers that referenced this file
// 's symbols continue to compile because the package path is
// unchanged and the symbols now live in the same package (just in
// other files).
//
// Honest-limitation: this file is intentionally a 32-LoC
// package-doc-only sentinel — well under the 66-LoC Check 44 cap.
// Future contributors MUST NOT add runtime declarations here; use
// the canonical owner files above.
package jobs
