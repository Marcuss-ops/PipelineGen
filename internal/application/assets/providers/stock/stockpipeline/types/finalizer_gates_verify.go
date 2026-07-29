// Package stockpipeline — finalizer_gates_verify.go — fail-closed gates.
//
// VerifyChunks and VerifyMetadata are the §12-1 validation gates that
// reject incomplete chunk/metadata states before BuildFinalizationRequest
// composes the canonical FinalizationRequest.
package types

import (
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// VerifyChunks is the §12-1 fail-closed gate for chunked outputs.
// Pure function — easy TDD. Composition order:
//
//  1. empty chunks → ErrStockNoChunksFinalized
//  2. missing LocalPath on any chunk → ErrStockChunkNotFinalized
//  3. empty RemoteFileID on any chunk → ErrStockChunkNotFinalized
//  4. empty SHA256 on any chunk → ErrStockChunkHashMissing
//  5. malformed SHA256 on any chunk (len<64 / non-hex / uppercase) →
//     ErrStockChunkHashInvalid (Commit 0.2 P0 2.4 hardening)
//
// Order matters for the test assertion table (each test isolates
// one rule, not the chain).
//
// Commit 0.2 (godlike/07 fail-closed at the gate layer): SHA256
// strict-format validation is enforced here so the
// BuildFinalizationRequest IdempotencyKey derivation
// (prefix + sha[:16]) is no longer reachable on a short hash,
// eliminating the verdict's P0 #3 panic class.
func VerifyChunks(chunks []ChunkState) error {
	if len(chunks) == 0 {
		return ErrStockNoChunksFinalized
	}
	for _, c := range chunks {
		if c.LocalPath == "" {
			return fmt.Errorf("%w: chunk[%d] (artifact=%s) LocalPath empty",
				ErrStockChunkNotFinalized, c.Index, c.ArtifactID)
		}
		if c.RemoteFileID == "" {
			return fmt.Errorf("%w: chunk[%d] (artifact=%s) RemoteFileID empty",
				ErrStockChunkNotFinalized, c.Index, c.ArtifactID)
		}
		if c.SHA256 == "" {
			return fmt.Errorf("%w: chunk[%d] (artifact=%s) SHA256 must be computed BEFORE publish (P0 2.4)",
				ErrStockChunkHashMissing, c.Index, c.ArtifactID)
		}
		// Commit 0.2 P0 2.4 hardening: reject malformed SHA256 BEFORE
		// the panic site at BuildFinalizationRequest's composition.
		// Errors.Is(asset.ErrSHA256Invalid, ...) AND
		// errors.Is(ErrStockChunkHashInvalid, ...) both surface so
		// callers can probe either sentinel.
		if _, err := asset.ValidateSHA256(c.SHA256); err != nil {
			// godlike/07 typed-error contract (Commit 0.2 P0 2.4):
			// errors.Join preserves BOTH sentinels so callers can
			// errors.Is(ErrStockChunkHashInvalid) AND
			// errors.Is(asset.ErrSHA256Invalid) — fmt.Errorf supports
			// only one %w, so Join is the canonical multi-sentinel carrier.
			return errors.Join(
				ErrStockChunkHashInvalid,
				fmt.Errorf("chunk[%d] (artifact=%s)", c.Index, c.ArtifactID),
				err,
			)
		}
	}
	return nil
}

// VerifyMetadata is the §12-1 fail-closed gate for the per-run
// metadata.json. Symmetric to VerifyChunks but with metadata-specific
// flags. Pure function. Commit 0.2 hardening: SHA256 strict-format
// validation surfaces ErrStockMetadataHashInvalid for malformed
// digest inputs (len<64 / non-hex / uppercase) — same defence-in-depth
// contract as VerifyChunks.
func VerifyMetadata(m MetadataState) error {
	if m.LocalPath == "" {
		return fmt.Errorf("%w: LocalPath empty",
			ErrStockMetadataNotPublished)
	}
	if m.RemoteFileID == "" {
		return fmt.Errorf("%w: RemoteFileID empty (publish failed or missing)",
			ErrStockMetadataNotPublished)
	}
	if m.SHA256 == "" {
		return fmt.Errorf("%w: SHA256 must be computed BEFORE publish (P0 2.4)",
			ErrStockMetadataNotPublished)
	}
	// Commit 0.2 P0 2.4 hardening: malformed-SHA256 → ErrStockMetadataHashInvalid.
	if _, err := asset.ValidateSHA256(m.SHA256); err != nil {
		// godlike/07 typed-error: errors.Join preserves both sentinels.
		return errors.Join(ErrStockMetadataHashInvalid, err)
	}
	return nil
}
