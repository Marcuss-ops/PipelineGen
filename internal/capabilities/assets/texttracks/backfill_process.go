// Package texttracks — backfill_process.go (LEAF):
//
// Per-clip single-asset pipeline. The CORE orchestrator for the
// asset scope: takes one asset, decides source-READY vs
// NEEDS-ACQUIRE vs OUT-OF-LUCK, then fans out via the
// Materializer.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// "process ONE clip for text-track backfill" decision. The CLI
// drives the loop via backfill_run.go; the per-clip decision
// MUST NOT be re-implemented inline by other leaves.
//
// Cross-file callers (same package):
//   - backfill.go      : declares BackfillService struct +
//     BackfillAssetResult DTO that this
//     method populates.
//   - backfill_run.go  : calls ProcessAsset inside the per-
//     asset loop.
//   - backfill_acquire.go : owns tryAcquire which this method
//     invokes when the source is missing
//     AND the acquirer is wired.
package texttracks

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// assetContentHash returns the canonical content fingerprint for
// subtitle artifact identity. content_hash is the dispatcher-aware
// Tier 1 value. The legacy fallback is demolished (migration 281): an
// unknown content hash is honestly reported as unknown, never invented
// from a compatibility bucket.
func assetContentHash(a *asset.Asset) string {
	if a == nil {
		return ""
	}
	return a.ContentHash()
}
