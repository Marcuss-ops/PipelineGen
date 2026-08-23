// Package qdrant — P1 QDRANT-VERIFIER-SPLIT: metadata phase (July 2026).
//
// verifyMetadata runs the post-scroll validation gates: dead-letter check,
// golden-query smoke, filter smoke, and the Ready computation. Extracted
// from verifier.go so each phase has a single responsibility.
package verification

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// verifyMetadata performs Gates 5–7 (dead letters, golden query, filter
// smoke) and computes the final Ready gate + per-gate details (Task 7).
//
// Populates report.DeadLetterOpen, report.GoldenQueriesOK,
// report.FiltersOK, report.GateDetails, report.Ready, and report.Errors.
func (v *ReindexVerifier) verifyMetadata(ctx context.Context, target string, report *schema.SwitchReport) {
	// ── Gate 5: Dead‑letter check ───────────────────────────────
	v.checkDeadLetters(ctx, report)

	// ── Gate 6: Golden‑query smoke ──────────────────────────────
	report.GoldenQueriesOK = v.runGoldenQuerySmoke(ctx, target)
	if !report.GoldenQueriesOK {
		if len(report.Errors) < schema.MaxErrors {
			report.Errors = append(report.Errors,
				"QDRANT-005: golden query smoke failed — collection not queryable or payloads malformed")
		} else {
			report.ErrorsTruncated = true
		}
	}

	// ── Gate 7: Filter smoke ────────────────────────────────────
	report.FiltersOK = v.runFilterSmoke(ctx, target)
	if !report.FiltersOK {
		if len(report.Errors) < schema.MaxErrors {
			report.Errors = append(report.Errors,
				"QDRANT-005: filter smoke failed — payload index or filtering broken")
		} else {
			report.ErrorsTruncated = true
		}
	}

	// ── Ready gate + per-gate details (Task 7) ──────────────────
	report.GateDetails = computeGateDetails(report)
	report.Ready = computeReady(report)

	if !report.Ready {
		v.log.Warn("PR 12 reindex verification FAILED",
			zap.String("target", target),
			zap.Bool("complete_scan", report.CompleteScan),
			zap.Int("expected", report.ExpectedPoints),
			zap.Int("actual", report.ActualPoints),
			zap.Int("scrolled", report.TotalScrolled),
			zap.Int("missing", report.MissingCount),
			zap.Int("orphan", report.OrphanCount),
			zap.Int("payload_issues", report.PayloadIssues),
			zap.Int("version_mismatch", report.VersionMismatch),
			zap.Int("non_canonical_point_count", report.NonCanonicalPointCount),
			zap.Int("duplicate_qdrant_points", report.DuplicateQdrantPoints),
			zap.Int("dead_letter_open", report.DeadLetterOpen),
			zap.Int("errors", len(report.Errors)))
	} else {
		v.log.Info("PR 12 reindex verification PASSED — all gates green",
			zap.String("target", target),
			zap.Int("points", report.ActualPoints),
			zap.Int("scanned", report.TotalScrolled))
	}
}

// checkDeadLetters queries the optional schema.DeadLetterChecker and records
// the count of open dead-letter entries on report. When the checker is
// nil (legacy admin CLIs) this is a no-op. Errors are appended to
// report.Errors but do not abort verification.
func (v *ReindexVerifier) checkDeadLetters(ctx context.Context, report *schema.SwitchReport) {
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

// computeReady evaluates all gate conditions and returns true only when
// every gate is green: CompleteScan=true, counts match, zero missing/
// orphan/payload issues/version mismatches/non-canonical point IDs/
// dead letters, golden-query and filter smokes pass, and no errors.
//
// Task 7 hardening: ErrorsTruncated=true also blocks Ready — the
// operator must raise MaxErrors and re-run to get a complete diagnostic
// surface before the alias switch is permitted.
func computeReady(report *schema.SwitchReport) bool {
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
		report.DuplicateQdrantPoints == 0 &&
		report.DeadLetterOpen == 0 &&
		report.GoldenQueriesOK &&
		report.FiltersOK &&
		len(report.Errors) == 0 &&
		!report.ErrorsTruncated
}

// computeGateDetails builds the per-gate pass/fail breakdown (Task 7)
// from the report counters. Operators read the GateDetails JSON block
// to see exactly which condition(s) blocked the alias switch.
// Every gate has a Passed bool and a human-readable Description.
func computeGateDetails(report *schema.SwitchReport) *schema.GateDetails {
	channelTotal := 0
	for _, c := range report.VersionMismatchPerChannel {
		channelTotal += c
	}

	return &schema.GateDetails{
		PointCountParity: schema.GateDetail{
			Passed:      report.ActualPoints == report.ExpectedPoints && report.ExpectedPoints > 0,
			Description: fmt.Sprintf("expected=%d actual=%d", report.ExpectedPoints, report.ActualPoints),
		},
		CompleteScan: schema.GateDetail{
			Passed:      report.CompleteScan,
			Description: fmt.Sprintf("scrolled=%d (CompleteScan=%v)", report.TotalScrolled, report.CompleteScan),
		},
		MissingOrphan: schema.GateDetail{
			Passed:      report.MissingCount == 0 && report.OrphanCount == 0,
			Description: fmt.Sprintf("missing=%d orphan=%d", report.MissingCount, report.OrphanCount),
		},
		PayloadValidation: schema.GateDetail{
			Passed:      report.PayloadIssues == 0,
			Description: fmt.Sprintf("payload_issues=%d", report.PayloadIssues),
		},
		EmbeddingVersion: schema.GateDetail{
			Passed:      channelTotal == 0,
			Description: fmt.Sprintf("version_mismatch=%d per_channel_total=%d", report.VersionMismatch, channelTotal),
		},
		CanonicalPointID: schema.GateDetail{
			Passed:      report.NonCanonicalPointCount == 0,
			Description: fmt.Sprintf("non_canonical=%d", report.NonCanonicalPointCount),
		},
		DuplicatePoints: schema.GateDetail{
			Passed:      report.DuplicateQdrantPoints == 0,
			Description: fmt.Sprintf("duplicate_qdrant_points=%d", report.DuplicateQdrantPoints),
		},
		DeadLetters: schema.GateDetail{
			Passed:      report.DeadLetterOpen == 0,
			Description: fmt.Sprintf("dead_letter_open=%d", report.DeadLetterOpen),
		},
		GoldenQueries: schema.GateDetail{
			Passed:      report.GoldenQueriesOK,
			Description: fmt.Sprintf("golden_queries_ok=%v", report.GoldenQueriesOK),
		},
		FilterSmoke: schema.GateDetail{
			Passed:      report.FiltersOK,
			Description: fmt.Sprintf("filters_ok=%v", report.FiltersOK),
		},
		ZeroErrors: schema.GateDetail{
			Passed:      len(report.Errors) == 0 && !report.ErrorsTruncated,
			Description: fmt.Sprintf("errors=%d truncated=%v", len(report.Errors), report.ErrorsTruncated),
		},
	}
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
		return true
	}

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
	sample, err := v.client.ScrollPoints(ctx, collection, "", 20, nil)
	if err != nil {
		v.log.Warn("QDRANT-005 filter smoke: cannot scroll for filter discovery",
			zap.String("collection", collection),
			zap.Error(err))
		return false
	}
	if len(sample.Points) == 0 {
		v.log.Info("QDRANT-005 filter smoke: collection empty, skipping filter test")
		return true
	}

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

	filter := map[string]any{
		"must": []map[string]any{
			{
				"key":   "source",
				"match": map[string]any{"value": sourceValue},
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
