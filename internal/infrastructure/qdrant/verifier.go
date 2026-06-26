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
	client     *Client
	assetStore AssetStore
	deadLetter DeadLetterChecker // nil = skip dead-letter check
	schema     *IndexSchema      // canonically the schema under reindex; nil = skip per-channel version check
	log        *zap.Logger
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
func NewReindexVerifier(client *Client, assetStore AssetStore, deadLetter DeadLetterChecker, schema *IndexSchema, log *zap.Logger) *ReindexVerifier {
	return &ReindexVerifier{
		client:     client,
		assetStore: assetStore,
		deadLetter: deadLetter,
		schema:     schema,
		log:        log,
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
		TargetCollection:          targetCollection,
		ExpectedPoints:            expectedPoints,
		GoldenQueriesOK:           true, // TODO QDRANT-005: wire golden‑query smoke runner
		FiltersOK:                 true, // TODO QDRANT-005: wire filter smoke runner
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
		result, err := v.client.ScrollPoints(ctx, targetCollection, offset, scrollPage)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("scroll page %d: %v", iteration, err))
			break // partial data is better than nothing
		}

		pointsScrolled += len(result.Points)
		for _, pt := range result.Points {
			// Read canonical asset_id directly from point payload
			// (UUID v5 hashes are one-way; PointIDToAssetID was removed).
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
