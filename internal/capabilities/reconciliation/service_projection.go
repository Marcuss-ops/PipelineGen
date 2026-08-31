// Package reconciler — projection phase (apply-repair + payload-mapper).
//
// service_projection.go owns the Phase 4 (projection) phase of the
// reconciler pipeline. After Phase 1 (SQLite snapshot in service.go),
// Phase 2 (Qdrant scroll in service_drift.go), Phase 3 (classification
// in scanner.go), this file's `applyRepair` enumerates the typed
// classification pairs and dispatches the canonical outbox reparents
// and payload key strips.
//
// **godlike/06 SSOT strict ownership**: this file is the SOLE owner
// of:
//
//   - `applyRepair`: the dispatch router from Classification →
//     RepairSummary + per-call error slice + legacyStripTotals. No
//     other site builds the RepairSummary counters.
//   - `legacyStripTotals`: the typed envelope tracking
//     (drive_link, local_path, status) per-key cleanups. The
//     struct field names are referenced by metrics.MediaMetrics
//     (at the consumer site in service.go::Reconcile Phase 4)
//
// **Pre-PR-11 dedupe / supersede gate**: the `contentHash` argument
// in the `outbox.EnqueueReindex` call carries the canonical
// supersede-gate key (assetID:targetSchema:contentHash). The
// worker-side gate (PR-11, June 2026) reads the same triple so this
// dispatch surface IS the canonical supersede-projection. Empty
// contentHash is rare but legal for newly-inserted rows that haven't
// been hashed yet — the canonical envelope builder rejects empty
// hashes, so Production will observe dispatch errors rather than
// producing dedupe-broken events.
//
// **Phase 4 invariants enforced here**:
//
//   - Per-kind dispatch policy is exhaustive over the 6 reindex
//     kinds (Missing, VersionStale, PayloadIncomplete,
//     LifecycleMismatch, WorkspaceMismatch, NonCanonicalPointID).
//   - Per-kind dispatch policy is exclusive over the 1 delete kind
//     (Orphan).
//   - DELETE_USER_REVIEW is preserved for Duplicate /
//     MissingVectors / DimensionMismatch — no automated repair,
//     surfaced to operators via report.Classifications.
//
// **Out of scope for this PR**: introducing new typed envelopes,
// renaming client APIs, or splitting per-kind dispatch into helper
// methods. The split is pure code-motion per godlike/07
// minimum-blast-radius.
package reconciliation

import (
	"context"
	"fmt"
)

// legacyStripTotals tracks legacy-key cleanups per key observed at
// scan time (not per DeletePayloadKeys call).
//
// INVARIANT: driveLinksStripped and localPathsStripped are NOT
// necessarily equal. They reflect payload content at scan time:
//   - driveLinksStripped = points whose payload actually carried
//     drive_link at scan time.
//   - localPathsStripped = points whose payload actually carried
//     local_path at scan time.
//
// The Qdrant REST DeletePayloadKeys call passes both keys because
// the REST shape accepts a list per call (no per-point granularity),
// but the on-wire cost of stripping both keys at once is identical
// to stripping one — keeping the call shape unchanged preserves
// cleanup throughput while letting dashboards tell apart
// "drive_link-only legacy migrations" from "both-keys legacy".
//
// statusKeysStripped counts LifecycleKeyLegacy points — these come
// from a SEPARATE DeletePayloadKeys call with key=["status"],
// distinct from the locator call.
//
// All three counters are bumped based on what the scanner observed,
// never per DeletePayloadKeys call.
//
// **godlike/06 SSOT**: this struct lives next to its sole producer
// (applyRepair below) so a future agent adding a new legacy key
// (e.g. `s3_path`) adds the field here in lockstep with the dispatch
// arm in applyRepair. Splitting struct ↔ producer across files would
// violate godlike/06 "one canonical owner per fact".
type legacyStripTotals struct {
	statusKeysStripped int
	driveLinksStripped int
	localPathsStripped int
}

// applyRepair dispatches the repair actions. Returns the run summary
// plus any non-fatal dispatch errors (so the report can include them
// without aborting mid-run).
//
// Per-kind dispatch policy:
//   - KindMissing, KindVersionStale, KindPayloadIncomplete,
//     KindLifecycleMismatch, KindWorkspaceMismatch,
//     KindNonCanonicalPointID: outbox.EnqueueReindex(assetID, contentHash).
//     The contentHash is propagated from Classification.ContentHash
//     (the scanner-side hash, sourced from media_assets.metadata_json.$
//     .content_hash via SQLiteAssetStore.ListAssetsForReconcile).
//     It feeds BOTH the PR-11 outbox dedupe key
//     (assetID:targetSchema:contentHash) AND the worker's source_version
//     supersede gate. Empty contentHash is rare but legal for newly-
//     inserted rows that haven't been hashed yet — the canonical
//     envelope builder rejects empty hashes, so Production will observe
//     dispatch errors rather than producing dedupe-broken events.
//   - KindOrphan: outbox.EnqueueDelete(assetID). Deterministic key.
//   - KindLifecycleKeyLegacy: payload.DeletePayloadKeys([collection],
//     ["status"], [pointIDs]). One batch per category.
//   - KindLocatorLegacy: payload.DeletePayloadKeys([collection],
//     ["drive_link", "local_path"], [pointIDs]). One batch per category.
//
// Returns legacyStripTotals so the caller can emit per-key metrics.
// Per-call dispatch error from outbox/payload is appended to repairErrs
// and counted in the report, but does NOT bump Applied — Applied is
// computed from the RepairSummary counters only.
func (s *Service) applyRepair(ctx context.Context, collection string, pairs []Classification) (RepairSummary, []string, legacyStripTotals) {
	summary := RepairSummary{}
	var errs []string
	var legacyStrips legacyStripTotals

	var legacyKeyPoints []string
	var locatorPoints []string
	var locatorKeysByPointID = map[string][]string{}

	for _, c := range pairs {
		switch c.Kind {
		case KindMissing,
			KindVersionStale,
			KindHashMismatch,
			KindPayloadIncomplete,
			KindLifecycleMismatch,
			KindWorkspaceMismatch,
			KindNonCanonicalPointID:
			// Card 7.1 (July 2026): the admin reindex path passes
			// force=true. The reconciler is only invoked from
			// `reconcile-qdrant --apply` (an admin tool) so the
			// force flag is always true here. The worker bypasses
			// the source_version supersede gate for these
			// admin-driven repairs. A future split between
			// production-repair and admin-repair can introduce a
			// separate "force=false" production-repair dispatch
			// path without changing this caller.
			if err := s.outbox.EnqueueReindex(ctx, c.AssetID, c.ContentHash, true); err != nil {
				errs = append(errs, fmt.Sprintf("enqueue reindex %s: %v", c.AssetID, err))
				continue
			}
			summary.ReindexEnqueued++

		case KindOrphan:
			if err := s.outbox.EnqueueDelete(ctx, c.AssetID); err != nil {
				errs = append(errs, fmt.Sprintf("enqueue delete %s: %v", c.AssetID, err))
				continue
			}
			summary.DeleteEnqueued++

		case KindLifecycleKeyLegacy:
			if c.QdrantPointID != "" {
				legacyKeyPoints = append(legacyKeyPoints, c.QdrantPointID)
			}
		case KindLocatorLegacy:
			if c.QdrantPointID != "" {
				locatorPoints = append(locatorPoints, c.QdrantPointID)
				locatorKeysByPointID[c.QdrantPointID] = c.LocatorKeys
			}
			// KindDuplicate, KindMissingVectors, KindDimensionMismatch:
			// no automated repair. Duplicates need manual operator review
			// (which point is canonical? does the extra point carry stale
			// vectors or a different workspace?). MissingVectors and
			// DimensionMismatches are verifier-side gates — the reconciler
			// scrolls with with_vector=false so it cannot detect these.
		}
	}

	if len(legacyKeyPoints) > 0 {
		if err := s.payload.DeletePayloadKeys(ctx, collection, []string{"status"}, legacyKeyPoints); err != nil {
			errs = append(errs, fmt.Sprintf("strip legacy status keys: %v", err))
		} else {
			summary.PayloadStrips += len(legacyKeyPoints)
			legacyStrips.statusKeysStripped += len(legacyKeyPoints)
		}
	}
	if len(locatorPoints) > 0 {
		if err := s.payload.DeletePayloadKeys(ctx, collection, []string{"drive_link", "local_path"}, locatorPoints); err != nil {
			errs = append(errs, fmt.Sprintf("strip legacy locator keys: %v", err))
		} else {
			summary.PayloadStrips += len(locatorPoints)
			// Per-key metric bumps use c.LocatorKeys from scan time
			// (NOT a blanket bump per point) so dashboards see
			// "drive_link-only legacy migrations" distinct from
			// "both-keys legacy" — see legacyStripTotals doc on the
			// driveLinksStripped vs localPathsStripped invariant.
			for _, keys := range locatorKeysByPointID {
				for _, k := range keys {
					switch k {
					case "drive_link":
						legacyStrips.driveLinksStripped++
					case "local_path":
						legacyStrips.localPathsStripped++
					}
				}
			}
		}
	}

	return summary, errs, legacyStrips
}
