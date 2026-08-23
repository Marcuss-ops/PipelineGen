// Package finalizer — completion_idempotency.go (PR-GODOBJ-5-FINALIZER split)
//
// Hosts the idempotent-completion surface for the JobFinalizer. Five
// declarations move from the pre-split monolithic job_finalizer.go
// into this file:
//
//   - handleIdempotentCompletion — the already-SUCCEEDED branch
//     (called by the orchestrator at step 4 when jobRow.status ==
//     "SUCCEEDED"). Compares completion fingerprints to decide
//     idempotent success vs ErrCompletionConflict. P1.2: rebuilds
//     the optional-artifact audit report on the in-memory return so
//     dashboards reading `finResult` on a retry see the actual
//     outcome rather than a silent empty list (the cross-reference
//     is deterministic over the request struct — same input → same
//     report).
//
//   - artifactFingerprintEntry — typed-data struct used in the
//     completion fingerprint. Carries the minimum per-artifact
//     summary (ArtifactID + SHA256 + SourceVersion + FileID) so
//     two completions with the same result JSON but different
//     artifacts produce different fingerprints.
//
//   - computeCompletionFingerprint — SHA-256 hash of (sorted-by-
//     ArtifactID) artifact fingerprints + result Data. Sorted to
//     preserve determinism across artifact-order permutations.
//
//   - extractCompletionFingerprint — reads the completion_fingerprint
//     field out of a stored result JSON (the wrapped envelope that
//     markSucceeded writes).
//
//   - hashResult / hashJSONString — pure SHA-256-hex helpers.
//
// godlike/06 SSOT: this file is the canonical owner of "is this
// retry idempotent?". Callers MUST NOT compute their own
// fingerprint comparison outside handleIdempotentCompletion.
package finalizer

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ── Idempotent completion ───────────────────────────────────────────

// handleIdempotentCompletion checks whether an already-SUCCEEDED job
// was completed with the same result and artifacts. If so, it returns
// idempotent success. If the completion fingerprint differs, it returns
// ErrCompletionConflict.
//
// The completion fingerprint is a SHA-256 hash of the result manifest
// data + all artifact IDs (sorted) + SHA256s + source versions + remote
// asset IDs. Two completions with the same result but different artifacts
// produce different fingerprints and correctly fail as conflict.
func (f *Finalizer) handleIdempotentCompletion(
	ctx context.Context,
	row *jobRow,
	req *finalization.FinalizationRequest,
) (*finalization.FinalizationResult, error) {
	// P1.2: rebuild the optional-artifact audit report so callers
	// retrying an already-SUCCEEDED job (idempotent replay path)
	// see the same per-optional outcome on the in-memory return as
	// the first successful commit. Best-effort attach — a build
	// error here would mean the FIRST commit also failed, and the
	// stored fingerprint comparison below is the louder signal.
	//
	// The cross-reference is deterministic over the request struct
	// (P1.2 typed-data invariant: same OptionalDeclarations +
	// same Artifacts -> same OptionalArtifactReport), so the
	// recompute produces a byte-equivalent report to what was
	// persisted on the first commit. We attach it to the returned
	// FinalizationResult so dashboards reading `finResult` on a
	// retry see the actual outcome rather than a silent empty list.
	optionalReport, _ := f.buildOptionalArtifactReport(req, time.Now().UTC())

	requestFingerprint := computeCompletionFingerprint(req.Result.Data, req.Artifacts)

	// If the stored result is empty, we can't compare — treat as conflict.
	if row.resultJSON == "" || row.resultJSON == "{}" || row.resultJSON == "null" {
		f.log.Warn("job already SUCCEEDED with empty result, cannot verify idempotency",
			zap.String("job_id", req.Result.JobID),
		)
		return nil, finalization.NewFinalizationError(
			"COMPLETION_CONFLICT",
			"job already SUCCEEDED with empty/nil result — cannot verify idempotency",
			req.Result.JobID, req.Lease.Attempt, finalization.ErrCompletionConflict,
		)
	}

	storedFingerprint := extractCompletionFingerprint(row.resultJSON)
	if storedFingerprint == "" {
		// Legacy: no fingerprint stored — fall back to result-data-only hash.
		storedHash := hashJSONString(row.resultJSON)
		requestHash := hashJSONString(string(req.Result.Data))
		if storedHash == requestHash {
			f.log.Info("job already SUCCEEDED with same result hash — idempotent success (legacy fallback)",
				zap.String("job_id", req.Result.JobID),
				zap.String("hash", requestHash),
			)
			return &finalization.FinalizationResult{
				JobID:       req.Result.JobID,
				Status:      "SUCCEEDED",
				CompletedAt: time.Now().UTC(),
			}, nil
		}
		return nil, finalization.NewFinalizationError(
			"COMPLETION_CONFLICT",
			fmt.Sprintf("job already SUCCEEDED with different result (stored_hash=%s request_hash=%s)", storedHash, requestHash),
			req.Result.JobID, req.Lease.Attempt, finalization.ErrCompletionConflict,
		)
	}

	if storedFingerprint == requestFingerprint {
		f.log.Info("job already SUCCEEDED with same completion fingerprint — idempotent success",
			zap.String("job_id", req.Result.JobID),
			zap.String("fingerprint", requestFingerprint),
		)
		return &finalization.FinalizationResult{
			JobID:                  req.Result.JobID,
			Status:                 "SUCCEEDED",
			CompletedAt:            time.Now().UTC(),
			OptionalArtifactReport: optionalReport,
		}, nil
	}

	return nil, finalization.NewFinalizationError(
		"COMPLETION_CONFLICT",
		fmt.Sprintf("job already SUCCEEDED with different completion fingerprint (stored=%s request=%s)",
			storedFingerprint, requestFingerprint),
		req.Result.JobID, req.Lease.Attempt, finalization.ErrCompletionConflict,
	)
}

// ── Helpers ─────────────────────────────────────────────────────────

func hashJSONString(s string) string {
	if s == "" || s == "null" {
		s = "{}"
	}
	h := digest.SHA256Bytes([]byte(s))
	return h
}

// ── Completion fingerprint (§ 4.5 idempotency) ─────────────────────

// artifactFingerprintEntry is a deterministic per-artifact summary used
// in the completion fingerprint.
type artifactFingerprintEntry struct {
	ArtifactID    string `json:"artifact_id"`
	SHA256        string `json:"sha256"`
	SourceVersion int64  `json:"source_version"`
	FileID        string `json:"file_id"`
}

// computeCompletionFingerprint computes a SHA-256 hash of the result
// manifest data combined with all artifact identifiers (sorted by
// ArtifactID for determinism). Artifact IDs, SHA256 content hashes,
// source versions, and remote asset IDs (FileID) are all included so
// that two completions with the same result JSON but different artifacts
// produce different fingerprints.
func computeCompletionFingerprint(resultData json.RawMessage, artifacts []finalization.PublishedArtifact) string {
	sorted := make([]artifactFingerprintEntry, len(artifacts))
	for i, a := range artifacts {
		sorted[i] = artifactFingerprintEntry{
			ArtifactID:    a.ArtifactID,
			SHA256:        a.SHA256,
			SourceVersion: a.SourceVersion,
			FileID:        a.Location.FileID,
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ArtifactID < sorted[j].ArtifactID
	})

	payload, _ := json.Marshal(struct {
		Result    json.RawMessage            `json:"result"`
		Artifacts []artifactFingerprintEntry `json:"artifacts"`
	}{
		Result:    resultData,
		Artifacts: sorted,
	})

	return hashJSONString(string(payload))
}

// extractCompletionFingerprint attempts to extract the
// completion_fingerprint from a stored result JSON. Returns "" if the
// stored result predates the fingerprint wrapper (legacy format).
func extractCompletionFingerprint(storedJSON string) string {
	var wrapped struct {
		Data                  json.RawMessage `json:"data"`
		CompletionFingerprint string          `json:"completion_fingerprint"`
	}
	if err := json.Unmarshal([]byte(storedJSON), &wrapped); err != nil {
		return ""
	}
	return wrapped.CompletionFingerprint
}
