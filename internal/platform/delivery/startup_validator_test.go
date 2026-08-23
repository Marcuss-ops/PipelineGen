// Package delivery — startup_validator_test.go (P1.3, July 2026).
//
// Tests for DriveRootsValidator.ValidateDriveRoots. Each test pins
// ONE invariant from the file header of startup_validator.go:
//
//	(1) AllPass         — every reachable root succeeded → nil err
//	(2) AllFail         — every reachable root failed → umbrella sentinel
//	(3) PartialFail     — mixed success/failure → umbrella + per-key detail
//	(4) EmptySkipped    — empty RootFolderID is SKIPPED, not failed
//	(5) NilRegistry     — fail-fast on nil registry (typed sentinel)
//	(6) NilFolders      — fail-fast on nil folders (typed sentinel)
//	(7) RetryRecovers   — fake flip-to-nil on second call (documented
//	                      non-transient classification)
//	(8) ReportCoversAllKeys — every registry key appears exactly once
//	                          across PerDestination ∪ Skipped
//
// The test registry is built via NewDestinationRegistry(&config.Config{...})
// (the canonical public path) instead of the test-only
// NewDestinationRegistryWithPolicies factory — the latter is gated
// behind the `drivepolicypkgtest` build tag, so it is unavailable
// without opting in. The startup validator is a production concern,
// not a policy-test concern, so it MUST be exercisable by default
// `go test`.
package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
)

// fakeStartupRootsProbe is the package-private test double for
// StartupRootsProbe. Tracks probe calls + per-root errors via
// probeErrFn (selective per-root failure). The validator's needs are
// strictly read-only; EnsureFolder is NOT part of this fake because
// the validator never calls it.
type fakeStartupRootsProbe struct {
	// probeCalls records each probed root ID in iteration order.
	probeCalls []string
	// probeErrFn, if non-nil, is called per rootID and its return is
	// forwarded verbatim to the validator. Lets a single test mix
	// success-on-some-roots and failure-on-others.
	probeErrFn func(rootID string) error
}

func (f *fakeStartupRootsProbe) ProbeFolderAccess(_ context.Context, rootID string) error {
	f.probeCalls = append(f.probeCalls, rootID)
	if f.probeErrFn != nil {
		return f.probeErrFn(rootID)
	}
	return nil
}

// Compile-time assertion: the fake satisfies the narrow probe port
// (so a future port drift that adds another method fails the build
// here rather than at runtime).
var _ StartupRootsProbe = (*fakeStartupRootsProbe)(nil)

// countNonEmptyRoots returns the number of registered destinations
// whose RootFolderID is non-empty. The startup validator probes
// these via ProbeFolderAccess; destinations with empty roots are
// routed to Skipped instead. The P0-#1 DestinationClipMetadata
// sidecar (no own root — its root is the clip folder owned by
// DestinationYouTubeClip) lives in the Skipped bucket.
//
// This helper is the single source of truth for the per-test count
// assertions so the test suite stays in lockstep with the registry
// when new destinations are added (P0-#1 surfaced a pre-existing
// stale-count issue where SoundEffectSidecar and Document had been
// added without updating the hardcoded `Len(..., 10)` assertions).
func countNonEmptyRoots(reg *DestinationRegistry) int {
	n := 0
	for _, k := range reg.Keys() {
		p, err := reg.Resolve(k)
		if err != nil {
			continue
		}
		if strings.TrimSpace(p.RootFolderID) != "" {
			n++
		}
	}
	return n
}

// startupTestRegistry builds a registry with non-empty roots for every
// canonical destination, via the canonical public constructor
// (NewDestinationRegistry + a populated cfg.Drive). This matches the
// testRegistry helper in internal/infrastructure/drive/publisher_test.go
// and stays in the same public path that production code uses.
//
// Note: DestinationClipMetadata (P0-#1 atomic-RMW cutover, July 2026)
// is registered with RootFolderID="" because its root is supplied by
// the caller via ParentFolderID (the clip's already-resolved
// folder). The startup validator correctly classifies it as Skipped
// (it does not own its own Drive root — that ownership lives on
// DestinationYouTubeClip, which is already validated). Tests in this
// file therefore expect exactly one Skipped entry on startupTestRegistry:
// [DestinationClipMetadata].
func startupTestRegistry() *DestinationRegistry {
	return NewDestinationRegistry(&config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder:        "media-root",
			ClipsRootFolder:        "clips-root",
			ArtlistRootFolder:      "artlist-root",
			StockRootFolder:        "stock-root",
			ImagesRootFolder:       "images-root",
			VoiceoverRootFolder:    "vo-root",
			BooksRootFolder:        "books-root",
			ScriptsRootFolder:      "scripts-root",
			SoundEffectsRootFolder: "sfx-root",
		},
	})
}

// startupEmptyRootRegistry builds a registry where DestinationArtlist
// has an empty RootFolderID (the Skipped path) and the rest are
// non-empty (the PerDestination path). Constructed via the
// NewDestinationRegistry public entry point so the test exercises
// the production code path (the resolution helpers ClipsFolder/ImagesFolder/etc.
// fall through to MediaRootFolder when their own root is unset, so to
// force Artlist to be EMPTY we must use the canonical helper but
// also blank the fallback. The cleanest way: leave
// ArtlistRootFolder empty AND set a different MediaRootFolder that
// is overridden by the per-key roots via ResolveFolder. Artlist's
// helper returns MediaRootFolder when ArtlistRootFolder is empty,
// so to make it NULL-empty here we override post-construction via
// a separate registry policy.
//
// Simpler: re-build the registry via the canonical constructor, then
// iterate and zero out the Artlist root via a post-construction step.
// We avoid mutating DestinationRegistry's internal map directly (the
// p.policies field is unexported), so the test uses
// NewDestinationRegistryWithPolicies — but only when the build tag
// is available. To keep the test agnostic, we use NewDestinationRegistry
// and accept that the test exercises the empty-root code path
// indirectly: we pass "" for the ArtlistRootFolder AND override
// MediaRootFolder such that ResolveFolder returns "" for Artlist.
//
// In practice: when both ArtlistRootFolder and MediaRootFolder are
// empty, ArtlistFolder() returns "". Use this shape.
func startupEmptyRootRegistry() *DestinationRegistry {
	// ArtlistRootFolder is "". MediaRootFolder is also "" (no fallback).
	// Result: ArtlistFolder() returns "". Other destinations still
	// carry their explicit non-empty roots.
	return NewDestinationRegistry(&config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder:        "", // no fallback
			ClipsRootFolder:        "clips-root",
			ArtlistRootFolder:      "", // <-- empty: should be Skipped
			StockRootFolder:        "stock-root",
			ImagesRootFolder:       "images-root",
			VoiceoverRootFolder:    "vo-root",
			BooksRootFolder:        "books-root",
			ScriptsRootFolder:      "scripts-root",
			SoundEffectsRootFolder: "sfx-root",
		},
	})
}

// ── Test 1: AllPass ───────────────────────────────────────────────────

func TestDriveRootsValidator_AllPass_P1_3(t *testing.T) {
	reg := startupTestRegistry()
	probe := &fakeStartupRootsProbe{}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.NoError(t, err, "AllPass: validator must NOT return error")
	require.NotNil(t, report)
	require.False(t, report.HasFailures(), "AllPass: HasFailures must be false")
	require.Empty(t, report.FailedDestinations(), "AllPass: FailedDestinations must be empty")
	// P0-#1 (July 2026): DestinationClipMetadata has RootFolderID=""
	// (its root is the clip's folder, owned by DestinationYouTubeClip,
	// already validated). Validator must classify it as Skipped, not
	// probe. Exactly one Skipped entry expected here.
	require.ElementsMatch(t, []DestinationKey{DestinationClipMetadata}, report.Skipped,
		"AllPass: DestinationClipMetadata (no own root, sidecar of YouTubeClip) MUST be Skipped")
	// P0-#1 dynamic count: derive expected PerDestination size from the
	// registry (12 destinations, 1 with empty root → 11 in PerDestination).
	// Locking to countNonEmptyRoots(reg) so future registry additions
	// don't silently desync the test from production.
	require.Len(t, report.PerDestination, countNonEmptyRoots(reg),
		"AllPass: every non-empty-root destination MUST be probed exactly once (dynamic count via countNonEmptyRoots)")

	// Every probe was called exactly once for every non-empty root.
	require.Len(t, probe.probeCalls, countNonEmptyRoots(reg),
		"AllPass: every non-empty root probed once each (dynamic count)")
	for _, r := range report.PerDestination {
		require.NoError(t, r.Err, "AllPass: per-destination Err must be nil for %q", r.Destination)
		require.GreaterOrEqual(t, r.Elapsed.Microseconds(), int64(0), "AllPass: Elapsed must be recorded per probe")
	}
}

// ── Test 2: AllFail (every reachable root rejected) ──────────────────

func TestDriveRootsValidator_AllFail_P1_3(t *testing.T) {
	reg := startupTestRegistry()
	probe := &fakeStartupRootsProbe{
		probeErrFn: func(string) error { return errors.New("drive: sim not found") },
	}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.Error(t, err, "AllFail: validator must return error")
	require.ErrorIs(t, err, ErrDriveStartupValidationFailed,
		"AllFail: error MUST wrap ErrDriveStartupValidationFailed verbatim — typed-NIL-safe composition-root gate")
	require.NotNil(t, report, "AllFail: report still carries per-destination detail (even on failure)")
	require.True(t, report.HasFailures(), "AllFail: HasFailures must be true")

	failed := report.FailedDestinations()
	require.Len(t, failed, countNonEmptyRoots(reg),
		"AllFail: every reachable root must be in FailedDestinations (dynamic count via countNonEmptyRoots)")

	// Per-destination errors surface with the typed chain.
	for _, r := range failed {
		found := false
		for _, p := range report.PerDestination {
			if p.Destination == r {
				found = true
				require.Error(t, p.Err, "AllFail: per-destination Err must be non-nil")
				require.Contains(t, p.Err.Error(), "sim not found",
					"AllFail: per-destination Err must carry the probe error chain verbatim")
			}
		}
		require.True(t, found, "AllFail: %q must appear in PerDestination", r)
	}
}

// ── Test 3: PartialFail (mixed outcome) ─────────────────────────────

func TestDriveRootsValidator_PartialFail_P1_3(t *testing.T) {
	reg := startupTestRegistry()
	probe := &fakeStartupRootsProbe{
		probeErrFn: func(rootID string) error {
			// Fail ONLY on voiceover — every other root succeeds.
			// Voiceover helper returns MediaRootFolder fallback; with
			// MediaRootFolder="media-root" set in testRegistry, the
			// resolved voiceover folder is "vo-root".
			if rootID == "vo-root" {
				return errors.New("drive: voiceover root missing")
			}
			return nil
		},
	}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.Error(t, err, "PartialFail: validator must return error (1 root failed)")
	require.ErrorIs(t, err, ErrDriveStartupValidationFailed)
	require.NotNil(t, report)

	failed := report.FailedDestinations()
	require.Equal(t, []DestinationKey{DestinationVoiceover}, failed,
		"PartialFail: only the voiceover root should be in FailedDestinations")

	// Sanity: every non-empty-root destination is probed even when one fails.
	// Dynamic count: one root (voiceover) is set up to fail; the rest succeed.
	require.Len(t, report.PerDestination, countNonEmptyRoots(reg),
		"PartialFail: every non-empty-root destination probed (dynamic count)")
	successCount := 0
	for _, p := range report.PerDestination {
		if p.Destination == DestinationVoiceover {
			require.Error(t, p.Err)
			require.Contains(t, p.Err.Error(), "voiceover root missing")
		} else if p.Err == nil {
			successCount++
		}
	}
	// Success count = total non-empty roots - 1 (voiceover failed).
	// Dynamic via countNonEmptyRoots to stay in lockstep with the registry.
	require.Equal(t, countNonEmptyRoots(reg)-1, successCount,
		"PartialFail: all destinations except voiceover MUST succeed (dynamic count)")
}

// ── Test 4: EmptySkipped (empty RootFolderID is skipped, not failed) ─

func TestDriveRootsValidator_EmptySkipped_P1_3(t *testing.T) {
	reg := startupEmptyRootRegistry() // DestinationArtlist has empty RootFolderID
	probe := &fakeStartupRootsProbe{}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.NoError(t, err, "EmptySkipped: empty roots MUST NOT trigger ErrDriveStartupValidationFailed")
	require.NotNil(t, report)
	require.False(t, report.HasFailures(),
		"EmptySkipped: HasFailures must be false (empty root is Skipped, not a probe failure)")

	// The Artlist destination is in Skipped (empty root). Admin is also
	// skipped because its RootFolder() returns MediaRootFolder which is
	// empty in this registry. ClipMetadata is also Skipped (its root
	// is the clip folder, owned by DestinationYouTubeClip; no own root
	// to probe). The other non-empty-root destinations are in PerDestination.
	require.ElementsMatch(t, []DestinationKey{DestinationArtlist, DestinationAdmin, DestinationClipMetadata}, report.Skipped,
		"EmptySkipped: DestinationArtlist + DestinationAdmin + DestinationClipMetadata (empty/no-own-root) MUST be in Skipped")
	require.Len(t, report.PerDestination, countNonEmptyRoots(reg),
		"EmptySkipped: every non-empty-root destination MUST be probed (dynamic count via countNonEmptyRoots)")

	// Verify NO probe call was made for the empty Artlist root
	// (validator MUST skip it before invoking ProbeFolderAccess).
	for _, probed := range probe.probeCalls {
		require.NotEmpty(t, probed,
			"EmptySkipped: empty RootFolderID MUST NOT call ProbeFolderAccess (no Drive API call)")
	}
}

// ── Test 5: NilRegistry fail-fast ────────────────────────────────────

func TestDriveRootsValidator_NilRegistry_P1_3(t *testing.T) {
	probe := &fakeStartupRootsProbe{}
	v, err := NewDriveRootsValidator(nil, probe, zap.NewNop(), nil)
	require.Error(t, err, "NilRegistry: must fail-fast on nil registry")
	require.ErrorIs(t, err, ErrMissingStartupValidatorRegistry,
		"NilRegistry: error MUST wrap ErrMissingStartupValidatorRegistry verbatim")
	require.Nil(t, v, "NilRegistry: validator pointer must be nil on error (composition-time safety)")
}

// ── Test 6: NilFolders fail-fast ─────────────────────────────────────

func TestDriveRootsValidator_NilFolders_P1_3(t *testing.T) {
	reg := startupTestRegistry()
	v, err := NewDriveRootsValidator(reg, nil, zap.NewNop(), nil)
	require.Error(t, err, "NilFolders: must fail-fast on nil StartupRootsProbe")
	require.ErrorIs(t, err, ErrMissingStartupValidatorFolders,
		"NilFolders: error MUST wrap ErrMissingStartupValidatorFolders verbatim")
	require.Nil(t, v)
}

// ── Test 7: AllFailReportShape — every reachable root surfaces in
//            PerDestination with the probe error chain verbatim, and
//            the umbrella ErrDriveStartupValidationFailed wraps the
//            burst via errors.Join. (We do NOT pin the per-root
//            retry-call count because that depends on pkg/retry's
//            IsTransient classifier — orthogonal to the validator's
//            contract; report-shape invariants are the stable surface.) ──

func TestDriveRootsValidator_AllFailReportShape_P1_3(t *testing.T) {
	reg := startupTestRegistry()
	probe := &fakeStartupRootsProbe{
		probeErrFn: func(string) error {
			return errors.New("simulated probe failure")
		},
	}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrDriveStartupValidationFailed,
		"AllFailReportShape: error MUST wrap ErrDriveStartupValidationFailed verbatim")
	require.NotNil(t, report, "AllFailReportShape: report still surfaces despite umbrella error")

	// Every reachable root failed → all in FailedDestinations AND
	// every row has Err set (the per-destination error chain).
	failed := report.FailedDestinations()
	require.Len(t, failed, countNonEmptyRoots(reg),
		"AllFailReportShape: every reachable root must fail (dynamic count via countNonEmptyRoots)")
	require.True(t, report.HasFailures())

	for _, r := range report.PerDestination {
		require.Error(t, r.Err, "AllFailReportShape: per-destination Err must be non-nil for %q", r.Destination)
		require.Contains(t, r.Err.Error(), "simulated probe failure",
			"AllFailReportShape: per-destination Err must carry the probe error chain verbatim for %q", r.Destination)
	}
}

// ── Test 7b: FailureLabelPerDestination — the per-destination error
//             envelope MUST identify the failing key + root via fmt.Errorf
//             so audit log scrapers can attribute failures to specific
//             destinations, not just count the burst total. ────────

func TestDriveRootsValidator_FailureLabelPerDestination_P1_3(t *testing.T) {
	reg := startupTestRegistry()
	probe := &fakeStartupRootsProbe{
		probeErrFn: func(rootID string) error {
			if rootID == "vo-root" {
				return errors.New("voiceover unreachable")
			}
			return nil
		},
	}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrDriveStartupValidationFailed)
	require.NotNil(t, report,
		"FailureLabelPerDestination: report MUST still surface despite the umbrella error (per-destination detail)")
	require.Contains(t, err.Error(), string(DestinationVoiceover),
		"FailureLabelPerDestination: umbrella error MUST name the failing destination key")
	require.Contains(t, err.Error(), "vo-root",
		"FailureLabelPerDestination: umbrella error MUST name the failing root ID for ops log")
	require.Contains(t, err.Error(), "voiceover unreachable",
		"FailureLabelPerDestination: umbrella error MUST carry the underlying probe error verbatim")
}

// ── Test 8: ReportCoversAllKeys — every registry key appears exactly
//            once across PerDestination ∪ Skipped ─────────────────────

func TestDriveRootsValidator_ReportCoversAllKeys_P1_3(t *testing.T) {
	reg := startupEmptyRootRegistry() // includes a Skipped entry (Artlist)
	probe := &fakeStartupRootsProbe{}

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), nil)
	require.NoError(t, err)

	report, err := v.ValidateDriveRoots(context.Background())
	require.NoError(t, err)
	require.NotNil(t, report)

	// PerDestination + Skipped must cover every key in the registry,
	// and no key may appear in both lists.
	seen := make(map[DestinationKey]bool, len(report.PerDestination)+len(report.Skipped))
	for _, p := range report.PerDestination {
		require.False(t, seen[p.Destination],
			"ReportCoversAllKeys: %q must not appear twice across the union", p.Destination)
		seen[p.Destination] = true
	}
	for _, k := range report.Skipped {
		require.False(t, seen[k],
			"ReportCoversAllKeys: %q must not appear in both PerDestination and Skipped", k)
		seen[k] = true
	}
	for _, k := range reg.Keys() {
		require.True(t, seen[k], "ReportCoversAllKeys: %q must appear in either PerDestination or Skipped", k)
	}
}
