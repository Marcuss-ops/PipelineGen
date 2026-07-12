// Package drive — publisher_pathbuilder_test.go: PR-VO-ERR-PATHBUILDER
// sentinel + PR-VO-SUBFOLDER 3-branch resolveDestination tests + DoD 9
// PathBuilder/YouTubeClipPath tests. Step 6 split (July 2026):
// consolidated from the previous (publisher_test_resolve_destination_test.go
// + 4 tests moved from publisher_test_dod_language_test.go).
//
// godlike/06 SSOT: VoiceoverPath (PR-VO-SUBFOLDER subpath structure)
// + YouTubeClipPath (canonical clips/{group}/{subject} structure)
// live in `internal/application/assets/delivery/registry.go`. The
// publisher's resolveDestination handler mirrors the canonical
// issuer ↔ verifier gate through the typed-error surface
// (ErrPathBuilderIncompleteForOverride sentinel + dual-%w fmt.Errorf
// wrap per godlike/07 typed-error contract).
//
// 11 tests under this header cover:
//   4 PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE tests (typed sentinel +
//     resolveDestination dual-return shape + Publish swallow + success
//     path nil-err pair)
//   3 PR-VO-SUBFOLDER tests (Voiceover subpath under override +
//     PathBuilderFailOverride FallsBack + PathBuilderFailsNoOverride
//     ReturnsError)
//   4 DoD 9 PathBuilder tests (CategoryBoxe + IsDeterministicAcrossRetries +
//     SanitisesSpecialCharacters + WithRootFolderOverride UsesSingleLeaf)
//
// These tests FAIL on regression only if:
//   - ErrPathBuilderIncompleteForOverride sentinel is dropped or wrapped
//     via errors.Join-only (loses single-line stderr benefit per
//     godlike/07 single-line-stderr criterion)
//   - dual-%w fmt.Errorf wrap is replaced by %v+%w (loses typed recovery)
//   - VoiceoverPath structure collapses at override (silent file overwrites)
//   - PathBuilder falls through silently when caller omits Group/Subject
//   - YouTubeClipPath loses its deterministic / SafeFolderName contract
//   - RootFolderOverride subpath structure is bypassed
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// ── PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE tests (July 2026) ──────────
//
// PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE replaces the original log.Warn +
// silent-swallow in Publisher.resolveDestination Step 4 fall-through with a
// typed sentinel that callers can errors.Is. The sentinel is the canonical
// diagnostic for "PathBuilder failed + the caller supplied RootFolderOverride,
// so we fell back to direct-to-root upload" — useful for ops dashboards,
// smoke alerts, and aggressive-mode callers that want to fail-closed at the
// fallback (forward-pointer PR-VO-AGGREGATE-SUBPATH-CASCADE).
//
// godlike/07 typed-error contract: the sentinel is a top-level
// `var X = errors.New(...)` declared in errors.go for clean errors.Is probes.
// The wrap in resolveDestination uses dual-%w fmt.Errorf (Go 1.20+) to
// preserve BOTH the typed sentinel (errors.Is) AND the underlying
// PathBuilder cause (errors.As) — unlike the pre-PR fmt.Errorf "%%v+%%w"
// which stringified the cause and lost typed recovery. errors.Join is
// equally valid for typed-chain preservation but introduces newline-
// separated stderr noise that breaks single-line log aggregators; the
// dual-%w fmt.Errorf idiom is the canonical wrap for this surface.

// TestErrPathBuilderIncompleteForOverride_Sentinel pins the typed error
// declaration + dual-%w fmt.Errorf wrap contract.
//
// godlike/07 typed-error contract verification: rather than using
// errors.As(err, &recoveredCause) with a `*error` target (rejected by
// `go vet` because the second argument must be a concrete type, not
// the bare error interface), we verify typed-recovery via errors.Is
// against the SAME underlying-cause pointer-identity — errors.Is walks
// via Unwrap() and matches by == or Is(), achieving the same chain-
// preservation guarantee as errors.As without go vet flakiness. This
// also avoids walk-order dependency on the fmt.Errorf wrapErrors slice
// order (the first-match of `*error` was previously implementation-
// detail-dependent).
func TestErrPathBuilderIncompleteForOverride_Sentinel(t *testing.T) {
	var _ error = ErrPathBuilderIncompleteForOverride // compile-time pin

	// (a) Bare sentinel: errors.Is matches the error itself.
	require.ErrorIs(t, ErrPathBuilderIncompleteForOverride, ErrPathBuilderIncompleteForOverride,
		"bare sentinel MUST errors.Is match itself")

	// (b1) Production dual-%w fmt.Errorf wrap (canonical in resolveDestination).
	//      underlyingCause is held for the errors.Is identity probe at (c).
	underlyingCause := fmt.Errorf("group is required")
	wrapped := fmt.Errorf("delivery: PathBuilder failed under RootFolderOverride (cause: %w): %w",
		underlyingCause, ErrPathBuilderIncompleteForOverride)

	// (b2) errors.Is recovers the sentinel via wrap-chain walk.
	require.ErrorIs(t, wrapped, ErrPathBuilderIncompleteForOverride,
		"dual-%w fmt.Errorf must preserve the sentinel for errors.Is (typed-chain preservation contract)")

	// (c) Typed-recovery: errors.Is recovers the underlying cause via
	//     pointer-identity match against the SAME underlyingCause variable
	//     (== equality through the wrap chain). This is equivalent to
	//     errors.As(wrapped, &concreteErrorType) for purposes of the
	//     godlike/07 typed-recovery contract, without the *error target
	//     go vet rejection.
	require.ErrorIs(t, wrapped, underlyingCause,
		"dual-%w fmt.Errorf must preserve the underlying PathBuilder cause via errors.Is (typed-recovery contract — equivalent to errors.As for chain-preservation)")

	// (d) Underlying cause's message preserved in err.Error() for grep-ability.
	require.Contains(t, wrapped.Error(), "group is required",
		"dual-%w fmt.Errorf must preserve the underlying cause's message via the wrap chain (log/diagnostic surface)")

	// (e) Sentinel discriminator phrase preserved (stable against message rewording).
	require.Contains(t, wrapped.Error(), "RootFolderOverride",
		"dual-%w fmt.Errorf must preserve the sentinel's discriminator phrase (RootFolderOverride) for grep-able diagnostic surface")

	// (f) DOWNSTREAM-COMPAT ALIAS ONLY — errors.Join is equally valid for
	//     downstream consumers (godlike/06 SSOT does NOT forbid it; production
	//     uses dual-%w fmt.Errorf for the single-line-stderr benefit). The
	//     alias is documented here to prevent future agents from switching
	//     publisher.go to errors.Join and regressing to the 3-line stderr
	//     noise that breaks single-line log aggregators. DO NOT change
	//     publisher.go to use errors.Join — the dual-%w fmt.Errorf wrap is
	//     the canonical production idiom per godlike/07 single-line stderr
	//     criterion.
	joinedCause := fmt.Errorf("group is required")
	joined := errors.Join(joinedCause, ErrPathBuilderIncompleteForOverride)
	require.ErrorIs(t, joined, ErrPathBuilderIncompleteForOverride,
		"errors.Join downstream-compat: sentinel preserved for errors.Is")
	require.ErrorIs(t, joined, joinedCause,
		"errors.Join downstream-compat: underlying cause preserved for errors.Is (typed-recovery alias)")

	// (g) Negative control: unrelated sentinel does NOT match.
	otherSentinel := errors.New("drive: unrelated sentinel")
	require.NotErrorIs(t, wrapped, otherSentinel,
		"dual-%w wrapped sentinel MUST NOT match unrelated sentinels (errors.Is isolation)")
}

// TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel
// pins the canonical dual-return shape contract: when PathBuilder fails AND
// the caller supplied RootFolderOverride, resolveDestination returns BOTH a
// non-nil ResolvedDriveDestination struct (with direct-to-root fallback) AND
// the typed sentinel wrapped error. The dual-return is the load-bearing
// invariant that enables errors.Is probes at call sites without losing
// the resolved.FolderID needed for the upload. White-box test in same package.
func TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	resolved, err := pub.resolveDestination(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		RootFolderOverride: override,
		ConflictPolicy:     delivery.ConflictOverwrite,
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject omitted → YouTubeClipPath returns "subject (video ID) is required".
	})

	// (1) Dual-return shape assertions.
	require.NotNil(t, resolved,
		"resolveDestination must return a non-nil ResolvedDriveDestination even on typed-error path (dual-return shape contract)")
	require.Equal(t, override, resolved.RootFolderID,
		"resolved.RootFolderID must be the explicit override (root-folder fallback)")
	require.Empty(t, resolved.PathSegments,
		"resolved.PathSegments must be empty on the typed-error path (direct-to-root fallback)")
	require.Equal(t, override, resolved.FolderID,
		"resolved.FolderID must equal the explicit override (no nested folder created on typed-error path)")

	// (2) Typed-error contract: errors.Is the canonical sentinel.
	require.Error(t, err, "resolveDestination must surface a non-nil error on the typed-error path")
	require.ErrorIs(t, err, ErrPathBuilderIncompleteForOverride,
		"the returned error MUST wrap ErrPathBuilderIncompleteForOverride (errors.Is gateway for call-site decision)")

	// (3) Typed-chain preservation + grep-able diagnostic surface.
	//     The dual-%w fmt.Errorf wrap is verified by:
	//       (3a) err.Error() contains "group is required" (the underlying
	//            cause's message survives the wrap via the %w chain)
	//       (3b) errors.Is(err, ErrPathBuilderIncompleteForOverride)
	//            (the sentinel is recoverable via the wrap chain).
	//     We intentionally do NOT use errors.As(&recoveredCause) here
	//     because (a) the underlying cause is a private fmt.Errorf
	//     returned by policy.PathBuilder and is not typeable from
	//     outside, (b) `go vet` rejects `errors.As(err, &error)` as the
	//     target must be a concrete type, (c) the (3a) + (3b) checks
	//     together verify the chain-preservation contract without
	//     walk-order flakiness.
	require.Contains(t, err.Error(), "subject (video ID) is required",
		"dual-%w fmt.Errorf must preserve the underlying PathBuilder cause 'subject (video ID) is required' (typed-chain diagnostic via message-preservation — Group now falls back to youtube_uncategorized, Subject is first to fail)")
	require.ErrorIs(t, err, ErrPathBuilderIncompleteForOverride,
		"dual-%w fmt.Errorf must preserve ErrPathBuilderIncompleteForOverride for errors.Is at the resolveDestination call site (call-site decision gateway)")
}

// TestResolveDestination_SuccessPath_ReturnsNilErr pins the success-path
// contract for resolveDestination: when PathBuilder succeeds + override set
// + segments non-empty, the function returns (resolved, nil). This is the
// PAIR to TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel
// (which pins the fallback-path err=non-nil contract). Together the two
// tests lock the dual-return shape's err variable across both branches.
//
// godlike/07 typed-error regression-prevention: pre-PR-VO-ERR-PATHBUILDER-
// INCOMPLETE-OVERRIDE the finalize `return ..., nil` was silently zeroing
// out err even when the wrap was set. The bug was latent because the
// pre-PR log.Warn + err=nil discipline coincidentally matched. The
// fallback-path test surfaced the bug because it asserts err != nil;
// this success-path test prevents future regressions where someone might
// re-introduce `return ..., nil` (under the false assumption that the
// explicit nil is 'safer').
func TestResolveDestination_SuccessPath_ReturnsNilErr(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "leaf-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	resolved, err := pub.resolveDestination(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		Group:              "test",
		Subject:            "abc",
		RootFolderOverride: "explicit-override-folder-id",
		ConflictPolicy:     delivery.ConflictOverwrite,
	})

	// Success path: PathBuilder succeeded → segments built → EnsureFolder
	// called → leaf folder returned. err MUST be nil on the success path
	// (the dual-return shape demands err=nil when nothing failed).
	require.NoError(t, err, "resolveDestination MUST return nil err on success path (paired with fallback-path test for non-nil err)")

	// Resolved struct carries the leaf folder id (returned by fakeFolderManager).
	require.NotNil(t, resolved)
	require.Equal(t, "leaf-folder-id", resolved.FolderID,
		"success path: FolderID must equal the leaf folder returned by EnsureFolder")
	require.NotEmpty(t, resolved.PathSegments,
		"success path: PathSegments must be non-empty when PathBuilder succeeds")
	require.Equal(t, []string{"abc"}, resolved.PathSegments,
		"success path: PathSegments must collapse to a single leaf under RootFolderOverride")
	require.Equal(t, "explicit-override-folder-id", resolved.RootFolderID,
		"success path: RootFolderID must be the explicit override (RootFolderOverride precedence)")
}

// TestResolveDestination_PathBuilderFailOverride_UsesOverrideRoot pins the
// end-to-end Publish swallow behavior: when resolveDestination returns typed
// sentinel + resolved struct, Publish errors.Is + log.Debug + uses override root.
func TestResolveDestination_PathBuilderFailOverride_UsesOverrideRoot(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		RootFolderOverride: override,
		ConflictPolicy:     delivery.ConflictOverwrite,
	})
	require.NoError(t, err,
		"Publish MUST swallow ErrPathBuilderIncompleteForOverride at the call-site (backward-compat per godlike/07 minimum-blast-radius)")

	// Upload landed in the override root via the resolved struct (proves dual-return shape contract).
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, override, files.uploadCalls[0].folderID,
		"Publish MUST use the override root from the resolved struct (dual-return shape contract)")
}

// ── PR-VO-SUBFOLDER tests (July 2026, commit c96eb1e0) ───────────────────
//
// PR-VO-SUBFOLDER fixes the invariant that callers with an explicit
// RootFolderOverride still benefit from the canonical PathBuilder
// structure (e.g. voiceover: voiceovers/{project}/{language}). The
// three tests below pin the three PathBuilder branches of resolveDestination:
//
//   1. PathBuilder succeeds + override set → subpath built under override.
//   2. PathBuilder fails + override set    → direct-to-root fallback (warn).
//   3. PathBuilder fails + no override     → error propagates to caller.
//
// They lock the contract that future refactors of PathBuilder or the
// registry-driven Resolve cannot silently break the PR-VO-SUBFOLDER
// contract — any drift surfaces here first.

// TestResolveDestination_VoiceoverWithRootFolderOverride_BuildsSubpath
// pins the canonical voiceover subpath structure under an explicit
// RootFolderOverride. Pre-PR-VO-SUBFOLDER the PathBuilder was
// short-circuited away when RootFolderOverride was non-empty, so the
// canonical voiceovers/{project}/{language} subtree was being
// SKIPPED and the MP3 landed directly in the override root. After
// the fix, PathBuilder runs first, segments under the override, and
// EnsureFolder is called with the canonical 2-segment structure.
func TestResolveDestination_VoiceoverWithRootFolderOverride_BuildsSubpath(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "voiceover-sub-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	// Caller supplies an explicit Drive folder ID (e.g. via
	// cmd/admin resolve flow OR voiceover handler's prior
	// GetOrCreateFolder call). Project + language drive the
	// canonical {project}/{language} subfolder.
	override := "explicit-voiceover-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationVoiceover,
		LocalPath:          "/tmp/storia-boxe-it.mp3",
		Filename:           "storia-boxe-it.mp3",
		ProjectID:          "storia-boxe-it",
		Language:           "it-IT",
		RootFolderOverride: override,
		// ConflictPolicy omitted → registry-driven default applies
		// (Voiceover is ConflictSkip per P1.1 mapping).
	})
	require.NoError(t, err)

	// (1) EnsureFolder MUST be called with the EXPLICIT override as the
	//     parent (NOT the registry vo-root "vo-root") — the PR-VO-SUBFOLDER
	//     invariant: the override wins for the parent, the PathBuilder
	//     still owns the canonical subpath segments.
	require.Len(t, folders.ensureCalls, 1,
		"voiceover with RootFolderOverride MUST trigger exactly one EnsureFolder call (PR-VO-SUBFOLDER invariant)")
	require.Equal(t, override, folders.ensureCalls[0].parent,
		"EnsureFolder MUST be called with the explicit RootFolderOverride as parent — NOT the registry vo-root")
	require.Equal(t, []string{"storia-boxe-it", "it-IT"}, folders.ensureCalls[0].segments,
		"EnsureFolder MUST be called with the canonical voiceover subpath [{project},{language}] — SafeFolderName preserves alphanum + hyphen")

	// (2) Upload landed in the sub-folder returned by EnsureFolder (NOT
	//     the override root directly).
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "voiceover-sub-folder-id", files.uploadCalls[0].folderID,
		"Upload MUST land in the canonical sub-folder returned by EnsureFolder")
}

// TestResolveDestination_PathBuilderFailsWithOverride_FallsBack pins
// the backward-compat fallback path: PathBuilder fails (missing
// metadata) AND the caller supplied RootFolderOverride. The fix
// logs a Warn and UPLOADS DIRECTLY into the override root (no
// EnsureFolder call). This is the production-PR-VO-SUBFOLDER contract
// — voiceover handlers that hit the legacy Group="" path still
// work without surfacing a typed error.
//
// Pre-PR-VO-SUBFOLDER this branch surface did not exist at all:
// PathBuilder was unconditionally skipped when override was set, so
// the SAME failure mode (no Group, no Subject, with override) would
// upload into the override root WITHOUT the warn signal, masking
// upstream metadata failures. The warn + typed continue-path is the
// load-bearing visibility surface.
func TestResolveDestination_PathBuilderFailsWithOverride_FallsBack(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}

	// PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE (July 2026): the
	// fallback diagnostic moved from Warn to Debug (the explicit
	// errors.Is sentinel at the call-site surface is now the
	// canonical diagnostic; the Debug log is the explicit call-site
	// ack that the swallow took place, NOT the primary failure
	// surface). The observer uses DebugLevel to capture the ack.
	core, recorded := observer.New(zapcore.DebugLevel)
	log := zap.New(core)

	pub, err := NewPublisher(reg, folders, files, log)
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		RootFolderOverride: override,
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject omitted → YouTubeClipPath returns "subject (video ID) is required".
		// ConflictPolicy explicit Overwrite so the registry's
		// ConflictSkip default is bypassed (test focuses on the
		// PathBuilder branch, not the policy branch).
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err,
		"PathBuilder failure with explicit override MUST NOT propagate — backward-compat fallback to direct-to-root")

	// (1) EnsureFolder MUST NOT be called (no segments → direct upload).
	require.Empty(t, folders.ensureCalls,
		"EnsureFolder MUST NOT be called when PathBuilder fails + RootFolderOverride is set (direct-to-root fallback)")

	// (2) Upload landed directly in the override root.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, override, files.uploadCalls[0].folderID,
		"Upload MUST land in the explicit override root (direct-to-root fallback, no subfolder created)")

	// (3) Debug-level explicit-ack diagnostic surfaced the fallback so
	//     the operator sees the metadata gap in logs (NOT silent). The
	//     message text is the canonical 'incomplete subpath tolerated'
	//     ack that the call-site uses after errors.Is'ing
	//     ErrPathBuilderIncompleteForOverride.
	require.NotEmpty(t, recorded.All(), "expected at least one log entry on PathBuilder failure with override — got none")
	debugFound := false
	for _, entry := range recorded.All() {
		if strings.Contains(entry.Message, "incomplete subpath tolerated because override was set") {
			debugFound = true
			break
		}
	}
	require.True(t, debugFound,
		"expected Debug log with 'incomplete subpath tolerated because override was set' — got %v (PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE call-site ack contract)",
		recorded.All())
}

// TestResolveDestination_PathBuilderFailsNoOverride_ReturnsError
// pins the AUTHORITATIVE error path: PathBuilder fails AND the caller
// did NOT supply RootFolderOverride. In this branch the PathBuilder
// failure is the canonical signal — the publisher MUST propagate it
// so the caller can fix the metadata (Group, Subject, ProjectID,
// Language, etc.). Silently degrading here would let typos in the
// metadata inputs slip through to a phantom upload into the registry's
// root folder — which is exactly the failure mode the fix's
// RequireSubpath gating is meant to prevent.
func TestResolveDestination_PathBuilderFailsNoOverride_ReturnsError(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip.mp4",
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject omitted → YouTubeClipPath returns "subject (video ID) is required".
		// RootFolderOverride omitted (zero value).
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.Error(t, err,
		"PathBuilder failure with NO override MUST propagate to caller — the registry default would otherwise hide a metadata gap")
	require.Contains(t, err.Error(), "subject",
		"Error must surface PathBuilder's underlying 'subject (video ID) is required' — wrapping preserved so callers can errors.Is/As on it")
	require.Contains(t, err.Error(), "build path",
		"Error must include the publisher's 'delivery: build path for %q: %w' prefix so the canonical seam is grep-able in logs")
}

// ── FASE D: DoD 9 tests (July 2026) — YouTube→Publisher PathBuilder & category ──
//
// DoD 9 validates the YouTube clip publishing flow with semantic metadata:
//   - Category=Boxe paired with Subject=Pacquiao vs Broner (real-world names)
//   - PathBuilder segment sanitisation (SafeFolderName preserves spaces/hyphens)
//   - No-FolderID scenario (pure semantic routing — no RootFolderOverride)
//
// godlike/06 SSOT: YouTubeClipPath is the SOLE canonical owner of the
// clips/{group}/{subject} path structure. Category on PublishRequest is
// carried for Qdrant payload enrichment; the PathBuilder consumes Group
// and Subject only.

// TestYouTubeClipPath_CategoryBoxeSubjectPacquiaoVsBroner_DOD_9_1 pins
// DoD 9 item 1: YouTubeClipPath with the canonical Boxe category.
func TestYouTubeClipPath_CategoryBoxeSubjectPacquiaoVsBroner_DOD_9_1(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err, "YouTubeClipPath must succeed with Group=Boxe and Subject='Pacquiao vs Broner'")

	// SafeFolderName preserves alphanum, spaces, and hyphens verbatim —
	// "Boxe" and "Pacquiao vs Broner" pass through unchanged.
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, segs,
		"YouTubeClipPath must return canonical [{group},{subject}] segments — SafeFolderName preserves spaces and hyphens")
}

// TestYouTubeClipPath_IsDeterministicAcrossRetries_DOD_9_2 pins DoD 9 item 2:
// YouTubeClipPath is idempotent across N retries with the same inputs.
func TestYouTubeClipPath_IsDeterministicAcrossRetries_DOD_9_2(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	first, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		retry, err := delivery.YouTubeClipPath(req)
		require.NoError(t, err, "retry %d must not fail", i)
		require.Equal(t, first, retry,
			"retry %d: YouTubeClipPath must be byte-stable idempotent — same inputs must produce same segments", i)
	}
}

// TestYouTubeClipPath_PathBuilderSanitisesSpecialCharacters_DOD_9_3 pins
// DoD 9 item 3: PathBuilder sanitises special characters via SafeFolderName.
func TestYouTubeClipPath_PathBuilderSanitisesSpecialCharacters_DOD_9_3(t *testing.T) {
	// Subject with a YouTube-style video ID containing special chars
	// that SafeFolderName would replace (slashes → spaces/hyphens).
	req := delivery.PublishRequest{
		Group:   "NBA / Highlights",
		Subject: "game:7/OT",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	// SafeFolderName replaces / and : with safe alternatives (spaces/hyphens).
	require.Equal(t, 2, len(segs), "must produce exactly 2 segments [{group},{subject}]")
	// Positive assertions: SafeFolderName replaces non-alphanum (except -/_) with _.
	require.Equal(t, "NBA _ Highlights", segs[0],
		"SafeFolderName must replace / with _ in group segment")
	require.Equal(t, "game_7_OT", segs[1],
		"SafeFolderName must replace / with _ and : with _ in subject segment")
}

// TestYouTubeClipPath_WithRootFolderOverride_UsesSingleLeaf pins the
// override-aware clip path contract used by payload-selected roots:
// when RootFolderOverride is set, the path builder must emit a single
// leaf folder instead of the legacy youtube_uncategorized/group layer.
func TestYouTubeClipPath_WithRootFolderOverride_UsesSingleLeaf(t *testing.T) {
	req := delivery.PublishRequest{
		RootFolderOverride: "explicit-root",
		Subject:            "qQIsvIOQS8U",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"qQIsvIOQS8U"}, segs)

	req = delivery.PublishRequest{
		RootFolderOverride: "explicit-root",
		Group:              "boxing-channels",
	}
	segs, err = delivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"boxing-channels"}, segs)
}
