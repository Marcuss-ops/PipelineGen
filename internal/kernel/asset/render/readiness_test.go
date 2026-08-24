// Package asset — readiness_test.go (PR-CATALOG-MULTILINGUA
// step 7, July 2026).
//
// Pins EvaluateMultilingualReadiness's contract. The
// multilingual gate is a pure function of the deps tuple,
// so the tests use in-memory closures that mirror the
// production collaborator surfaces without any I/O.
//
// godlike/06 SSOT: this test file is the SOLE canonical
// regression pin for the readiness predicate. Adding a
// 6th gate to readiness.go MUST add a corresponding test
// table row here AND a stub closure AND increment
// CanonicalGateRequirementValues.
package render

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// stubDepsAllPass returns a deps tuple where every gate
// passes — used as the all-pass baseline. Tests mutate
// the returned struct's closure fields to flip ONE gate at
// a time.
func stubDepsAllPass(t *testing.T) MultilingualGateDeps {
	t.Helper()
	return MultilingualGateDeps{
		VerifyDriveRenderMaster: func(ctx context.Context, driveFileID, contentHash string) (bool, error) {
			return true, nil
		},
		ListOriginalsPresent: func(ctx context.Context, assetID string) (bool, bool, bool, error) {
			return true, true, true, nil
		},
		ListEnabledLanguages: func() []LanguageSpec {
			return []LanguageSpec{
				{Code: "it", Enabled: true, TranslateClips: true},
				{Code: "en", Enabled: true, TranslateClips: true},
				{Code: "es", Enabled: true, TranslateClips: true},
			}
		},
		ListCurrentTracksForAsset: func(ctx context.Context, assetID string) (map[string]bool, error) {
			return map[string]bool{
				"it": true,
				"en": true,
				"es": true,
			}, nil
		},
		VerifyQdrantPoint: func(ctx context.Context, assetID string) (bool, bool, error) {
			return true, true, nil
		},
		VerifyOutboxEmpty: func(ctx context.Context, assetID string) (int, error) {
			return 0, nil
		},
	}
}

// TestEvaluateMultilingualReadiness_AllPassed verifies the
// happy path: all 5 gates pass → Passed=true, Missing=[],
// Diagnostic renders the diagnostic breakdown.
func TestEvaluateMultilingualReadiness_AllPassed(t *testing.T) {
	deps := stubDepsAllPass(t)
	audit, err := EvaluateMultilingualReadiness(
		context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assert.True(t, audit.Passed, "all gates pass; want Passed=true")
	assert.Len(t, audit.Missing, 0, "all gates pass; want Missing=[]")
	assert.Contains(t, audit.Diagnostic, "language registry enabled count: 3",
		"diagnostic surfaces the configured language count even on all-pass")
}

// TestEvaluateMultilingualReadiness_OneFailureEach pins
// the failure-mode coverage for each of the 5 gates in
// isolation (with the originals-present gate split into
// 3 sub-cases for transcript/description/visual_summary).
// Each subtest flips ONE gate to return failure and
// asserts the corresponding Missing entry surfaces.
func TestEvaluateMultilingualReadiness_OneFailureEach(t *testing.T) {
	cases := []struct {
		name        string
		modify      func(*MultilingualGateDeps)
		wantMissing GateRequirement
	}{
		{
			name: "drive_render_master_not_verified",
			modify: func(d *MultilingualGateDeps) {
				d.VerifyDriveRenderMaster = func(ctx context.Context, driveFileID, contentHash string) (bool, error) {
					return false, nil
				}
			},
			wantMissing: GateRenderMasterOnDrive,
		},
		{
			name: "transcript_missing",
			modify: func(d *MultilingualGateDeps) {
				d.ListOriginalsPresent = func(ctx context.Context, assetID string) (bool, bool, bool, error) {
					return false, true, true, nil
				}
			},
			wantMissing: GateOriginalsPresent,
		},
		{
			name: "description_missing",
			modify: func(d *MultilingualGateDeps) {
				d.ListOriginalsPresent = func(ctx context.Context, assetID string) (bool, bool, bool, error) {
					return true, false, true, nil
				}
			},
			wantMissing: GateOriginalsPresent,
		},
		{
			name: "visual_summary_missing",
			modify: func(d *MultilingualGateDeps) {
				d.ListOriginalsPresent = func(ctx context.Context, assetID string) (bool, bool, bool, error) {
					return true, true, false, nil
				}
			},
			wantMissing: GateOriginalsPresent,
		},
		{
			name: "language_it_missing",
			modify: func(d *MultilingualGateDeps) {
				d.ListCurrentTracksForAsset = func(ctx context.Context, assetID string) (map[string]bool, error) {
					return map[string]bool{
						"en": true,
						"es": true,
					}, nil
				}
			},
			wantMissing: GateRequiredLanguagesPresent,
		},
		{
			name: "qdrant_point_missing",
			modify: func(d *MultilingualGateDeps) {
				d.VerifyQdrantPoint = func(ctx context.Context, assetID string) (bool, bool, error) {
					return false, true, nil
				}
			},
			wantMissing: GateQdrantUpdated,
		},
		{
			name: "qdrant_hash_mismatch",
			modify: func(d *MultilingualGateDeps) {
				d.VerifyQdrantPoint = func(ctx context.Context, assetID string) (bool, bool, error) {
					return true, false, nil
				}
			},
			wantMissing: GateQdrantUpdated,
		},
		{
			name: "outbox_not_empty",
			modify: func(d *MultilingualGateDeps) {
				d.VerifyOutboxEmpty = func(ctx context.Context, assetID string) (int, error) {
					return 1, nil
				}
			},
			wantMissing: GateOutboxEmpty,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps := stubDepsAllPass(t)
			c.modify(&deps)
			audit, err := EvaluateMultilingualReadiness(
				context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
			)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			assert.False(t, audit.Passed, "want Passed=false")
			assert.Contains(t, audit.Missing, c.wantMissing,
				"Missing must surface the failing gate")
			assert.NotEmpty(t, audit.Diagnostic,
				"Diagnostic surfaces even on failure")
		})
	}
}

// TestEvaluateMultilingualReadiness_MultipleFailures pins
// that multiple gates failing surface ALL of them in the
// Missing list (not just the first). The Diagnostic
// string should reflect each failing gate.
func TestEvaluateMultilingualReadiness_MultipleFailures(t *testing.T) {
	deps := stubDepsAllPass(t)
	deps.VerifyDriveRenderMaster = func(ctx context.Context, driveFileID, contentHash string) (bool, error) {
		return false, nil
	}
	deps.VerifyOutboxEmpty = func(ctx context.Context, assetID string) (int, error) {
		return 3, nil
	}
	audit, err := EvaluateMultilingualReadiness(
		context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assert.False(t, audit.Passed)
	assert.Contains(t, audit.Missing, GateRenderMasterOnDrive,
		"Missing must contain drive gate")
	assert.Contains(t, audit.Missing, GateOutboxEmpty,
		"Missing must contain outbox gate")
	assert.Len(t, audit.Missing, 2, "exactly 2 gates failed; missing = 2 elements")
	assert.Contains(t, audit.Diagnostic, "render_master not verified")
	assert.Contains(t, audit.Diagnostic, "outbox has 3 pending events")
}

// TestEvaluateMultilingualReadiness_AssetIDEmpty pins the
// godlike/07 fail-closed contract: an empty asset_id is
// rejected with a typed error rather than a silent
// false-positive.
func TestEvaluateMultilingualReadiness_AssetIDEmpty(t *testing.T) {
	deps := stubDepsAllPass(t)
	audit, err := EvaluateMultilingualReadiness(
		context.Background(), deps, "", "drive-file-1", "hash-1",
	)
	assert.Error(t, err, "empty asset_id surfaces as a typed error")
	assert.ErrorIs(t, err, ErrReadinessPredicateAssetIDEmpty,
		"error must be ErrReadinessPredicateAssetIDEmpty")
	assert.Equal(t, ReadinessAudit{}, audit,
		"audit is zero-value on asset_id-empty error")
}

// TestEvaluateMultilingualReadiness_NilDeps pins that
// each deps function being nil surfaces as a typed
// error rather than a panic. The error message wraps
// the failing gate's name.
func TestEvaluateMultilingualReadiness_NilDeps(t *testing.T) {
	cases := []struct {
		name     string
		nilSet   func(*MultilingualGateDeps)
		wantGate GateRequirement
	}{
		{"nil-drive", func(d *MultilingualGateDeps) { d.VerifyDriveRenderMaster = nil }, GateRenderMasterOnDrive},
		{"nil-originals", func(d *MultilingualGateDeps) { d.ListOriginalsPresent = nil }, GateOriginalsPresent},
		{"nil-qdrant", func(d *MultilingualGateDeps) { d.VerifyQdrantPoint = nil }, GateQdrantUpdated},
		{"nil-outbox", func(d *MultilingualGateDeps) { d.VerifyOutboxEmpty = nil }, GateOutboxEmpty},
		{"nil-list-enabled", func(d *MultilingualGateDeps) { d.ListEnabledLanguages = nil }, GateRequiredLanguagesPresent},
		// nil-list-current-tracks only matters when registry
		// is non-empty; stubDepsAllPass sets registry to 3
		// codes -> ListCurrentTracksForAsset is consulted ->
		// nil -> typed error from gate (c).
		{"nil-list-current-tracks", func(d *MultilingualGateDeps) { d.ListCurrentTracksForAsset = nil }, GateRequiredLanguagesPresent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps := stubDepsAllPass(t)
			c.nilSet(&deps)
			_, err := EvaluateMultilingualReadiness(
				context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
			)
			assert.Error(t, err, "nil dep field must surface as typed error")
			assert.ErrorIs(t, err, ErrReadinessPredicateDepsNil,
				"error must be ErrReadinessPredicateDepsNil")
			assert.Contains(t, err.Error(), string(c.wantGate),
				"error message must name the failing gate")
		})
	}
}

// TestEvaluateMultilingualReadiness_DepErrors pins that
// each deps function returning a non-nil error propagates
// as ErrReadinessPredicateDependency wrapper.
func TestEvaluateMultilingualReadiness_DepErrors(t *testing.T) {
	cases := []struct {
		name    string
		breaker func(*MultilingualGateDeps)
	}{
		{"drive-error", func(d *MultilingualGateDeps) {
			d.VerifyDriveRenderMaster = func(ctx context.Context, driveFileID, contentHash string) (bool, error) {
				return false, errors.New("drive unavailable")
			}
		}},
		{"originals-error", func(d *MultilingualGateDeps) {
			d.ListOriginalsPresent = func(ctx context.Context, assetID string) (bool, bool, bool, error) {
				return false, false, false, errors.New("asset_text_tracks reader unavailable")
			}
		}},
		{"current-tracks-error", func(d *MultilingualGateDeps) {
			d.ListCurrentTracksForAsset = func(ctx context.Context, assetID string) (map[string]bool, error) {
				return nil, errors.New("asset_text_tracks reader unavailable")
			}
		}},
		{"qdrant-error", func(d *MultilingualGateDeps) {
			d.VerifyQdrantPoint = func(ctx context.Context, assetID string) (bool, bool, error) {
				return false, false, errors.New("qdrant unavailable")
			}
		}},
		{"outbox-error", func(d *MultilingualGateDeps) {
			d.VerifyOutboxEmpty = func(ctx context.Context, assetID string) (int, error) {
				return -1, errors.New("outbox unavailable")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps := stubDepsAllPass(t)
			c.breaker(&deps)
			_, err := EvaluateMultilingualReadiness(
				context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
			)
			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrReadinessPredicateDependency)
			// Original error message must be propagated via wrap.
			assert.Contains(t, err.Error(), "unavailable",
				"wrapped error message preserves the underlying cause")
		})
	}
}

// TestEvaluateMultilingualReadiness_EmptyLanguageRegistry
// pins that an empty EnabledLanguages list is treated as
// "multilingual pipeline disabled" — NOT a gate failure.
// The empty-registry case keeps Passed=true when every
// other gate passes; the deployment-mode (multilingual
// enabled or not) is a configuration decision, not a gate.
func TestEvaluateMultilingualReadiness_EmptyLanguageRegistry(t *testing.T) {
	deps := stubDepsAllPass(t)
	deps.ListEnabledLanguages = func() []LanguageSpec {
		return []LanguageSpec{}
	}
	deps.ListCurrentTracksForAsset = func(ctx context.Context, assetID string) (map[string]bool, error) {
		return map[string]bool{}, nil
	}
	audit, err := EvaluateMultilingualReadiness(
		context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assert.True(t, audit.Passed,
		"empty registry is multilingual-pipeline-disabled, NOT a gate failure")
	assert.Len(t, audit.Missing, 0, "all other gates passed")
	assert.Contains(t, audit.Diagnostic, "multilingual pipeline disabled",
		"diagnostic surfaces the disabled-pipeline state")
}

// TestEvaluateMultilingualReadiness_DisabledSpecNotFannedOut
// pins the capability-filter contract (mirror of the
// language_registry.go interface-level contract): a spec
// with Enabled=false is registered (audit trail) but is
// NOT in the gateway's required-languages set. The
// predicate MUST NOT fail on a code that is registered
// but disabled; only Enabled=true codes are required.
func TestEvaluateMultilingualReadiness_DisabledSpecNotFannedOut(t *testing.T) {
	deps := stubDepsAllPass(t)
	// Registry has it/en/es with it disabled.
	deps.ListEnabledLanguages = func() []LanguageSpec {
		return []LanguageSpec{
			{Code: "it", Enabled: false, TranslateClips: true},
			{Code: "en", Enabled: true, TranslateClips: true},
			{Code: "es", Enabled: true, TranslateClips: true},
		}
	}
	// Current tracks: en, es (intentionally missing it).
	deps.ListCurrentTracksForAsset = func(ctx context.Context, assetID string) (map[string]bool, error) {
		return map[string]bool{
			"en": true,
			"es": true,
		}, nil
	}
	audit, err := EvaluateMultilingualReadiness(
		context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assert.True(t, audit.Passed,
		"disabled codes are not in the required-languages gateway; passing current-track for them is not required")
	assert.Len(t, audit.Missing, 0)
}

// TestEvaluateMultilingualReadiness_DiagnosticSortsLanguages
// pins the diagnostic alphabetical-order contract: missing
// language codes are reported in sorted order so the
// operator's terminal-rendered output is stable across
// runs (chronological ordering differs from language
// registry declaration order).
//
// Setup: stubDepsAllPass yields enabled=[it, en, es]; the
// override makes current-tracks only contain codes c, d, e —
// so all three enabled codes (it, en, es) are MISSING. The
// diagnostic must surface them in alphabetical order:
//
//	en < es < it
//
// (NOT in registry declaration order, which is it < en < es.)
func TestEvaluateMultilingualReadiness_DiagnosticSortsLanguages(t *testing.T) {
	deps := stubDepsAllPass(t)
	deps.ListCurrentTracksForAsset = func(ctx context.Context, assetID string) (map[string]bool, error) {
		// all enabled codes (it/en/es) are missing; sort
		// must reorder them as en, es, it.
		return map[string]bool{
			"c": true, "d": true, "e": true,
		}, nil
	}
	audit, err := EvaluateMultilingualReadiness(
		context.Background(), deps, "asset-1", "drive-file-1", "hash-1",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assert.False(t, audit.Passed)
	assert.Contains(t, audit.Diagnostic,
		"required languages missing current tracks: en, es, it",
		"diagnostic surfaces the 3 missing codes (it/en/es from the registry) in alphabetical order — NOT registry declaration order — for stable operator output")
}

// TestCanonicalGateRequirementValues pins the closed set
// of 5 gate names. A 6th gate added in a future PR
// requires a corresponding test-table row here AND a
// stub closure in stubDepsAllPass.
func TestCanonicalGateRequirementValues(t *testing.T) {
	assert.Len(t, CanonicalGateRequirementValues(), 5,
		"CanonicalGateRequirementValues must return exactly 5 gates")
	expected := []GateRequirement{
		GateRenderMasterOnDrive,
		GateOriginalsPresent,
		GateRequiredLanguagesPresent,
		GateQdrantUpdated,
		GateOutboxEmpty,
	}
	assert.Equal(t, expected, CanonicalGateRequirementValues(),
		"CanonicalGateRequirementValues must return in declared order")
}
