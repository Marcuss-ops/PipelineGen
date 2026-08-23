// Package completion — complete_job_tx_artifact.go: artifact hash + map helpers.
//
// 2026-07-06 (Phase 4 decomposition): extracted from complete_job_service.go
// per the god-object decomposition plan. Pure code-motion, zero behavior
// changes.
//
// checkArtifactHashRoundTrip enforces godlike/07 no-fake-availability on
// artifact hashes: if a prior SUCCEEDED state has DIFFERENT sha256 for any
// artifact, surface the typed sentinel with drift summary.
//
// artifactMapEntries converts remote artifact manifests into the typed
// write surface for job_artifacts.
package completion

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// checkArtifactHashRoundTrip enforces godlike/07 no-fake-
// availability on the artifact hashes: if a prior SUCCEEDED
// state has DIFFERENT sha256 for any artifact, surface the typed
// sentinel with the drift summary.
//
// Returns the canonical ordered list of artifact IDs and a
// non-nil typed error if drift was detected. The caller MUST
// handle the error AFTER persisting the new job_artifacts row
// (so the prior & current state are both on disk for the audit
// surface).
func checkArtifactHashRoundTrip(incoming []job.RemoteArtifact, prior map[string]PriorArtifactHash) ([]string, error) {
	out := make([]string, 0, len(incoming))
	for _, a := range incoming {
		out = append(out, a.ID)
		p, ok := prior[a.ID]
		if !ok {
			continue // no prior → no drift possible
		}
		if p.SHA256 != a.SHA256 {
			return out, fmt.Errorf("%w: artifact[%s] prior_sha256=%q new_sha256=%q",
				remote.ErrRemoteArtifactHashMismatch, a.ID, p.SHA256, a.SHA256)
		}
	}
	return out, nil
}

// artifactMapEntries converts the request's RemoteArtifactManifest
// into the typed write surface for job_artifacts.
func artifactMapEntries(in []job.RemoteArtifact) []ArtifactMapEntry {
	out := make([]ArtifactMapEntry, len(in))
	for i, a := range in {
		out[i] = ArtifactMapEntry{
			ArtifactID:    a.ID,
			SHA256:        a.SHA256,
			RemoteAssetID: a.RemoteAssetID,
			Status:        a.Status,
		}
	}
	return out
}
