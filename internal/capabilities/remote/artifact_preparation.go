// Package remote — artifact_preparation.go (Drive cutover P0.5, July 2026).
//
// VerifyArtifact performs real file-level verification of a
// finalization.VerifiedArtifact: checks that the on-disk file exists,
// has the expected size, has the correct SHA-256 content hash, and
// carries a deterministic IdempotencyKey derived from
// SHA-256(jobID:artifactID:sha256:sourceVersion).
//
// This supersedes the lightweight non-empty-string checks in
// internal/application/assets/finalizer/artifact_preparation.go::validate,
// which never called os.Stat / sha256 on the actual file — the
// "verified" in VerifiedArtifact was misleading. P0.5 makes it real.
//
// godlike/07 typed-error contract:
//
//	ErrArtifactVerificationFailed carries a human-readable detail
//	string enumerating every field mismatch. Callers errors.Is
//	against the sentinel to branch on verification failures.
package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ── Sentinel ────────────────────────────────────────────────────────

// ErrArtifactVerificationFailed is the typed sentinel returned when
// on-disk verification of a VerifiedArtifact fails. The error message
// carries detail enumerating every field mismatch (missing-file,
// size-mismatch, hash-mismatch, idempotency-key-mismatch) so operators
// can see the full picture in one error line.
var ErrArtifactVerificationFailed = errors.New("artifact verification: on-disk file does not match VerifiedArtifact claims")

// ── VerifyArtifact ──────────────────────────────────────────────────

// VerifyArtifact performs on-disk verification of an artifact:
//
//  1. os.Stat(LocalPath) — file must exist; returned size must equal
//     artifact.SizeBytes.
//  2. sha256(LocalPath) — actual content hash must equal artifact.SHA256.
//  3. IdempotencyKey — must equal SHA-256(jobID:artifactID:sha256:version),
//     the deterministic key derivation for idempotent publication.
//
// Returns nil when all checks pass. Returns ErrArtifactVerificationFailed
// (wrapping a descriptive string) when any check fails.
func VerifyArtifact(ctx context.Context, jobID string, artifact finalization.VerifiedArtifact) error {
	var failures []string

	// (1) File existence + size.
	fi, err := os.Stat(artifact.LocalPath)
	if err != nil {
		failures = append(failures, fmt.Sprintf("file not found: %s (%v)", artifact.LocalPath, err))
	} else {
		if fi.Size() != artifact.SizeBytes {
			failures = append(failures, fmt.Sprintf("size mismatch: on-disk=%d claimed=%d", fi.Size(), artifact.SizeBytes))
		}

		// (2) Content hash — measured as finalize.artifact_hash so the
		// SHA-256 cost is separated from the validation envelope in the
		// RunReport (post_writer_finalize no longer hides it). Stage comes
		// from the caller's context; default publish for non-finalize
		// callers.
		var actualSHA string
		if hashErr := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     kernobs.StageOrDefault(ctx, kernobs.StagePublish),
			Component: kernobs.ComponentName("finalize"),
			Operation: kernobs.OperationName("artifact_hash"),
			Items:     1,
			Bytes:     artifact.SizeBytes,
		}, func(opCtx context.Context) error {
			var err error
			actualSHA, err = sha256File(artifact.LocalPath)
			return err
		}); hashErr != nil {
			failures = append(failures, fmt.Sprintf("cannot compute SHA-256: %v", hashErr))
		} else if actualSHA != artifact.SHA256 {
			failures = append(failures, fmt.Sprintf("SHA-256 mismatch: on-disk=%s claimed=%s", actualSHA, artifact.SHA256))
		}
	}

	// (3) IdempotencyKey derivation.
	// Skipped when jobID is empty (caller hasn't threaded the job identity
	// yet — e.g. finalizer.validate during pre-publish). When jobID is
	// provided, the key MUST match the deterministic derivation.
	if jobID != "" {
		expectedKey := deriveIdempotencyKey(jobID, artifact.ArtifactID, artifact.SHA256, artifact.SourceVersion)
		if artifact.IdempotencyKey != expectedKey {
			failures = append(failures, fmt.Sprintf("idempotency-key mismatch: expected=%s got=%s", expectedKey, artifact.IdempotencyKey))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%w: artifact=%s job=%s: %v",
			ErrArtifactVerificationFailed, artifact.ArtifactID, jobID, failures)
	}
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────

// sha256File computes the hex-encoded SHA-256 digest of the file at
// path. Uses raw crypto/sha256 + os.Open (no import from
// internal/infrastructure — domain-layer layering rule).
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	return digest.SHA256Reader(f)
}

// deriveIdempotencyKey computes the deterministic idempotency key:
//
//	SHA-256(jobID:artifactID:sha256:sourceVersion)
//
// The colon-delimited input string is hashed via SHA-256 and returned
// as a 64-char hex string. Same inputs → same key; different inputs →
// different key.
func deriveIdempotencyKey(jobID, artifactID, sha256hex string, sourceVersion int64) string {
	input := jobID + ":" + artifactID + ":" + sha256hex + ":" + strconv.FormatInt(sourceVersion, 10)
	return digest.SHA256String(input)
}
