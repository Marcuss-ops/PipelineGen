// validation_test.go pins the canonical validation contract for
// POST /api/stock-pipeline/run + POST /api/stock-pipeline/search-and-run
// (PR-STOCK-DRY-VALIDATION, 2026-07-08).
//
// godlike/06 SSOT (one canonical owner per fact): the test surface
// imports + invokes applyStockDefaults + stockValidationInput +
// stockValidationDefaults DIRECTLY from handler.go — NO re-implementation,
// NO type re-declaration. Drift in the canonical helper surface
// surfaces here as build failure (compile-time), not runtime panic.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion checks a falsifiable
// surface — the EXACT byte-equivalent error message literals that the
// production HTTP response surfaces to operators. A regression that
// rewords any literal surfaces as test failure BEFORE the regression
// reaches the HTTP layer (parity with operator smoke assertions).
package stock

import (
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// TestApplyStockDefaults_ValidationContract is the canonical
// table-driven test that pins the 7-step validation contract for
// BOTH wire-field-name variants ("search_queries, ..." for /run vs
// "queries, ..." for /search-and-run).
//
// Pre-PR: each endpoint had an INLINE 22-line if/else block with the
// EXACT same logic — godlike/07 minimum-blast-radius DRY violation.
// Post-PR: applyStockDefaults is the SOLE canonical surface; both
// handler endpoints invoke it with their context-appropriate
// sourcesEmptyMsg.
func TestApplyStockDefaults_ValidationContract(t *testing.T) {
	cases := []struct {
		name            string
		sourcesEmptyMsg string
		input           stockValidationInput
		wantErr         string // exact literal; "" = success
	}{
		// ── 1. Empty-all-sources gate (both wire variants) ────────
		{"run_empty_all_sources",
			"search_queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{},
			"search_queries, direct_urls, drive_urls, or clips required"},
		{"searchandrun_empty_all_sources",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{},
			"queries, direct_urls, drive_urls, or clips required"},

		// ── 2. Clips no-URL gate ───────────────────────────────────
		{"clips_no_url_rejected",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{Clips: []stockpipeline.ClipSpec{{URL: ""}}},
			"clips require at least one clip with a non-empty url"},
		{"clips_one_with_url_passes",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{Clips: []stockpipeline.ClipSpec{{URL: "https://example.com/v.mp4"}}},
			""},

		// ── 3. Single-source-pass paths (sanity) ─────────────────
		{"search_sources_only",
			"search_queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{SearchSourceCount: 1},
			""},
		{"direct_urls_only",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1},
			""},
		{"drive_urls_only",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DriveURLsCount: 1},
			""},

		// ── 4. ClipDuration validation ───────────────────────────
		{"clip_duration_negative_rejected",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, ClipDuration: -1},
			"clip_duration must be >= 0"},
		{"clip_duration_zero_defaults_10",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, ClipDuration: 0},
			""},
		{"clip_duration_below_3_rejected",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, ClipDuration: 2},
			"clip_duration must be between 3 and 30 seconds"},
		{"clip_duration_above_30_rejected",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, ClipDuration: 31},
			"clip_duration must be between 3 and 30 seconds"},
		{"clip_duration_min_3_boundary_ok",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, ClipDuration: 3},
			""},
		{"clip_duration_max_30_boundary_ok",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, ClipDuration: 30},
			""},

		// ── 5. TotalMinutes defaulting (≤0 → 5) ───────────────────
		{"total_minutes_negative_defaults_5",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, TotalMinutes: -1},
			""},
		{"total_minutes_zero_defaults_5",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, TotalMinutes: 0},
			""},

		// ── 6. Async / Persist invariants ────────────────────────
		// Note: Persist default assertion is verified in
		// TestApplyStockDefaults_AsyncFalseFlipsPersist + AsyncTrue
		// below; this table-driven just confirms no-error happy
		// paths exist.
		{"async_false_passes",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, Async: false},
			""},
		{"async_true_passes",
			"queries, direct_urls, drive_urls, or clips required",
			stockValidationInput{DirectURLsCount: 1, Async: true},
			""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adjusted, err := applyStockDefaults(tc.sourcesEmptyMsg, tc.input)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("expected error %q, got %q (byte-equivalence regression in canonical wire-error literal)", tc.wantErr, err.Error())
				}
				// On error path, defaults should be zero-valued.
				if adjusted.TotalMinutes != 0 || adjusted.ClipDuration != 0 || adjusted.Persist != false {
					t.Fatalf("on error path expected zero-value defaults, got %+v", adjusted)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Defaulting invariants for success paths.
			expectedMin := 5
			if tc.input.TotalMinutes > 0 {
				expectedMin = tc.input.TotalMinutes
			}
			expectedDur := 10
			if tc.input.ClipDuration > 0 {
				expectedDur = tc.input.ClipDuration
			}
			if adjusted.TotalMinutes != expectedMin {
				t.Errorf("TotalMinutes = %d, want %d (input.TotalMinutes=%d)", adjusted.TotalMinutes, expectedMin, tc.input.TotalMinutes)
			}
			if adjusted.ClipDuration != expectedDur {
				t.Errorf("ClipDuration = %d, want %d (input.ClipDuration=%d)", adjusted.ClipDuration, expectedDur, tc.input.ClipDuration)
			}
			expectedPersist := !tc.input.Async
			if adjusted.Persist != expectedPersist {
				t.Errorf("Persist = %v, want %v (Async=%v)", adjusted.Persist, expectedPersist, tc.input.Async)
			}
		})
	}
}

// TestApplyStockDefaults_AsyncFalseFlipsPersist pins the sync-mode
// invariant (godlike/07 typed-defaulting contract): when Async=false
// on the wire, the returned Persist MUST be true so the runner's
// resilient path (orchestrator.upload + orchestrator.finalize +
// orchestrator.index) actually runs instead of stopping at the
// legacy manifest-only flow. A regression that breaks this contract
// means sync operators silently get half-state jobs.
func TestApplyStockDefaults_AsyncFalseFlipsPersist(t *testing.T) {
	adjusted, err := applyStockDefaults(
		"search_queries, direct_urls, drive_urls, or clips required",
		stockValidationInput{
			SearchSourceCount: 1,
			DirectURLsCount:   0,
			DriveURLsCount:    0,
			Clips:             nil,
			TotalMinutes:      1,
			ClipDuration:      10,
			Async:             false,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !adjusted.Persist {
		t.Errorf("Persist should be TRUE when Async=false (sync enables resilient path: upload + finalize + index), got false")
	}
}

// TestApplyStockDefaults_AsyncTruePreservesPersistFalse is the
// inverse of the previous test: when Async=true (the operator's
// default wire value), Persist MUST stay false so the request flows
// through the canonical jobs-broker path WITHOUT triggering the
// in-process resilient path on the api handler side. (The HandleJob
// worker independently decides whether to persist on its side.)
func TestApplyStockDefaults_AsyncTruePreservesPersistFalse(t *testing.T) {
	adjusted, err := applyStockDefaults(
		"search_queries, direct_urls, drive_urls, or clips required",
		stockValidationInput{
			SearchSourceCount: 1,
			DirectURLsCount:   0,
			DriveURLsCount:    0,
			Clips:             nil,
			TotalMinutes:      1,
			ClipDuration:      10,
			Async:             true,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adjusted.Persist {
		t.Errorf("Persist should be FALSE when Async=true (async path does NOT enable api-side resilient flag), got true")
	}
}

// TestApplyStockDefaults_ClipsBoundarySemantics pins the 2-clip
// scenario: one clip with empty URL + one with non-empty URL MUST
// pass (the helper breaks on the loop at first non-empty URL). A
// regression that requires ALL clips to have URLs would fail this
// case (currently NO — one non-empty URL is sufficient).
func TestApplyStockDefaults_ClipsOneNonEmptyPasses(t *testing.T) {
	adjusted, err := applyStockDefaults(
		"search_queries, direct_urls, drive_urls, or clips required",
		stockValidationInput{
			Clips: []stockpipeline.ClipSpec{
				{URL: ""},
				{URL: "https://example.com/v.mp4"},
			},
			TotalMinutes: 1,
			ClipDuration: 10,
			Async:        true,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adjusted.TotalMinutes != 1 {
		t.Errorf("TotalMinutes = %d, want 1", adjusted.TotalMinutes)
	}
	if adjusted.ClipDuration != 10 {
		t.Errorf("ClipDuration = %d, want 10", adjusted.ClipDuration)
	}
}

// TestApplyStockDefaults_ClipsAllEmptyRejected locks the inverse:
// when ALL clips have empty URLs, the helper MUST reject — failure
// signature is the canonical "clips require at least one clip with
// a non-empty url" string.
func TestApplyStockDefaults_ClipsAllEmptyRejected(t *testing.T) {
	_, err := applyStockDefaults(
		"search_queries, direct_urls, drive_urls, or clips required",
		stockValidationInput{
			Clips: []stockpipeline.ClipSpec{
				{URL: ""},
				{URL: ""},
				{URL: ""},
			},
		},
	)
	if err == nil {
		t.Fatalf("expected error when all clips have empty URLs")
	}
	wantLiteral := "clips require at least one clip with a non-empty url"
	if err.Error() != wantLiteral {
		t.Fatalf("expected error literal %q, got %q", wantLiteral, err.Error())
	}
}
