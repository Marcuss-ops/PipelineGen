// Package script — clip_binding_helpers_test.go: hermetic
// table-driven coverage for the canonical helpers in
// clip_binding_helpers.go.
//
// godlike/06 SSOT: pinning tests for the canonical
// `max(0, endMs - startMs)` math (the literal SSOT — no
// defensive clamp on negative startMs; the rollover signal is
// intentionally observable) + the deliberate 0-returning
// asset-level fallback (the "honest null" idiom — godlike/07
// NO-FAKE-AVAILABILITY).
package script

import "testing"

// ── ClipDurationMs: per-segment math ───────────────────────────────────

func TestClipDurationMs_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		startMs int64
		endMs   int64
		want    int64
	}{
		// The canonical positive segment — the workhorse case
		// pinned by scene_planner.go's binding-build contract.
		// Evidence StartMs=0 / EndMs=1000 →
		// binding.DurationMs == 1000.
		{"canonical positive segment (0 → 1000)", 0, 1000, 1000},

		// Mid-clip segment — the planner's typical case when
		// the clipper surfaces both StartMs and EndMs offsets.
		{"mid-clip segment (500 → 2000)", 500, 2000, 1500},

		// Reversed offsets — the segment math yields 0
		// (matches the godlike/07 NO-FAKE-AVAILABILITY
		// surface; downstream probes
		// `binding.DurationMs <= 0 → unknown duration` and
		// engages the asset-level fallback).
		{"reversed offsets (500 → 200)", 500, 200, 0},

		// Both zero — no segment info path. Caller's `<= 0`
		// guard engages the asset-level fallback; the
		// fallback itself returns 0 (the honest-null idiom).
		// The cumulative surface is "duration unknown" — not
		// fabricated.
		{"both zero (no segment info)", 0, 0, 0},

		// godlike/06 literal SSOT: the implementation is the
		// literal `max(0, endMs - startMs)` math WITHOUT a
		// defensive clamp on negative startMs. A negative
		// startMs surfaces a phantom non-zero DurationMs
		// (e.g. (-100, 200) → 200 - (-100) = 300). This is
		// INTENTIONAL: a poisoned upstream rollover masks the
		// problem in operator logs. Pinning the literal
		// behaviour here so any future "defensive clamp"
		// mutation breaks this test (not a silent operator
		// surprise).
		{"negative startMs is NOT clamped (-100 → 200) = literal 300", -100, 200, 300},

		// Both negative — the literal `endMs > startMs` check
		// still fires for (-100, -50) since -50 > -100.
		{"both negative (literal max)", -100, -50, 50},

		// Equal values — no time elapsed.
		{"start == end (1000 == 1000)", 1000, 1000, 0},

		// Just one ms apart.
		{"1ms segment", 999, 1000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClipDurationMs(tc.startMs, tc.endMs)
			if got != tc.want {
				t.Errorf("ClipDurationMs(%d, %d) = %d, want %d",
					tc.startMs, tc.endMs, got, tc.want)
			}
		})
	}
}

// ── ClipDurationMsFromAssetID: honest-null idiom ──────────────────────

// TestClipDurationMsFromAssetID_AlwaysZero pins the contract
// documented in clip_binding_helpers.go: this function MUST
// return 0 unconditionally because the script package has no
// asset-access port. Returning a non-zero here would either:
//
//	(a) constitute fake availability (godlike/07
//	    NO-FAKE-AVAILABILITY violation — fabricating a
//	    duration the layer doesn't own), or
//	(b) introduce an asset access dependency that breaks
//	    godlike/06 layering (importing an infrastructure
//	    package here).
//
// The "always zero" assertion below is the literal pin. The
// table covers canonical clip-id formats (the exact shape
// produced by the YouTube clip-id builder in
// `internal/application/scripts/usecase/...`), a uuid-shaped
// id (defensive — some callers pass through asset IDs from
// the artlist_atomic_writer lineage), an empty id
// (degenerate — nil-string handling), and a unicode-containing
// id (defensive — tests the parameter doesn't accidentally
// influence the return path).
func TestClipDurationMsFromAssetID_AlwaysZero(t *testing.T) {
	cases := []struct {
		name    string
		assetID string
	}{
		// Canonical clip-id format produced by the YouTube
		// builder (matches the scene_planner.go call site
		// fixture evidence).
		{"canonical clip id", "clip_yt_abc123_120_180_v1"},

		// UUID-shaped id (from the artlist_atomic_writer
		// lineage — defensive cover).
		{"uuid-shaped id", "550e8400-e29b-41d4-a716-446655440000"},

		// Empty asset id (degenerate — nay-string handling).
		{"empty asset id (degenerate)", ""},

		// Unicode-containing id (defensive — tests the
		// parameter doesn't accidentally influence the
		// return path).
		{"unicode-containing id (defensive)", "clip_文字_xyz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClipDurationMsFromAssetID(tc.assetID); got != 0 {
				t.Errorf("ClipDurationMsFromAssetID(%q) = %d, want 0 (scriptpkg has no asset access — assetID parameter MUST be ignored)",
					tc.assetID, got)
			}
		})
	}
}
