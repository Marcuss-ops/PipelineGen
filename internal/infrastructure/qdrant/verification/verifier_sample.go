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

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
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
	checkCanonical := func(idx, iteration int, pt schema.ScrollPoint) {
		assetID, assetIDOK := pt.Payload["asset_id"].(string)
		if assetIDOK && assetID != "" {
			qdrantIDs[assetID] = true
		}

		// Gate 3: Payload minimum validation.
		if issue := validatePayloadMinimum(pt.Payload, assetID); issue != "" {
			report.PayloadIssues++
			if len(report.Errors) < 20 {
				report.Errors = append(report.Errors, issue)
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
				if len(report.Errors) < 20 {
					report.Errors = append(report.Errors,
						fmt.Sprintf("PR 12 non-canonical pt.ID: pt.ID=%q, schema.AssetIDToQdrantPointID(%q)=%q (point #%d on page %d)",
							pt.ID, assetID, canonical, idx, iteration))
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
// The legacy global embedding_version rescue path was DELETED (QDRANT-005
// closure); points missing per-channel keys ALWAYS fail.
//
// Returns true if ANY per-channel mismatch was found (so the caller can
// bump the global VersionMismatch counter).
func (v *ReindexVerifier) verifyPerChannelVersions(payload map[string]interface{}, report *schema.SwitchReport) bool {
	pointMismatched := false

	// Legacy global check.
	if gv, ok := payload["embedding_version"].(string); ok && gv != "" && gv != schema.CurrentEmbeddingVersion {
		pointMismatched = true
	}

	if v.schema == nil {
		return pointMismatched
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
// but not SQLite). When zero points were scrolled a guard in the
// orchestrator skips this call to avoid catastrophic false positives.
func computeMissingOrphan(sqliteSet, qdrantIDs map[string]bool, report *schema.SwitchReport) {
	for sqliteID := range sqliteSet {
		if !qdrantIDs[sqliteID] {
			report.MissingIDs = append(report.MissingIDs, sqliteID)
			report.MissingCount++
		}
	}
	for qdrantID := range qdrantIDs {
		if !sqliteSet[qdrantID] {
			report.OrphanIDs = append(report.OrphanIDs, qdrantID)
			report.OrphanCount++
		}
	}
}

// validatePayloadMinimum checks that a Qdrant point's payload contains the
// minimum required fields (asset_id, name, source). Returns a human-readable
// issue string, or empty string if the payload is valid.
func validatePayloadMinimum(payload map[string]interface{}, pointID string) string {
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
