// Package scripts — generation_manifest.go is the canonical owner of
// the typed *job.ArtifactManifest construction (godlike/06 SSOT: one
// owner per fact). The handler invokes buildManifestFromArtifacts
// with the []job.Artifact slice returned by the service-core
// adapter (KILL K1: filesystem ops moved to
// adapters/artifacts_persistence.go so the handler does NOT touch
// os.MkdirAll / os.WriteFile / os.TempDir / ComputeSHA256-on-file).
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//
//	(1) This file has zero filesystem ops. The handler pattern is:
//	      handler.HandleSingle → svc.PersistGeneratedArtifacts(...)
//	                           → []job.Artifact
//	                           → buildManifestFromArtifacts(...) (this file)
//	                           → *job.ArtifactManifest
//	                           → handlerResult[job.ManifestKey] = manifest
//	    The handler does NOT call os.MkdirAll or os.WriteFile.
//	(2) The handler injects the manifest via handlerResult[
//	    job.ManifestKey] = manifest (the runner reads via
//	    job.Decode and uses the canonical lookup-key contract).
//
// godlike/07 typed-error contract: buildManifestFromArtifacts
// returns *job.ArtifactManifest (no error return — validation
// failures are propagated via the caller's typed-envelope-route,
// not by a Go-level error). If the caller wants to bail on
// validation, it calls manifest.Validate() itself (the typed-error
// probe is exposed there).
//
// godlike/07 honest-limitation: this file targets ~95 LoC. The
// artifact-id convention (jobID + ":" + kind), the per-kind
// attachment, the manifest-assemble pipeline are inherent to the
// §8.4 multi-artifact spec. Future SLIM pointers may move per-kind
// attachment helpers into per-kind files; for now we stay
// consolidated.
package jobs

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// buildManifestFromArtifacts materialises the canonical Sender-side
// ArtifactManifest sidecar from pre-computed []job.Artifact.
// Per §8.4 spec, the manifest SchemaVersion is V1, the JobID is
// carried per-job, and the WorkflowID is empty (the runner infers
// it from dispatch state).
//
// The caller is responsible for pre-phase steps:
//   - service-core persistence (adapters/artifacts_persistence.go).
//   - typed envelope construction (handled by the single executor
//     in generation_single_executor.go).
//   - manifest validation probe (callers may call manifest.Validate()
//     before injecting into handlerResult). Validation failures
//     are recoverable: the runner falls back to the typed envelope
//     when the manifest sidecar is missing or invalid.
//
// This function performs no I/O. The handler invokes it after the
// adapter returns the artifact slice.
func buildManifestFromArtifacts(jobID string, artifacts []job.Artifact) *job.ArtifactManifest {
	return &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "",
		JobID:         jobID,
		Artifacts:     artifacts,
	}
}
