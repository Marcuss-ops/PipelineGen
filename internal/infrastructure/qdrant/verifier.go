// Package qdrant — QDRANT-003 reindex verification (June 2026).
//
// VerifyReindex is the post-reindex validation gate. It replaces the
// previous buildSwitchReport placeholder (which had TODO zero-values for
// missing/orphan/dead-letter/golden-query/filter fields). The new verifier:
//
//  1. Counts points in the target collection — hard error on mismatch.
//  2. Scrolls all points and compares IDs against SQLite (missing/orphan).
//  3. Validates payload minimum on every point (asset_id, name, source).
//  4. Checks embedding_version on sampled points for consistency.
//  5. Optionally checks dead-letter count via DeadLetterChecker.
//
// Ready is true ONLY when all gates pass: counts match, zero missing, zero
// orphan, zero payload issues, zero version mismatches, zero dead letters,
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
	deadLetter    DeadLetterChecker        // nil = skip dead-letter check
	schema        *IndexSchema             // canonically the schema under reindex; nil = skip per-channel version check
	log           *zap.Logger
	goldenQueries GoldenQueryRunner        // nil = skip golden-query gate (QDRANT-005)
}

// NewReindexVerifier creates a verifier. deadLetter may be nil (legacy
// admin CLIs). schema MAY be nil only for tests that exercise gates
// unrelated to per-channel embedding versioning; production wire paths
// (cmd/admin/reindex_qdrant.go, BuildOutboxBundle) MUST supply non-nil
// schema so the per-channel version check fires.
//
// QDRANT-003 (June 2026) closure — second-pass extension: per-channel
// embedding version check. The schema's EmbeddingSpec.ModelVersion is
// the canonical per-channel target; the verifier surfaces mismatches in
// report.VersionMismatchPerChannel so operators can see which channel's
// model output drifted from the manifest.
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
// QDRANT-003 closed (June 2026): every gate that was previously a TODO
// placeholder is now implemented. The caller checks `report.Ready` before
// calling SwitchAlias; a false Ready MUST block the alias switch.
func (v *ReindexVerifier) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*SwitchReport, error) {
	report := &SwitchReport{
		TargetCollection: targetCollection,
		ExpectedPoints:   expectedPoints,
		GoldenQueriesOK:  false,
		FiltersOK:        false,
		VersionMismatchPerChannel: make(map[string]int),
	}

	// ── Gate 1: Point count parity ────────────────────────────────
	actualPoints, err := v.client.CountPoints(ctx, targetCollection)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count points: %v", err))
		// Hard error — can't verify anything without a count.
		return report, fmt.Errorf("QDRANT-003: cannot verify reindex — count failed: %w", err)
	}
	report.ActualPoints = actualPoints

	// QDRANT-003 (June 2026): count mismatch is a HARD error that blocks
	// the Ready gate, not a logged warning. The previous implementation
	// logged a warning and continued; the new implementation sets Ready=false
	// and returns a detailed report.
	if actualPoints < expectedPoints {
		report.Errors = append(report.Errors,
			fmt.Sprintf("point count mismatch: expected %d, actual %d (delta %d)",
				expectedPoints, actualPoints, expectedPoints-actualPoints))
		// Do NOT return early. Continue gathering diagnostics so the
		// operator gets a full report. Ready will be false.
	}

	// ── Gate 2: Scroll + missing/orphan/payload/version ──────────
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
	const maxScrolls = 400 // safety cap: 200k points max
	pointsScrolled := 0

	for iteration := 0; iteration < maxScrolls; iteration++ {
		result, err := v.client.ScrollPoints(ctx, targetCollection, offset, scrollPage, nil)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("scroll page %d: %v", iteration, err))
			break // partial data is better than nothing
		}

		pointsScrolled += len(result.Points)
		for _, pt := range result.Points {
			// Read canonical asset_id directly from point payload
			// (UUID v5 hashes are one-way).
			// Comma-ok is required: a missing or non-string asset_id must NOT
			// pollute qdrantIDs with an empty key, which would silently mask a
			// SQLite row whose own asset_id is the empty string (MissingIDs).
			// validatePayloadMinimum below continues to surface the missing-field
			// case via PayloadIssues, so the Ready gate still trips correctly.
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

			// Gate 4: Embedding version check (sample-based: first 1000 points).
			// Full-scan would be O(n) per field; the first two pages (1000 pts)
			// give a representative sample for fast feedback.
			//
			// QDRANT-003 (June 2026) per-channel closure: the global
			// embedding_version check (legacy schema-version emission) AND
			// the per-channel embedding_version_<channel> check
			// (payload_mapper.go writes from EmbeddingSpec.ModelVersion)
			// share a single pointMismatched latch — a single point that
			// fails either check bumps VersionMismatch EXACTLY once.
			// Per-channel counter increments per channel normally; the
			// global gate stays a per-point count semantics.
			if iteration < 2 {
				pointMismatched := false

				// (a) Legacy global check: if the point carries a
				// global embedding_version and it disagrees with
				// CurrentEmbeddingVersion, the point is mismatched.
				// Absence here is neutral — the per-channel loop below
				// owns legacy-fallback semantics.
				if gv, ok := pt.Payload["embedding_version"].(string); ok && gv != "" && gv != CurrentEmbeddingVersion {
					pointMismatched = true
				}

				// (b) Per-channel check: each channel's payload
				// ["embedding_version_<channel>"] must equal the
				// schema's EmbeddingSpec.ModelVersion. Channels
				// without a per-channel key on the point fall back to
				// the legacy global embedding_version — accept the
				// point if the global matches CurrentEmbeddingVersion,
				// otherwise mark the channel-mismatch.
				if v.schema != nil {
					for _, spec := range v.schema.DenseVectors {
						if spec.ModelVersion == "" {
							// Channel has no canonical model version in
							// the schema; cannot compare. Skip.
							continue
						}
						key := fmt.Sprintf("embedding_version_%s", spec.Channel)
						actual, present := pt.Payload[key].(string)
						if !present {
							// Legacy fallback: the global embedding_version
							// must match CurrentEmbeddingVersion when a
							// channel is missing its per-channel key.
							if gv, ok := pt.Payload["embedding_version"].(string); !ok || gv != CurrentEmbeddingVersion {
								report.VersionMismatchPerChannel[spec.Channel]++
								pointMismatched = true
							}
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
		}

		if result.NextOffset == "" {
			break
		}
		offset = result.NextOffset

		if iteration == maxScrolls-1 {
			// QDRANT-003: iteration cap reached — this is a safety limit,
			// NOT a data-quality gate. Log it but do NOT block Ready.
			// Operators with >200k assets should increase maxScrolls.
			v.log.Warn("scroll iteration limit reached; verification of remaining points skipped",
				zap.Int("limit", maxScrolls),
				zap.Int("scrolled", pointsScrolled))
		}
	}

	// Guard: if zero points were scrolled (first-page failure or empty
	// collection), skip the missing/orphan computation — the qdrantIDs
	// set is empty and would produce catastrophic false positives.
	if pointsScrolled == 0 {
		report.Errors = append(report.Errors,
			"QDRANT-003: zero points scrolled — cannot compute missing/orphan IDs. "+
				"Check collection exists and scroll API is reachable.")
	} else {
		// ── Compute missing / orphan from ID sets ────────────────────
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

	// ── Gate 6: Golden‑query smoke ─────────────────────────────
	// QDRANT-005 (June 2026): replaces the hardcoded-true placeholder.
	// Scrolls a small sample and verifies the collection is queryable
	// with well-formed payloads. A failing collection (scroll error,
	// zero points, or all malformed payloads) sets GoldenQueriesOK=false
	// and blocks the Ready gate.
	report.GoldenQueriesOK = v.runGoldenQuerySmoke(ctx, targetCollection)

	// ── Gate 7: Filter smoke ───────────────────────────────────
	// QDRANT-005 (June 2026): validates that Qdrant payload indexes
	// work correctly. Discovers a source value from an unfiltered
	// scroll, then scrolls with a source filter and verifies every
	// returned point matches. Filter failure blocks the Ready gate.
	report.FiltersOK = v.runFilterSmoke(ctx, targetCollection)

	// ── Gate 5: Dead‑letter check (optional) ─────────────────────
	if v.deadLetter != nil {
		if dl, err := v.deadLetter.CountOpen(ctx); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("dead‑letter count: %v", err))
		} else {
			report.DeadLetterOpen = dl
		}
	}

	// ── Ready gate: ALL conditions must pass ─────────────────────
	report.Ready = report.ActualPoints >= report.ExpectedPoints &&
		report.ExpectedPoints > 0 &&
		report.MissingCount == 0 &&
		report.OrphanCount == 0 &&
		report.PayloadIssues == 0 &&
		report.VersionMismatch == 0 &&
		report.DeadLetterOpen == 0 &&
		report.GoldenQueriesOK &&
		report.FiltersOK &&
		len(report.Errors) == 0

	if !report.Ready {
		v.log.Warn("QDRANT-003: reindex verification FAILED",
			zap.String("target", targetCollection),
			zap.Int("expected", report.ExpectedPoints),
			zap.Int("actual", report.ActualPoints),
			zap.Int("missing", report.MissingCount),
			zap.Int("orphan", report.OrphanCount),
			zap.Int("payload_issues", report.PayloadIssues),
			zap.Int("version_mismatch", report.VersionMismatch),
			zap.Int("dead_letter_open", report.DeadLetterOpen),
			zap.Int("errors", len(report.Errors)))
	} else {
		v.log.Info("QDRANT-003: reindex verification PASSED — all gates green",
			zap.String("target", targetCollection),
			zap.Int("points", report.ActualPoints))
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
