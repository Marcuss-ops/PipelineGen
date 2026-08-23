// Package verification owns the on-disk integrity gate for the
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave. The package is the trusted authority
// between the staged resolver (Azione 3) and the JobFinalizer
// CompleteWithArtifacts single-TX atomic write (Azione 6).
//
// VerifiedArtifact is the 12-field envelope produced by VerifyStagedArtifact;
// it is a DELIBERATE superset of finalization.PublishedArtifact (10 fields:
// ArtifactID, Kind, Filename, MIMEType, SizeBytes, SHA256, SourceVersion,
// Requirement, IdempotencyKey, Location) plus 2 verifier-specific extensions:
//
//   - LocalPath  : re-emitted by the verifier (already in finalization.VerifiedArtifact;
//     convenient for the cutover pipe to audit the on-disk seam without re-querying
//     the staged resolver)
//   - ValidatedAt: operator-visible timestamp surfaced (godlike/07 audit-pin)
//
// The 12-field shape is enforced by godlike/06 SSOT discipline: exactly the
// published-artifact 10 + 2 verifier extensions; no extra fields, no missing
// fields.
package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staged"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// VerifiedArtifact is the 12-field envelope produced by VerifyStagedArtifact
// on a successful (size + SHA-256) match. See package doc for the 2+10 split
// rationale vs finalization.PublishedArtifact.
//
// Field provenance:
//
//	ArtifactID     ← staged.StagedArtifact.AssetID  (claimed identity)
//	Kind           ← publisher fills downstream      (default ArtifactUnknown here)
//	Filename       ← filepath.Base(LocalPath)        (auditable basename)
//	LocalPath      ← staged.StagedArtifact.LocalPath  (VERIFIED via os.Stat)
//	MIMEType       ← publisher fills downstream      (default "" here)
//	SizeBytes      ← staged.StagedArtifact.SizeBytes (VERIFIED via os.Stat)
//	SHA256         ← staged.StagedArtifact.SHA256    (VERIFIED via recompute)
//	SourceVersion  ← publisher fills downstream      (default 0 here)
//	Requirement    ← finalization.ArtifactRequired  (publisher may downgrade)
//	IdempotencyKey ← derived SHA256String(artifactID|sha256) per P0.7 convention
//	Location       ← finalization.AssetLocation{}    (populated by Publish step)
//	ValidatedAt    ← verifier clock (operator-visible timestamp)
type VerifiedArtifact struct {
	ArtifactID     string                           `json:"artifact_id"`
	Kind           finalization.ArtifactKind        `json:"kind"`
	Filename       string                           `json:"filename"`
	LocalPath      string                           `json:"local_path"`
	MIMEType       string                           `json:"mime_type"`
	SizeBytes      int64                            `json:"size_bytes"`
	SHA256         string                           `json:"sha256"`
	SourceVersion  int64                            `json:"source_version"`
	Requirement    finalization.ArtifactRequirement `json:"requirement"`
	IdempotencyKey string                           `json:"idempotency_key"`
	Location       finalization.AssetLocation       `json:"location"`
	ValidatedAt    time.Time                        `json:"validated_at"`
}

// Verifier runs the on-disk integrity gate. It calls os.Stat (cheap-first
// size check) then internal/infrastructure/files.HashFile (sha256.New()) on
// the staged LocalPath, comparing the recomputed size+SHA against the
// StagedArtifact.Claims.
//
// The clock field defaults to time.Now and is injectable via NewVerifierWithClock
// for deterministic tests.
type Verifier struct {
	now func() time.Time
}

// NewVerifier constructs a Verifier with the default time.Now clock.
// Composition root in internal/app/ uses this for production wiring.
func NewVerifier() *Verifier {
	return &Verifier{now: time.Now}
}

// NewVerifierWithClock constructs a Verifier with an injectable clock.
// Used by tests to assert ValidatedAt byte-stability across consecutive calls.
// A nil clock defaults to time.Now (fail-open, never nil-panic).
func NewVerifierWithClock(clock func() time.Time) *Verifier {
	if clock == nil {
		clock = time.Now
	}
	return &Verifier{now: clock}
}

// VerifyStagedArtifact recomputes size + SHA-256 from disk and compares against
// the staged metadata in *staged.StagedArtifact (Azione 3).
//
// Failure modes (all godlike/07 typed-error contract — errors.Is compatible):
//
//   - nil *staged.StagedArtifact        → plain error "verification: nil staged artifact"
//   - empty AssetID or LocalPath         → plain error (call-site contract)
//   - os.Stat err                        → wrapped fmt.Errorf("...stat...: %w", err)
//   - size drift (observedSize != sa.SizeBytes)
//     → wrapped fmt.Errorf("...size drift...: %w", ErrStagedSizeMismatch)
//   - SHA-256 recompute err (I/O)        → wrapped fmt.Errorf("...hash recompute...: %w", err)
//   - SHA-256 drift (observedHash != sa.SHA256)
//     → wrapped fmt.Errorf("...checksum drift...: %w", ErrStagedChecksumMismatch)
//
// On match: returns the 12-field VerifiedArtifact envelope (above).
//
// Idempotency: two consecutive calls against an UNCHANGED on-disk file return
// the same SHA-256 byte-stable. Against a MODIFIED on-disk file, the verifier
// surfaces the drift via the typed error sentinels (no silent-success fallthrough
// per godlike/07 no-fake-availability).
func (v *Verifier) VerifyStagedArtifact(_ context.Context, sa *staged.StagedArtifact) (*VerifiedArtifact, error) {
	// _ (ctx) is reserved for future tracing/correlation propagation per P3
	// surface; the gate is currently synchronous. Future tracing hooks (otel,
	// correlation id propagation per pkg/corid) plumb through the named param.

	// 0. Input contract gate.
	if sa == nil {
		return nil, errors.New("verification.VerifyStagedArtifact: nil staged artifact")
	}
	if sa.AssetID == "" || sa.LocalPath == "" {
		return nil, fmt.Errorf(
			"verification.VerifyStagedArtifact[%s]: missing required field (asset_id=%q local_path=%q)",
			sa.AssetID, sa.AssetID, sa.LocalPath,
		)
	}

	// 1. Stat the file (cheap-first ordering: O(metadata) syscalls, runs BEFORE
	//    the SHA computation O(bytes)). Surface I/O err as plain wrap (NOT a
	//    typed drift — the file may simply not exist yet on a flaky Sender).
	fi, err := os.Stat(sa.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("verification.VerifyStagedArtifact[%s]: stat %q: %w",
			sa.AssetID, sa.LocalPath, err)
	}
	observedSize := fi.Size()

	// 2. Size check (gate #1 — cheap; abort before SHA work).
	if observedSize != sa.SizeBytes {
		return nil, fmt.Errorf(
			"verification.VerifyStagedArtifact[%s]: size drift: observed=%d staged=%d: %w",
			sa.AssetID, observedSize, sa.SizeBytes, ErrStagedSizeMismatch,
		)
	}

	// 3. SHA-256 recompute via internal/infrastructure/files (canonical
	//    hashutil per Azione 3 precedent). Streaming via crypto/sha256.New()
	//    to avoid loading the whole file in memory.
	observedHash, hashErr := files.SHA256File(sa.LocalPath)
	if hashErr != nil {
		return nil, fmt.Errorf("verification.VerifyStagedArtifact[%s]: hash recompute %q: %w",
			sa.AssetID, sa.LocalPath, hashErr)
	}

	// 4. SHA-256 check (gate #2 — expensive; abort with typed drift sentinel).
	if observedHash != sa.SHA256 {
		return nil, fmt.Errorf(
			"verification.VerifyStagedArtifact[%s]: checksum drift: observed=%s staged=%s: %w",
			sa.AssetID, observedHash, sa.SHA256, ErrStagedChecksumMismatch,
		)
	}

	// 5. Build the 12-field VerifiedArtifact envelope. The 10 fields from
	//    finalization.PublishedArtifact are populated from the staged input
	//    + sensible defaults; the 2 verifier extensions (LocalPath +
	//    ValidatedAt) are surfaced here.
	filename := filepath.Base(sa.LocalPath)
	return &VerifiedArtifact{
		ArtifactID:     sa.AssetID,
		Kind:           finalization.ArtifactKind(""), // empty-zero sentinel: "unknown kind" (godlike/07 no-fake-availability: this default does NOT impersonate any specific kind). Populated downstream by publisher (mimetype/dispatch; Azione 6+ — out-of-scope for Azione 4).
		Filename:       filename,
		LocalPath:      sa.LocalPath,
		MIMEType:       "", // populated by publisher (mimetype detect)
		SizeBytes:      sa.SizeBytes,
		SHA256:         sa.SHA256,
		SourceVersion:  0, // populated by publisher (cumulative version)
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: idempotencyKeyFor(sa.AssetID, sa.SHA256, filename),
		Location:       finalization.AssetLocation{}, // populated by Publish step (Drive upload)
		ValidatedAt:    v.now(),
	}, nil
}

// idempotencyKeyFor derives the canonical idempotency key per the P0.7
// deterministic convention. The 3-tuple (artifactID | sha256Hex | filename)
// returns the same hex across retries when ALL three fields are byte-stable.
// Critically, including `filename` in the triple means a re-stage that
// renames the file (.tmp → .bin) produces a DIFFERENT key — preserving the
// no-fake-availability contract under filesystem-rename scenarios where the
// underlying content is the same but the on-disk handle differs.
func idempotencyKeyFor(artifactID, sha256Hex, filename string) string {
	return files.SHA256String(artifactID + ":" + sha256Hex + ":" + filename)
}
