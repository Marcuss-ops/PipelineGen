// Package reconciler — drift-detection phase (Qdrant scroll gate).
//
// service_drift.go owns the Phase 2 (drift detection) phase of the
// reconciler pipeline. The Qdrant scroll-with-fail-closed-gates
// mechanic lives here as the SOLE canonical owner (godlike/06 SSOT
// one-canonical-owner-per-fact) — no other source file in this
// package or elsewhere recomputes the scroll cursor exhaustion
// invariant or the duplicate-asset_id detection.
//
// The classification phase lives in scanner.go(::classify). The
// projection phase lives in service_projection.go(::applyRepair +
// ::legacyStripTotals). The orchestrator that calls scrollAll,
// classify, and applyRepair in sequence lives in service.go
// (::(*Service).Reconcile + ::NewServiceFromDeps). This file is
// strictly between scanner.go (classification) and
// service_projection.go (repair dispatch).
//
// **Phase 2 invariants enforced here** (PR 10, June 2026 fail-closed
// posture; see service.go::Reconcile Phase 2 call site for the
// rationale + scrollAll godoc for the gate-by-gate spec):
//
//   - Page error → return partial data DISCARDS + non-nil err.
//   - maxPages cap hit with trailing NextOffset → same.
//   - Trailing NextOffset at clean-exit loop → same.
//   - expectedAssets > 0 ∧ PointsMissingAssetID >= 1 → same.
//
// All four gates funnel into a single `return out, dupes, errs, err`
// shape so Reconcile can treat ANY non-nil err as "abort classification
// and emit fatal-Phase-2 metric". Partial data is NEVER returned to
// the caller (the gate (a) `out` map is discarded on err return so
// callers cannot classify against an incomplete picture, even by
// accident).
//
// The duplicate-detection Task-6 bookkeeping (`dupes` map of
// asset_id → []pointWithID) is co-located here because the duplicate
// "first-occurrence wins" decision is part of the scroll pattern,
// not the classification phase. scanner.go::classify reads the
// `dupes` map AND the `out` map and emits KindDuplicate
// classifications accordingly.
package reconciliation

import (
	"context"
	"fmt"
)

// scrollAll drives the Qdrant scroll API to completion with PR 10's
// fail-closed gates. expectedAssets = len(sqliteSet) — when > 0,
// the scan is expected to yield at least one decoded Qdrant point
// AND every decoded point's payload MUST carry a non-empty
// asset_id.
//
// Fail-closed gates (each returns a non-nil error from scrollAll and
// causes Reconcile to abort the run BEFORE classifying):
//
//	(a) any scroll page error                         — fatal
//	(b) maxPages reached with more pages trailing     — fatal (capped)
//	(c) trailing NextOffset at end of clean-exit loop — fatal
//	    (defence-in-depth — reachable only if someone raises
//	    maxPages without also bounding the trailing offset)
//	(e) expectedAssets > 0 ∧ PointsMissingAssetID >= 1 — fatal
//
// PR 10 unifies these into a single hard-error return; the QDRANT-005B
// period's soft path that returned partial data with nil err is gone.
// Partial data is discarded (`out`) so the caller cannot classify
// against an incomplete picture even by accident.
//
// On clean exit: returns (out, [], nil). Caller flips
// ScannedTotals.CompleteScan = true.
//
// Note on the original "zero Qdrant points with expected > 0" gate:
// it was considered and dropped in PR 10. The reconcile-repair use
// case IS "SQLite has N rows, Qdrant has 0 matching" — blocking it
// would prevent the reconciler from doing its primary job. Operators
// who suspect a misconfiguration (wrong alias, wrong collection)
// should still rely on the count gates in the post-reindex verifier
// (PR 12) — Qdrant.CountPoints reports N above the rest API which is
// a stronger signal than scroll returning zero.
func (s *Service) scrollAll(ctx context.Context, collection string, batchSize int, expectedAssets int) (map[string]pointWithID, map[string][]pointWithID, []string, error) {
	out := make(map[string]pointWithID)
	dupes := make(map[string][]pointWithID)
	var errs []string
	var missingIDs int
	var lastNextOffset string
	const maxPages = 400 // safety cap (~200k points at batch=500)
	for i := 0; i < maxPages; i++ {
		page, err := s.qdrant.ScrollPoints(ctx, collection, lastNextOffset, batchSize)
		if err != nil {
			// Gate (a): ANY scroll page error is fatal. PR 10 closes
			// the QDRANT-005B regression that returned partial data
			// with nil err after the first page.
			errs = append(errs, fmt.Sprintf("scroll page %d: %v", i, err))
			return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: scroll page %d failed: %w", i, err)
		}
		if len(page.Items) == 0 {
			break
		}
		for _, p := range page.Items {
			assetID, ok := p.Payload["asset_id"].(string)
			if !ok || assetID == "" {
				// Gate (e) antecedent: empty/missing asset_id is a
				// canonical-decode failure. We do NOT poison the map
				// with a synthetic-key entry (would mask MissingIDs
				// later). Continue scanning so the trailing-NextOffset
				// gate can still fire independently; the gate (e)
				// check at the end rolls up the count.
				missingIDs++
				errs = append(errs, fmt.Sprintf("page %d: point %q missing asset_id (fail-closed)", i, p.ID))
				continue
			}
			// Task 6: detect duplicates — when the same asset_id already
			// exists in the output map, the new point is a duplicate.
			// The first occurrence stays in the map; subsequent ones
			// are collected for classification as KindDuplicate.
			if _, exists := out[assetID]; exists {
				// Task 6: duplicate point — same asset_id already exists
				// in the output map (first occurrence is canonical).
				// Extra copies are flagged as KindDuplicate.
				dupes[assetID] = append(dupes[assetID], pointWithID{ID: p.ID, Payload: p.Payload})
				continue
			}
			out[assetID] = pointWithID{ID: p.ID, Payload: p.Payload}
		}
		if page.NextOffset == "" {
			lastNextOffset = ""
			break
		}
		lastNextOffset = page.NextOffset
		if i == maxPages-1 {
			// Gate (b): maxPages cap hit with trailing NextOffset.
			errs = append(errs, fmt.Sprintf("scroll iteration cap %d reached with NextOffset=%q; remaining points unsampled (fail-closed)", maxPages, lastNextOffset))
			return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: maxScrolls=%d reached with NextOffset still trailing (more points unsampled)", maxPages)
		}
	}

	// Gate (c) — defence in depth. Reaching this branch means the
	// for-loop's `if page.NextOffset == "" break` did NOT fire yet
	// (i.e. every visited page returned a non-empty NextOffset). The
	// inner `i == maxPages-1` early-return above prevents this at
	// the current cap, but if the cap is ever raised without also
	// bounding the trailing offset, this branch is the safety net.
	if lastNextOffset != "" {
		errs = append(errs, fmt.Sprintf("QDRANT-fail-closed: trailing NextOffset=%q at end of scroll loop", lastNextOffset))
		return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: trailing NextOffset=%q after cursor exhaust", lastNextOffset)
	}
	// Gate (e): missing-asset_id count when expected > 0. Even if
	// some points decoded fine, the undecoded ones are unknowns —
	// surfacing ZERO Orphan IDs in that case would falsely reassure
	// the operator. Discard partial data and abort.
	if expectedAssets > 0 && missingIDs > 0 {
		errs = append(errs, fmt.Sprintf("QDRANT-fail-closed: %d points missing asset_id (canonical decode failure on %d/%d assets)", missingIDs, missingIDs, expectedAssets))
		return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: %d Qdrant points with empty/missing asset_id (canonical decode failure)", missingIDs)
	}
	return out, dupes, errs, nil
}
