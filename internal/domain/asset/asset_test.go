package asset

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// ── Parity between this package and the legacy home ────────────────

// TestStateConstantsMatchAssets asserts that the State* lifecycle
// constants re-declared in this package stay in lockstep with the
// canonical definitions in internal/assets. If a future change to
// internal/assets tweaks a value, this test surfaces the drift in CI
// instead of letting it silently diverge.
//
// Until phase 3 of Wave 12 follow-up removes the legacy package,
// THIS test is the single source of truth that the two views agree.
// Once phase 3 deletes internal/assets, this test trivially
// degenerates (constants equal themselves) — that is the desired
// terminal state.
func TestStateConstantsMatchAssets(t *testing.T) {
	cases := []struct {
		name     string
		actual   assets.LifecycleState
		expected assets.LifecycleState
	}{
		{"StateStaging", StateStaging, assets.StateStaging},
		{"StateProcessing", StateProcessing, assets.StateProcessing},
		{"StateActive", StateActive, assets.StateActive},
		{"StateDeleted", StateDeleted, assets.StateDeleted},
		{"StateReady", StateReady, assets.StateReady},
		{"StatePending", StatePending, assets.StatePending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.actual != tc.expected {
				t.Errorf("drift detected: asset=%q internal/assets=%q",
					tc.actual, tc.expected)
			}
		})
	}
	// Per reviewer: %q prints LifecycleState as a string natively,
	// so no explicit string(...) casts needed in the format args.
}

// TestLocationKindConstantsMatchAssets asserts that the LocationKind*
// constants re-declared in this package stay in lockstep with the
// canonical definitions in internal/assets/location.go. Parity test
// added in Wave 12 follow-up Phase 2 PR-2; see internal/domain/asset/asset.go
// preamble for the migration rationale.
func TestLocationKindConstantsMatchAssets(t *testing.T) {
	cases := []struct {
		name     string
		actual   assets.LocationKind
		expected assets.LocationKind
	}{
		{"LocationKindDrive", LocationKindDrive, assets.LocationKindDrive},
		{"LocationKindLocal", LocationKindLocal, assets.LocationKindLocal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.actual != tc.expected {
				t.Errorf("drift detected: asset=%q internal/assets=%q",
					tc.actual, tc.expected)
			}
		})
	}
}

// TestLocationKindIsHardAlias confirms the LocationKind type alias is
// a true identity (not a wrapper). Same structural test as
// TestAssetIsHardAlias but for the LocationKind enum.
func TestLocationKindIsHardAlias(t *testing.T) {
	var a LocationKind = LocationKindDrive
	var legacy assets.LocationKind = a // no conversion: same type
	var back LocationKind = legacy
	if back != LocationKindDrive {
		t.Errorf("round-trip mismatch: got %q want %q", back, LocationKindDrive)
	}
}

// TestProcessingConstantsMatchAssets asserts that the Stage*/Status*
// const re-declarations and the ErrNotFound sentinel-error var stay
// in lockstep with the canonical definitions in
// internal/assets/processing_types.go and internal/assets/errors.go.
// Drift here would be SILENT under go build because of const+var
// re-declaration-by-value semantics — only this test surfaces it.
// Mirrors the discipline of TestStateConstantsMatchAssets and
// TestLocationKindConstantsMatchAssets. Wave 12 follow-up Phase 2
// PR-3 added these alongside the Stage*/Status* aliases.
func TestProcessingConstantsMatchAssets(t *testing.T) {
	// ProcessingStage / ProcessingStatus — typed string consts.
	// Use `any` rather than concrete types so a single table covers
	// both Stage* (assets.ProcessingStage) and Status* (assets.ProcessingStatus).
	// `%q` formats both string-based types identically for diagnostics.
	cases := []struct {
		name     string
		actual   any
		expected any
	}{
		{"StageUpload", StageUpload, assets.StageUpload},
		{"StageDownload", StageDownload, assets.StageDownload},
		{"StatusRunning", StatusRunning, assets.StatusRunning},
		{"StatusCompleted", StatusCompleted, assets.StatusCompleted},
		{"StatusFailed", StatusFailed, assets.StatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.actual != tc.expected {
				t.Errorf("drift detected: asset=%q internal/assets=%q",
					tc.actual, tc.expected)
			}
		})
	}
	// ErrNotFound — sentinel-error VAR re-binding. Compare via pointer
	// equality: `var ErrNotFound = assets.ErrNotFound` re-binds the
	// same *errors.errorString, so `==` works for `errors.Is`-compatible
	// comparisons. If internal/assets/errors.go swaps the sentinel for a
	// customErr type that breaks `==`, this test surfaces the drift.
	t.Run("ErrNotFound", func(t *testing.T) {
		if ErrNotFound != assets.ErrNotFound {
			t.Errorf("ErrNotFound drift: asset and internal/assets must point to same sentinel")
		}
	})
}

// TestAssetIsHardAlias confirms that asset.Asset is a type alias for
// assets.Asset (i.e. the same type, not a named type struct holding
// assets.Asset). If a future maintainer accidentally changes the
// declaration to `type Asset struct { Item assets.Asset; ... }`,
// the round-trip below would still compile but no longer be a
// zero-cost identity, breaking interchangeability between the two
// packages. The round-trip via assignability without conversion is
// the structural test.

// TestFunctionRebindingsMatchAssets asserts that the two function
// re-bindings (var X = assets.X) hold the same callable as the
// legacy package. Go's ==/!= operator PANICS when applied directly
// to function values (including via interface{}/any typing),
// so we use reflect.ValueOf().Pointer() to obtain the underlying
// code pointer as uintptr, which IS comparable across the alias
// boundary. Drift that would otherwise be silent — e.g. an
// unrelated `var` in either package shadowing the function — gets
// caught here. Signature drift is caught compile-time because
// `var X = assets.X` re-binds by value; this test is the runtime
// pointer backstop.
// YAGNI added in Wave 12 follow-up Phase 2 PR-4 because two new
// function re-bindings landed without parity coverage.
func TestFunctionRebindingsMatchAssets(t *testing.T) {
	cases := []struct {
		name     string
		actual   any
		expected any
	}{
		{"NewAssetStoreSQLite", NewAssetStoreSQLite, assets.NewAssetStoreSQLite},
		{"ScanCanonicalAssetRowsPublic", ScanCanonicalAssetRowsPublic, assets.ScanCanonicalAssetRowsPublic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Function values are not directly comparable in Go
			// (panic on ==/!=). Extract the underlying code
			// pointer via reflect.ValueOf(fn).Pointer() and
			// compare uintptr. Two top-level functions in
			// different packages that resolve to the same
			// callable declaration return the same Pointer.
			actualPtr := reflect.ValueOf(tc.actual).Pointer()
			expectedPtr := reflect.ValueOf(tc.expected).Pointer()
			if actualPtr != expectedPtr {
				t.Errorf("drift detected: asset.%s points to a different callable than assets.%s (uintptr %d vs %d, treat as noise across ASLR re-runs)",
					tc.name, tc.name, actualPtr, expectedPtr)
			}
		})
	}
}

// TestAssetIsHardAlias confirms that asset.Asset is a type alias for
// assets.Asset (i.e. the same type, not a named type struct holding
// assets.Asset). If a future maintainer accidentally changes the
// declaration to `type Asset struct { Item assets.Asset; ... }`,
// the round-trip below would still compile but no longer be a
// zero-cost identity, breaking interchangeability between the two
// packages. The round-trip via assignability without conversion is
// the structural test.
// YAGNI added in Wave 12 follow-up Phase 1.
func TestAssetIsHardAlias(t *testing.T) {
	a := Asset{ID: "test-asset"}
	var legacy assets.Asset = a // no conversion: same type via alias
	var back Asset = legacy     // no conversion: same type via alias
	if back.ID != "test-asset" {
		t.Errorf("round-trip ID mismatch: got %q want %q", back.ID, "test-asset")
	}
}
