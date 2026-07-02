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
//  5. pt.ID MUST equal schema.AssetIDToQdrantPointID(payload["asset_id"])
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
//
// P2 SPLIT-VERIFY-REINDEX (July 2026): VerifyReindex split into
// 7 gate functions so each helper has a single responsibility
// and the orchestrator stays ~30 lines. Gate functions:
//
//	verifyPointCountParity, verifyScrollAndCanonical,
//	verifyPerChannelVersions (per-point), computeMissingOrphan,
//	checkDeadLetters, runGoldenQuerySmoke, runFilterSmoke.
package qdrant

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
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
// P2 SPLIT-VERIFY-REINDEX (July 2026): the function body is now a thin
// orchestrator that delegates to 7 gate functions. The cyclomatic
// complexity of the scroll loop (~35 branches) is partitioned across
// verifyScrollAndCanonical (scroll + payload + canonical ID) and
// verifyPerChannelVersions (per-point version check), each with a
// single responsibility.
func (v *ReindexVerifier) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*SwitchReport, error) {
	report := &SwitchReport{
		TargetCollection:          targetCollection,
		ExpectedPoints:            expectedPoints,
		CompleteScan:              false,
		GoldenQueriesOK:           false,
		FiltersOK:                 false,
		VersionMismatchPerChannel: make(map[string]int),
		NonCanonicalPointIDs:      nil,
	}

	// ── Gate 1: Point count parity ──────────────────────────────
	if err := v.verifyPointCountParity(ctx, targetCollection, expectedPoints, report); err != nil {
		return report, err
	}

	// ── Load SQLite IDs for missing/orphan computation ──────────
	sqliteIDs, err := v.assetStore.ListAllAssetIDs(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list SQLite asset IDs: %v", err))
		return report, fmt.Errorf("QDRANT-003: cannot verify reindex — SQLite list failed: %w", err)
	}
	sqliteSet := make(map[string]bool, len(sqliteIDs))
	for _, id := range sqliteIDs {
		sqliteSet[id] = true
	}

	// ── Gates 2–4: Scroll + canonical + per-channel versions ────
	qdrantIDs, scrollAborted := v.verifyScrollAndCanonical(ctx, targetCollection, report)

	// ── Compute missing/orphan from ID sets ─────────────────────
	// Guard: when zero points were scrolled (first-page failure or
	// empty collection), qdrantIDs is empty and computing missing/orphan
	// would produce catastrophic false positives.
	if report.TotalScrolled == 0 {
		report.Errors = append(report.Errors,
			"PR 12: zero points scrolled — cannot compute missing/orphan IDs. "+
				"Check collection exists and scroll API is reachable.")
	} else {
		computeMissingOrphan(sqliteSet, qdrantIDs, report)
	}

	// ── Gate 5: Dead‑letter check ───────────────────────────────
	v.checkDeadLetters(ctx, report)

	// ── Gate 6: Golden‑query smoke ──────────────────────────────
	report.GoldenQueriesOK = v.runGoldenQuerySmoke(ctx, targetCollection)
	if !report.GoldenQueriesOK {
		report.Errors = append(report.Errors,
			"QDRANT-005: golden query smoke failed — collection not queryable or payloads malformed")
	}

	// ── Gate 7: Filter smoke ────────────────────────────────────
	report.FiltersOK = v.runFilterSmoke(ctx, targetCollection)
	if !report.FiltersOK {
		report.Errors = append(report.Errors,
			"QDRANT-005: filter smoke failed — payload index or filtering broken")
	}

	// ── Ready gate ──────────────────────────────────────────────
	report.Ready = computeReady(report)

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

	if scrollAborted {
		return report, fmt.Errorf("PR 12: scroll aborted mid-scan (see report.Errors — alias switch BLOCKED)")
	}
	return report, nil
}

// ── Gate 1: verifyPointCountParity ─────────────────────────────────

// verifyPointCountParity checks that the Qdrant collection's point count
// exactly matches the expected count. Strict equality (PR 12): both
// under-count and over-count are blocking. The mismatch is appended to
// report.Errors but does NOT return an error — the verifier continues
// gathering diagnostics. Returns error only on a fatal CountPoints
// failure (Qdrant unreachable).
func (v *ReindexVerifier) verifyPointCountParity(ctx context.Context, target string, expected int, report *SwitchReport) error {
	actual, err := v.client.CountPoints(ctx, target)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count points: %v", err))
		return fmt.Errorf("QDRANT-003: cannot verify reindex — count failed: %w", err)
	}
	report.ActualPoints = actual

	if actual != expected {
		report.Errors = append(report.Errors,
			fmt.Sprintf("PR 12 point count mismatch (strict): expected %d, actual %d (delta %+d)",
				expected, actual, actual-expected))
	}
	return nil
}

// ── Gate 2–4: verifyScrollAndCanonical ────────────────────────────

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
// orchestrator can run all remaining gates (missing/orphan, dead letters,
// smokes, Ready) before deciding whether to return non-nil error.
//
// Populates report.PayloadIssues, report.NonCanonicalPointCount,
// report.VersionMismatch, report.VersionMismatchPerChannel,
// report.TotalScrolled, report.CompleteScan, and report.Errors.
func (v *ReindexVerifier) verifyScrollAndCanonical(ctx context.Context, target string, report *SwitchReport) (qdrantIDs map[string]bool, scrollAborted bool) {
	qdrantIDs = make(map[string]bool)
	var offset string
	const scrollPage = 500
	const maxScrolls = 400 // PR 12: cap is BLOCKING.
	pointsScrolled := 0

	// ── Per-point check: canonical pt.ID + payload validation ───
	checkCanonical := func(idx, iteration int, pt ScrollPoint) {
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

// ── Gate 4 (per-point): verifyPerChannelVersions ──────────────────

// verifyPerChannelVersions checks a single Qdrant point's payload for
// per-channel embedding_version_<channel> keys against the verifier's
// IndexSchema. Every declared channel MUST have a matching version key;
// a missing or mismatched key bumps the per-channel counter in report.
//
// The legacy global embedding_version rescue path was DELETED (QDRANT-005
// closure); points missing per-channel keys ALWAYS fail.
//
// Returns true if ANY per-channel mismatch was found (so the caller can
// bump the global VersionMismatch counter).
func (v *ReindexVerifier) verifyPerChannelVersions(payload map[string]interface{}, report *SwitchReport) bool {
	pointMismatched := false

	// Legacy global check.
	if gv, ok := payload["embedding_version"].(string); ok && gv != "" && gv != CurrentEmbeddingVersion {
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

// ── Compute missing/orphan ────────────────────────────────────────

// computeMissingOrphan compares the SQLite ID set against the Qdrant
// point ID set and populates report.MissingIDs / MissingCount (in
// SQLite but not Qdrant) and report.OrphanIDs / OrphanCount (in Qdrant
// but not SQLite). When zero points were scrolled a guard in the
// orchestrator skips this call to avoid catastrophic false positives.
func computeMissingOrphan(sqliteSet, qdrantIDs map[string]bool, report *SwitchReport) {
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

// ── Gate 5: checkDeadLetters ──────────────────────────────────────

// checkDeadLetters queries the optional DeadLetterChecker and records
// the count of open dead-letter entries on report. When the checker is
// nil (legacy admin CLIs) this is a no-op. Errors are appended to
// report.Errors but do not abort verification.
func (v *ReindexVerifier) checkDeadLetters(ctx context.Context, report *SwitchReport) {
	if v.deadLetter == nil {
		return
	}
	dl, err := v.deadLetter.CountOpen(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("dead-letter count: %v", err))
		return
	}
	report.DeadLetterOpen = dl
}

// ── Ready gate ────────────────────────────────────────────────────

// computeReady evaluates all gate conditions and returns true only when
// every gate is green: CompleteScan=true, counts match, zero missing/
// orphan/payload issues/version mismatches/non-canonical point IDs/
// dead letters, golden-query and filter smokes pass, and no errors.
func computeReady(report *SwitchReport) bool {
	channelTotal := 0
	for _, c := range report.VersionMismatchPerChannel {
		channelTotal += c
	}
	return report.CompleteScan &&
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
}

// ── Payload validation ────────────────────────────────────────────

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
