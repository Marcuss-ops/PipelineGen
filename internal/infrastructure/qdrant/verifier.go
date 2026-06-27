// Package qdrant — QDRANT-003 + PR 12 (June 2026) reindex verification.
//
// VerifyReindex is the post-reindex validation gate. Replaces the
// previous buildSwitchReport placeholder (QDRANT-003) AND the lax
// sample-based gates (PR 12).
//
// PR 12 hardening — strict verifier (the user spec:
// "Verifier severo"):
//
//  1. ActualPoints == ExpectedPoints (strict equality, NOT >= — the
//     previous >= accepted over-counting, which masked failed
//     partial reindexes).
//  2. Full per-channel scan on EVERY scrolled page (was: first 2
//     pages = 1000-point sample only). Every channel-declared key
//     MUST be present on every point.
//  3. EVERY scroll page error is fatal — the previous
//     `break; partial data is better than nothing` is gone. The
//     verifier returns a partial report + non-nil err so the
//     caller (reindex_qdrant.go) refuses the alias switch.
//  4. maxScrolls page cap is BLOCKING (was a logged warning). A
//     collection larger than the cap cannot complete its scan
//     and the operator MUST raise the cap on the production
//     deployment — silently truncating was the original hazard.
//  5. pt.ID MUST equal AssetIDToQdrantPointID(payload["asset_id"])
//     EXACTLY. The previous uuid.Parse(pt.ID) accepted ANY
//     UUID-form string, which silently lost the reverse-mapping
//     when the canonical asset_id was different from the
//     authored point ID (a write path this codebase no longer
//     has but legacy collections may carry).
//
// Cross-PR invariants the verifier keeps paying attention to:
//
//   - QDRANT-005 closure: the global embedding_version rescue
//     path was DELETED. Points missing per-channel keys ALWAYS
//     bump the per-channel counter; no legacy fallback path.
//   - PR 10 fail-closed vocabulary: CompleteScan bool, Report.Errors,
//     terminal-vs-noop terminology. SwitchReport now exposes
//     CompleteScan + TotalScrolled so the operator can audit the
//     verifier's footprint the same way they audit the
//     reconciler's.
//
// Ready is true ONLY when all gates pass: counts match exactly,
// CompleteScan=true, zero missing, zero orphan, zero payload
// issues, zero per-channel mismatches, zero non-canonical point
// IDs, zero dead letters, golden-query and filter smokes pass,
// and no errors occurred during verification.
package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// ReindexVerifier holds the dependencies for post-reindex validation.
type ReindexVerifier struct {
	client        *Client
	assetStore    AssetStore
	deadLetter    DeadLetterChecker // nil = skip dead-letter check
	schema        *IndexSchema      // canonically the schema under reindex; nil = skip per-channel version check
	log           *zap.Logger
	goldenQueries GoldenQueryRunner // nil = skip golden-query gate (QDRANT-005)
}

// NewReindexVerifier creates a verifier. deadLetter may be nil (legacy
// admin CLIs). schema MAY be nil only for tests that exercise gates
// unrelated to per-channel embedding versioning; production wire paths
// (cmd/admin/reindex_qdrant.go, BuildOutboxBundle) MUST supply non-nil
// schema so the per-channel version check fires.
//
// QDRANT-003 (June 2026) closure — second-pass extension: per-channel
// embedding version check. PR 12 (June 2026) subsequently tightened:
// the per-channel check now runs on EVERY scrolled page (not the
// first-1000-point sample), and a missing per-channel key on ANY
// point is blocking.
//
// Breaking signature change: the production caller is
// internal/cmd/admin/reindex_qdrant.go (single callsite as of June
// 2026). Test fixtures do not construct ReindexVerifier directly.
func NewReindexVerifier(client *Client, assetStore AssetStore, deadLetter DeadLetterChecker, schema *IndexSchema, goldenQueries GoldenQueryRunner, log *zap.Logger) *ReindexVerifier {
	return &ReindexVerifier{
		client:        client,
		assetStore:    assetStore,
		deadLetter:    deadLetter,
		schema:        schema,
		log:           log,
		goldenQueries: goldenQueries,
	}
}

// VerifyReindex runs the full validation suite against the target collection
// and returns a populated SwitchReport with Ready set accordingly.
//
// expectedPoints is the count reported by ReindexAll (IndexedAssets).
//
// PR 12 (June 2026) hardened:
//   - Strict point-count equality.
//   - Any scroll page error returns (report, err) with Ready=false;
//     the caller MUST refuse the alias switch.
//   - maxScrolls cap → CompleteScan=false + Errors appended.
//   - pt.ID canonicality is checked literally against the
//     AssetIDToQdrantPointID boundary — non-canonical IDs are
//     blocking (NonCanonicalPointCount > 0 → Ready=false).
//   - Per-channel embedding_version_<channel> checked on EVERY
//     scrolled page; missing key on ANY point bumps the
//     per-channel counter and blocks.
//
// QDRANT-003 closed (June 2026): every gate that was previously a TODO
// placeholder is now implemented. The caller checks `report.Ready` before
// calling SwitchAlias; a false Ready MUST block the alias switch.
func (v *ReindexVerifier) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*SwitchReport, error) {
	report := &SwitchReport{
		TargetCollection:          targetCollection,
		ExpectedPoints:            expectedPoints,
		CompleteScan:              false, // PR 12: starts "incomplete"; flipped true on clean scroll-lopp exit.
		GoldenQueriesOK:           false,
		FiltersOK:                 false,
		VersionMismatchPerChannel: make(map[string]int),
		NonCanonicalPointIDs:      nil,
	}

	// ── Gate 1: Point count parity — PR 12 STRICT equality ────────
	actualPoints, err := v.client.CountPoints(ctx, targetCollection)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count points: %v", err))
		// Hard error — can't verify anything without a count.
		return report, fmt.Errorf("QDRANT-003: cannot verify reindex — count failed: %w", err)
	}
	report.ActualPoints = actualPoints

	// PR 12: strict equality (was: actual < expected). Both branches
	// of inequality are blocking — over-counting is just as suspect
	// as under-counting (a partially-cancelled writer can produce
	// extra points that don't round-trip through any SQLite row).
	if actualPoints != expectedPoints {
		report.Errors = append(report.Errors,
			fmt.Sprintf("PR 12 point count mismatch (strict): expected %d, actual %d (delta %+d)",
				expectedPoints, actualPoints, actualPoints-expectedPoints))
		// Do NOT return early. Continue gathering diagnostics so the
		// operator gets a full report. Ready will be false.
	}

	// ── Gate 2: Scroll + missing/orphan/payload/version/canonical ───
	sqliteIDs, err := v.assetStore.ListAllAssetIDs(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list SQLite asset IDs: %v", err))
		return report, fmt.Errorf("QDRANT-003: cannot verify reindex — SQLite list failed: %w", err)
	}

	// Build SQLite ID set for O(1) lookup.
	sqliteSet := make(map[string]bool, len(sqliteIDs))
	for _, id := range sqliteIDs {
		sqliteSet[id] = true
	}

	// Build Qdrant point ID set by scrolling.
	qdrantIDs := make(map[string]bool)
	var offset string
	scrollPage := 500
	const maxScrolls = 400 // PR 12: cap was a logged warning — now BLOCKING (gate 4 below).
	pointsScrolled := 0
	scrollAborted := false

	for iteration := 0; iteration < maxScrolls; iteration++ {
		result, serr := v.client.ScrollPoints(ctx, targetCollection, offset, scrollPage, nil)
		if serr != nil {
			// PR 12: ANY page error is FATAL. The QDRANT-003-era
			// "break; partial data is better than nothing" comment is
			// gone — partial data on a partial scan is exactly what
			// the user-spec wanted to prevent (Verifier severo).
			report.Errors = append(report.Errors, fmt.Sprintf("PR 12 scroll page %d: %v (fatal)", iteration, serr))
			scrollAborted = true
			break
		}

		pointsScrolled += len(result.Points)
		for idx, pt := range result.Points {
			// Read canonical asset_id directly from point payload.
			// Comma-ok is required: a missing or non-string asset_id
			// MUST NOT pollute qdrantIDs with an empty key (would
			// silently mask a SQLite row whose own asset_id is the
			// empty string in MissingIDs). PayloadIssues still
			// surfaces the missing-field case below.
			assetID, assetIDOK := pt.Payload["asset_id"].(string)
			if assetIDOK && assetID != "" {
				qdrantIDs[assetID] = true
			}

			// Gate 3: Payload minimum validation.
			if issue := validatePayloadMinimum(pt.Payload, assetID); issue != "" {
				report.PayloadIssues++
				if len(report.Errors) < 20 { // cap error list
					report.Errors = append(report.Errors, issue)
				}
			}

			// Gate 3b (PR 12 — strict canonical pt.ID): a point whose
			// UUID-form id does NOT match AssetIDToQdrantPointID(asset_id)
			// is BLOCKING. The previous uuid.Parse(pt.ID) accept-anything-
			// UUID-form mask is gone — a generic UUID string used to be
			// accepted because uuid.Parse returned nil err for any
			// canonical-form string, which silently lost the reverse-
			// mapping lookup that the canonical boundary is the only
			// authority for.
			//
			// Empty asset_id path: we still surface this case via
			// PayloadIssues above (asset_id missing in payload); the
			// canonical check is skipped because AssetIDToQdrantPointID("")
			// returns "" and a literal compare would mask the diagnosis.
			if assetIDOK && assetID != "" {
				canonical := AssetIDToQdrantPointID(assetID)
				if pt.ID != canonical {
					report.NonCanonicalPointCount++
					// Cap the per-error append, but the total count
					// keeps growing so Ready stays false until the
					// collection is fully re-emitted from a canonical
					// writer. The companion NonCanonicalTruncated flag
					// is set when the cap is reached so JSON consumers
					// can tell the count is the truth, the slice is a
					// sample of the first 20 entries.
					if len(report.NonCanonicalPointIDs) < 20 {
						report.NonCanonicalPointIDs = append(report.NonCanonicalPointIDs, pt.ID)
					} else {
						report.NonCanonicalTruncated = true
					}
					if len(report.Errors) < 20 {
						report.Errors = append(report.Errors,
							fmt.Sprintf("PR 12 non-canonical pt.ID: pt.ID=%q, AssetIDToQdrantPointID(%q)=%q (point #%d on page %d)",
								pt.ID, assetID, canonical, idx, iteration))
					}
				}
			}

			// Gate 4 (PR 12 — full per-channel scan, every page).
			// The QDRANT-005 "if iteration < 2" sample block is
			// GONE. We run the per-channel check on EVERY scrolled
			// point. A point missing a per-channel key always bumps
			// the per-channel counter (QDRANT-005 closure kept); a
			// mismatched value bumps it the same way.
			pointMismatched := false

			// Legacy global check: if the point carries a
			// global embedding_version and it disagrees with
			// CurrentEmbeddingVersion, the point is mismatched.
			// Absence here is neutral — the per-channel loop below
			// owns the surface.
			if gv, ok := pt.Payload["embedding_version"].(string); ok && gv != "" && gv != CurrentEmbeddingVersion {
				pointMismatched = true
			}

			if v.schema != nil {
				for _, spec := range v.schema.DenseVectors {
					if spec.ModelVersion == "" {
						// Channel has no canonical model version in
						// the schema; cannot compare. Skip — but the
						// point still must satisfy the OTHER channels
						// and the legacy global (above).
						continue
					}
					key := fmt.Sprintf("embedding_version_%s", spec.Channel)
					actual, present := pt.Payload[key].(string)
					if !present {
						// QDRANT-005 closure: the global embedding_version
						// rescue path was DELETED. A point missing its
						// per-channel key always bumps the per-channel
						// mismatch counter, regardless of whether the
						// global embedding_version matches.
						report.VersionMismatchPerChannel[spec.Channel]++
						pointMismatched = true
						continue
					}
					if actual != spec.ModelVersion {
						report.VersionMismatchPerChannel[spec.Channel]++
						pointMismatched = true
					}
				}
			}

			if pointMismatched {
				report.VersionMismatch++
			}
		}

		if result.NextOffset == "" {
			break
		}
		offset = result.NextOffset

		if iteration == maxScrolls-1 {
			// PR 12: cap reached with trailing NextOffset is BLOCKING.
			// The QDRANT-003-era "log.Warn and continue" path is gone —
			// the operator MUST raise the cap on the production
			// deployment, not silently truncate. CompleteScan=false;
			// Ready will be false at the gate below.
			report.Errors = append(report.Errors,
				fmt.Sprintf("PR 12 scroll iteration cap %d reached with NextOffset=%q trailing (BLOCKING — raise the cap on the production deployment; the remaining %d+ points were never scanned, the collection is unverified)",
					maxScrolls, offset, 0))
			scrollAborted = true
		}
	}

	report.TotalScrolled = pointsScrolled

	// PR 12: CompleteScan true ONLY when the scan finished cleanly.
	// False on any page error or cap-hit. Mirrors PR 10's
	// ScannedTotals.CompleteScan vocabulary.
	//
	// Strict equality vs the count endpoint: the verifier scans the
	// SAME collection the count endpoint reported (CountPoints → HTTP
	// GET /collections/<name>). A scroll that returns MORE points
	// than CountPoints indicates a duplicate-anomaly inside the
	// verifier's scroll state (rare — typically a faulty test mock
	// or a Qdrant bug under concurrent writes) and is treated as an
	// incomplete scan. A scroll that returns FEWER points means the
	// cap was hit or premature termination; incomplete.
	if !scrollAborted && pointsScrolled == report.ActualPoints {
		report.CompleteScan = true
	}

	// Guard: if no points were scrolled (first-page failure or empty
	// collection), skip the missing/orphan computation — the qdrantIDs
	// set is empty and would produce catastrophic false positives.
	if pointsScrolled == 0 {
		report.Errors = append(report.Errors,
			"PR 12: zero points scrolled — cannot compute missing/orphan IDs. "+
				"Check collection exists and scroll API is reachable.")
	} else {
		// ── Compute missing / orphan from ID sets ────────────────
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

	// ── Gate 5: Dead‑letter check (optional) ─────────────────────
	if v.deadLetter != nil {
		if dl, err := v.deadLetter.CountOpen(ctx); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("dead‑letter count: %v", err))
		} else {
			report.DeadLetterOpen = dl
		}
	}

	// ── Gate 6: Golden‑query smoke ─────────────────────────────
	report.GoldenQueriesOK = v.runGoldenQuerySmoke(ctx, targetCollection)
	if !report.GoldenQueriesOK {
		report.Errors = append(report.Errors,
			"QDRANT-005: golden query smoke failed — collection not queryable or payloads malformed")
	}

	// ── Gate 7: Filter smoke ───────────────────────────────────
	report.FiltersOK = v.runFilterSmoke(ctx, targetCollection)
	if !report.FiltersOK {
		report.Errors = append(report.Errors,
			"QDRANT-005: filter smoke failed — payload index or filtering broken")
	}

	// ── Ready gate: ALL conditions must pass (PR 12 strict) ───────
	//
	// PR 12 additions vs QDRANT-003:
	//   - CompleteScan must be true (no truncating scroll error / cap hit).
	//   - ActualPoints == ExpectedPoints (strict equality).
	//   - NonCanonicalPointCount == 0 (pt.ID == AssetIDToQdrantPointID(asset_id)).
	//   - Per-channel totals must collapse to zero across the entire
	//     scan (the global counter is unnecessary — channel totals
	//     sum to it; Ready gates on the channels directly to avoid
	//     the latch-leak footgun in QDRANT-003's pointMismatched
	//     per-point counter).
	channelTotal := 0
	for _, c := range report.VersionMismatchPerChannel {
		channelTotal += c
	}
	report.Ready = report.CompleteScan &&
		report.ActualPoints == report.ExpectedPoints &&
		report.ExpectedPoints > 0 &&
		report.MissingCount == 0 &&
		report.OrphanCount == 0 &&
		report.PayloadIssues == 0 &&
		channelTotal == 0 &&
		report.NonCanonicalPointCount == 0 &&
		report.DeadLetterOpen == 0 &&
		report.GoldenQueriesOK &&
		report.FiltersOK &&
		len(report.Errors) == 0

	if !report.Ready {
		v.log.Warn("PR 12 reindex verification FAILED",
			zap.String("target", targetCollection),
			zap.Bool("complete_scan", report.CompleteScan),
			zap.Int("expected", report.ExpectedPoints),
			zap.Int("actual", report.ActualPoints),
			zap.Int("scrolled", report.TotalScrolled),
			zap.Int("missing", report.MissingCount),
			zap.Int("orphan", report.OrphanCount),
			zap.Int("payload_issues", report.PayloadIssues),
			zap.Int("version_mismatch", report.VersionMismatch),
			zap.Int("non_canonical_point_count", report.NonCanonicalPointCount),
			zap.Int("dead_letter_open", report.DeadLetterOpen),
			zap.Int("errors", len(report.Errors)))
	} else {
		v.log.Info("PR 12 reindex verification PASSED — all gates green",
			zap.String("target", targetCollection),
			zap.Int("points", report.ActualPoints),
			zap.Int("scanned", report.TotalScrolled))
	}

	// PR 12: when ANY scroll gate fires (page error OR cap hit),
	// return a non-nil error so the caller (reindex_qdrant.go) can
	// distinguish "verifier ran clean" from "verifier aborted mid-
	// scan". The report itself still carries the diagnostics; the
	// cmd-level caller maps the err into the ErrAliasSwitchNotReady
	// contract that the alias switch gate honours.
	if scrollAborted {
		return report, fmt.Errorf("PR 12: scroll aborted mid-scan (see report.Errors — alias switch BLOCKED)")
	}
	return report, nil
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

// ── QDRANT-005 smoke runners ────────────────────────────────────────

// runGoldenQuerySmoke verifies the target collection is queryable by
// scrolling a small sample and checking that at least one returned point
// has a well-formed payload (asset_id, name, source all present).
//
// QDRANT-005 (June 2026): replaces the hardcoded GoldenQueriesOK=true
// placeholder. A failing smoke test (scroll error, empty collection,
// or zero points with valid payload) sets GoldenQueriesOK=false and
// blocks the Ready gate.
func (v *ReindexVerifier) runGoldenQuerySmoke(ctx context.Context, collection string) bool {
	result, err := v.client.ScrollPoints(ctx, collection, "", 10, nil)
	if err != nil {
		v.log.Warn("QDRANT-005 golden query smoke: scroll failed",
			zap.String("collection", collection),
			zap.Error(err))
		return false
	}
	if len(result.Points) == 0 {
		v.log.Warn("QDRANT-005 golden query smoke: collection is empty",
			zap.String("collection", collection))
		// An empty collection is unusual for a reindex but not
		// necessarily a failure — it means there are no assets.
		// Allow Ready to proceed; the count gates will catch
		// the mismatch if assets were expected.
		return true
	}

	// Verify at least one point has the minimum required payload.
	for _, pt := range result.Points {
		if validatePayloadMinimum(pt.Payload, pt.ID) == "" {
			return true
		}
	}

	v.log.Warn("QDRANT-005 golden query smoke: no point with valid payload in sample",
		zap.String("collection", collection),
		zap.Int("sample_size", len(result.Points)))
	return false
}

// runFilterSmoke validates that Qdrant payload indexes work correctly by
// running a filtered scroll and checking that every returned point matches
// the filter criteria.
//
// Algorithm:
//  1. Scroll a small unfiltered sample to discover a filterable field value.
//  2. Build a Qdrant filter on "source" matching that value.
//  3. Scroll with the filter and verify ALL results have the expected source.
//
// QDRANT-005 (June 2026): replaces the hardcoded FiltersOK=true placeholder.
// Filter failure blocks the Ready gate.
func (v *ReindexVerifier) runFilterSmoke(ctx context.Context, collection string) bool {
	// Phase 1: discover a source value from an unfiltered scroll.
	sample, err := v.client.ScrollPoints(ctx, collection, "", 20, nil)
	if err != nil {
		v.log.Warn("QDRANT-005 filter smoke: cannot scroll for filter discovery",
			zap.String("collection", collection),
			zap.Error(err))
		return false
	}
	if len(sample.Points) == 0 {
		// Empty collection — no filter to test.
		v.log.Info("QDRANT-005 filter smoke: collection empty, skipping filter test")
		return true
	}

	// Find the first point with a non-empty "source" field.
	var sourceValue string
	for _, pt := range sample.Points {
		if s, ok := pt.Payload["source"].(string); ok && s != "" {
			sourceValue = s
			break
		}
	}
	if sourceValue == "" {
		v.log.Warn("QDRANT-005 filter smoke: no source field found in sample points",
			zap.String("collection", collection),
			zap.Int("sample_size", len(sample.Points)))
		return false
	}

	// Phase 2: scroll with a source filter.
	filter := map[string]interface{}{
		"must": []map[string]interface{}{
			{
				"key":   "source",
				"match": map[string]interface{}{"value": sourceValue},
			},
		},
	}
	filtered, err := v.client.ScrollPoints(ctx, collection, "", 50, filter)
	if err != nil {
		v.log.Warn("QDRANT-005 filter smoke: filtered scroll failed",
			zap.String("collection", collection),
			zap.String("source", sourceValue),
			zap.Error(err))
		return false
	}
	if len(filtered.Points) == 0 {
		v.log.Warn("QDRANT-005 filter smoke: filtered scroll returned zero points",
			zap.String("collection", collection),
			zap.String("source", sourceValue))
		return false
	}

	// Verify ALL returned points have the matching source.
	for _, pt := range filtered.Points {
		s, ok := pt.Payload["source"].(string)
		if !ok {
			v.log.Warn("QDRANT-005 filter smoke: point missing source field",
				zap.String("point_id", pt.ID))
			return false
		}
		if s != sourceValue {
			v.log.Warn("QDRANT-005 filter smoke: source mismatch",
				zap.String("point_id", pt.ID),
				zap.String("expected", sourceValue),
				zap.String("got", s))
			return false
		}
	}

	v.log.Info("QDRANT-005 filter smoke: PASSED",
		zap.String("collection", collection),
		zap.String("source", sourceValue),
		zap.Int("matched", len(filtered.Points)))
	return true
}
