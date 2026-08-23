// Package qdrant — P1 QDRANT-VERIFIER-SPLIT: sample phase (July 2026).
//
// verifySample runs the scroll-and-canonical verification across every
// point in the Qdrant collection, performs per-channel version checks,
// and computes missing/orphan IDs. Extracted from verifier.go so each
// phase has a single responsibility.
package verification

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// verifySample performs Gates 2–4 (scroll, canonical pt.ID, per-channel
// versions) and computes missing/orphan from the ID sets.
//
// Populates report.TotalScrolled, report.CompleteScan,
// report.PayloadIssues, report.NonCanonicalPointCount,
// report.VersionMismatch, report.VersionMismatchPerChannel,
// report.MissingIDs/MissingCount, report.OrphanIDs/OrphanCount,
// and report.Errors.
//
// Returns scrollAborted (true on page error or cap hit).
func (v *ReindexVerifier) verifySample(ctx context.Context, target string, sqliteSet map[string]bool, report *schema.SwitchReport) (scrollAborted bool) {
	// ── Gates 2–4: Scroll + canonical + per-channel versions ────
	qdrantIDs, scrollAborted := v.verifyScrollAndCanonical(ctx, target, report)

	// ── Compute missing/orphan from ID sets ─────────────────────
	if report.TotalScrolled == 0 {
		report.Errors = append(report.Errors,
			"PR 12: zero points scrolled — cannot compute missing/orphan IDs. "+
				"Check collection exists and scroll API is reachable.")
	} else {
		computeMissingOrphan(sqliteSet, qdrantIDs, report)
	}
	return scrollAborted
}

// verifyScrollAndCanonical scrolls every point in the target collection
// and performs three per-point checks inline:
//   - Payload minimum validation (asset_id, name, source).
//   - Canonical pt.ID == schema.AssetIDToQdrantPointID(asset_id).
//   - Per-channel embedding_version_<channel> parity (delegates to
//     verifyPerChannelVersions).
//
// Returns the qdrantIDs set for downstream missing/orphan computation
// and a scrollAborted flag (true on page error or cap hit). Does NOT
// return an error — diagnostics are appended to report.Errors so the
// orchestrator can run all remaining gates before deciding whether to
// return non-nil error.
func (v *ReindexVerifier) verifyScrollAndCanonical(ctx context.Context, target string, report *schema.SwitchReport) (qdrantIDs map[string]bool, scrollAborted bool) {
	qdrantIDs = make(map[string]bool)
	var offset string
	const scrollPage = 500
	const maxScrolls = 400 // PR 12: cap is BLOCKING.
	pointsScrolled := 0

	// ── Per-point check: canonical pt.ID + payload validation ───
	// Task 7: removed 20-entry error cap — all errors up to
	// MaxErrors are reported. Beyond the cap, ErrorsTruncated is
	// set and no further diagnostics are appended (counters still
	// increment accurately). Same truncation pattern for
	// NonCanonicalPointIDs (20-entry diagnostic sample, cap tracked
	// via NonCanonicalTruncated).
	checkCanonical := func(idx, iteration int, pt schema.ScrollPoint) {
		assetID, assetIDOK := pt.Payload["asset_id"].(string)
		if assetIDOK && assetID != "" {
			if qdrantIDs[assetID] {
				// PR-HASH-SEMANTICS item 14: the same canonical asset_id
				// already appeared in an earlier point. The 1-asset-1-point
				// invariant is violated; the extra point is a duplicate.
				report.DuplicateQdrantPoints++
				if len(report.DuplicatePointIDs) < 20 {
					report.DuplicatePointIDs = append(report.DuplicatePointIDs, pt.ID)
				} else {
					report.DuplicateTruncated = true
				}
			} else {
				qdrantIDs[assetID] = true
			}
		}

		// Gate 3: Payload minimum validation.
		if issue := validatePayloadMinimum(pt.Payload, assetID); issue != "" {
			report.PayloadIssues++
			if len(report.Errors) < schema.MaxErrors {
				report.Errors = append(report.Errors, issue)
			} else {
				report.ErrorsTruncated = true
			}
		}

		// Gate 3b (PR 12): strict canonical pt.ID.
		if assetIDOK && assetID != "" {
			canonical := schema.AssetIDToQdrantPointID(assetID)
			if pt.ID != canonical {
				report.NonCanonicalPointCount++
				if len(report.NonCanonicalPointIDs) < 20 {
					report.NonCanonicalPointIDs = append(report.NonCanonicalPointIDs, pt.ID)
				} else {
					report.NonCanonicalTruncated = true
				}
				if len(report.Errors) < schema.MaxErrors {
					report.Errors = append(report.Errors,
						fmt.Sprintf("PR 12 non-canonical pt.ID: pt.ID=%q, schema.AssetIDToQdrantPointID(%q)=%q (point #%d on page %d)",
							pt.ID, assetID, canonical, idx, iteration))
				} else {
					report.ErrorsTruncated = true
				}
			}
		}
	}

	for iteration := 0; iteration < maxScrolls; iteration++ {
		result, serr := v.client.ScrollPoints(ctx, target, offset, scrollPage, nil)
		if serr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("PR 12 scroll page %d: %v (fatal)", iteration, serr))
			scrollAborted = true
			break
		}

		pointsScrolled += len(result.Points)
		for idx, pt := range result.Points {
			checkCanonical(idx, iteration, pt)

			// Gate 4 (PR 12): full per-channel scan, every page.
			pointMismatched := v.verifyPerChannelVersions(pt.Payload, report)
			if pointMismatched {
				report.VersionMismatch++
			}
		}

		if result.NextOffset == "" {
			break
		}
		offset = result.NextOffset

		if iteration == maxScrolls-1 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("PR 12 scroll iteration cap %d reached with NextOffset=%q trailing (BLOCKING — raise the cap on the production deployment; the remaining points were never scanned, the collection is unverified)",
					maxScrolls, offset))
			scrollAborted = true
		}
	}

	report.TotalScrolled = pointsScrolled

	// CompleteScan true ONLY when the scan finished cleanly.
	if !scrollAborted && pointsScrolled == report.ActualPoints {
		report.CompleteScan = true
	}

	return qdrantIDs, scrollAborted
}

// verifyPerChannelVersions checks a single Qdrant point's payload for
// per-channel embedding_version_<channel> keys against the verifier's
// schema.IndexSchema. Every declared channel MUST have a matching version key;
// a missing or mismatched key bumps the per-channel counter in report.
//
// Task 7 (July 2026): the legacy global embedding_version rescue path
// was PHYSICALLY REMOVED. Points without per-channel keys ALWAYS fail;
// the old global fallback (checking payload["embedding_version"] standalone)
// is gone — per-channel is the only path.
//
// Returns true if ANY per-channel mismatch was found (so the caller can
// bump the global VersionMismatch counter).
func (v *ReindexVerifier) verifyPerChannelVersions(payload map[string]any, report *schema.SwitchReport) bool {
	pointMismatched := false

	if v.schema == nil {
		return false
	}

	for _, spec := range v.schema.DenseVectors {
		if spec.ModelVersion == "" {
			continue
		}
		key := fmt.Sprintf("embedding_version_%s", spec.Channel)
		actual, present := payload[key].(string)
		if !present {
			report.VersionMismatchPerChannel[spec.Channel]++
			pointMismatched = true
			continue
		}
		if actual != spec.ModelVersion {
			report.VersionMismatchPerChannel[spec.Channel]++
			pointMismatched = true
		}
	}

	return pointMismatched
}

// computeMissingOrphan compares the SQLite ID set against the Qdrant
// point ID set and populates report.MissingIDs / MissingCount (in
// SQLite but not Qdrant) and report.OrphanIDs / OrphanCount (in Qdrant
// but not SQLite).
//
// Task 7: MissingIDs and OrphanIDs are capped at MaxMissingOrphanIDs.
// Beyond the cap, the respective Truncated flag is set but the counter
// keeps incrementing so the operator sees the actual totals.
func computeMissingOrphan(sqliteSet, qdrantIDs map[string]bool, report *schema.SwitchReport) {
	for sqliteID := range sqliteSet {
		if !qdrantIDs[sqliteID] {
			report.MissingCount++
			if report.MissingCount <= schema.MaxMissingOrphanIDs {
				report.MissingIDs = append(report.MissingIDs, sqliteID)
			} else {
				report.MissingTruncated = true
			}
		}
	}
	for qdrantID := range qdrantIDs {
		if !sqliteSet[qdrantID] {
			report.OrphanCount++
			if report.OrphanCount <= schema.MaxMissingOrphanIDs {
				report.OrphanIDs = append(report.OrphanIDs, qdrantID)
			} else {
				report.OrphanTruncated = true
			}
		}
	}
}

// validatePayloadMinimum checks that a Qdrant point's payload contains the
// minimum required fields (asset_id, name, source). Returns a human-readable
// issue string, or empty string if the payload is valid.
func validatePayloadMinimum(payload map[string]any, pointID string) string {
	if payload == nil {
		return fmt.Sprintf("point %s: payload is nil", pointID)
	}
	required := []string{"asset_id", "name", "source"}
	for _, field := range required {
		if val, ok := payload[field]; !ok || val == nil || val == "" {
			return fmt.Sprintf("point %s: missing required payload field %q", pointID, field)
		}
	}
	return ""
}
