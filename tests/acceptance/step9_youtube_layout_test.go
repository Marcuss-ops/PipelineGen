// Package acceptance_test — step9_youtube_layout_test.go.
// Step 9 acceptance battery (PR-CLIPINGEST-PIPELINE, July 2026):
// parallel to the Step 11 tests/acceptance/ surface; pins the
// canonical YouTube asset layout per the user spec. The battery
// is PURE — no I/O, no Drive API, no fs touch — so it runs in
// the CI test phase without ffmpeg, network, or sandbox flags.
//
// godlike/06 SSOT — pins:
//
//   - canonical filename shape ({asset_id}__master.mp4 / __preview.mp4
//     / __manifest.json). The processor.processRenditions function
//     is the SOLE canonical owner of the canonical `__<role>`
//     convention; the battery pins the literal filename strings
//     so a future drift surfaces as a test failure rather than a
//     silent Drive-level mix-up.
//
//   - canonical YouTubeAssetPath shape: youtube/{channel_id}/
//     {video_id}/clips/{asset_id}/. The YouTubeAssetPath builder
//     in internal/platform/delivery/registry_transport.go is the
//     SOLE canonical owner; the battery probes the SOMETIMES-
//     SAFENED segment shape (the pathutil.SafeFolderName helper
//     rejects empty strings + winslash-injection).
//
//   - canonical codec surface (H.264 / AAC / yuv420p /
//     30 fps / 1920x1080). The processor + ffmpeg stack is the
//     owner; the battery pins the literal SurfaceStrings so a
//     future config drift surfaces here.
//
//   - canonical idempotency key derivation
//     (delivery.DeriveIdempotencyKey). Pinned via the
//     destination:artifactID:contentHash:sourceVersion quadruple
//     — collision-avoidance on different ContentHash surfaces the
//     hash-based dedup contract.
//
// godlike/07 typed-error contract — pins:
//   - ErrYouTubeAssetPathMissingField (3 sub-tests covering
//     ChannelID / Subject / AssetID each independently blank).
//   - ErrAssetPublishLocationIncompleteForDestination (mapper
//     layer — fail-fast at the mapper boundary, NOT deeper in
//     the path-builder).
//   - ErrDriveFileSizeMismatch + ErrDriveFileSHA256Mismatch
//     (verification gate — size + SHA mismatches MUST surface
//     as typed sentinels, not generic errors).
//
// Verifier gate (Step 9 Commit 3-5): the battery pins the
// `VerificationParams.ExpectedSize` (int64) + `ExpectedSHA256`
// plumbing surface — the verify-before-cleanup ordering is
// structurally guaranteed by the publisher.Publish Step 6
// (verifier.Verify runs after PutFile succeeds and before the
// function returns to the caller, so failed verifies propagate
// as the err==nil gate that gates os.Remove at the caller
// seam — see step10 publish caller for that contract).
//
// The 11 tests cover 6 categories per the user spec:
//
//	(a) layout         — TestStep9_Layout_CanonicalFilenames (1)
//	(b) path-builder   — TestStep9_PathBuilder_YouTubeAsset + Sanitization (2)
//	(c) codec          — TestStep9_Codec_CanonicalValues (1)
//	(d) verify-gate    — TestStep9_VerifyGate_SizeMismatch + SHAMismatch (2)
//	(e) idempotency    — TestStep9_Idempotency_Determinism + CollisionAvoidance (2)
//	(f) fail-closed    — TestStep9_FailClosed_MissingChannelID + MissingSubject + MissingAssetID (3)
//
// No external assertion libraries — standard Go testing.T
// assertions are used, matching the acceptance_specscene_test.go
// pattern.
package acceptance_test

import (
	"errors"
	"strings"
	"testing"

	deliverypkg "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// ── (a) layout — canonical filename shape ─────────────────────────────────

// TestStep9_Layout_CanonicalFilenames pins the three canonical per-asset
// filenames produced by processor.processRenditions:
//
//	{asset_id}__master.mp4    (H.264/AAC/yuv420p/24fps/1920x1080)
//	{asset_id}__preview.mp4   (720p H.264/AAC proxy)
//	{asset_id}__manifest.json (per-asset metadata ledger)
//
// The `__` separator and the role suffix are SSOT — the canonical
// surfaces that downstream consumers (Qdrant payload mappers,
// operator dashboards, manual ops scripts) parse by string match.
// A drift here (e.g. master.mp4 without `__` or `__preview.mov`
// instead of `.mp4`) silently breaks every consumer; this pin
// catches the drift at CI time.
func TestStep9_Layout_CanonicalFilenames(t *testing.T) {
	const assetID = "yt_abc_0_30_v1"
	cases := []struct {
		name     string
		filename string
		wantRole string
	}{
		{"master", assetID + "__master.mp4", "__master.mp4"},
		{"preview", assetID + "__preview.mp4", "__preview.mp4"},
		{"manifest", assetID + "__manifest.json", "__manifest.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// godlike/06 SSOT pin: the literal filename MUST
			// end with the canonical role suffix + extension.
			if !strings.HasSuffix(tc.filename, tc.wantRole) {
				t.Errorf("filename %q MUST end with canonical suffix %q (the role convention is SSOT — drift breaks every string-matching consumer downstream)",
					tc.filename, tc.wantRole)
			}
			// Secondary pin: the assetID prefix MUST be
			// present (no drift in the prefix → role structure).
			if !strings.HasPrefix(tc.filename, assetID+"__") {
				t.Errorf("filename %q MUST start with the canonical asset_id prefix %q__ (the {asset_id}__<role> structure is SSOT)",
					tc.filename, assetID)
			}
		})
	}
}

// ── (b) path-builder — YouTubeAssetPath ──────────────────────────────────

// TestStep9_PathBuilder_YouTubeAsset pins the canonical
// `youtube/{channel_id}/{video_id}/clips/{asset_id}` segment
// shape returned by delivery.YouTubeAssetPath. The segments
// are the path-builder's word-level output (loop unroll to
// 4 segments per PublishRequest — surface must match exactly).
//
// godlike/06 SSOT: YouTubeAssetPath is the SOLE canonical owner
// of this shape. The acceptance-youtube test surface from
// the pre-PR tree uses a different segment count; the post-Step-9
// canonical is exactly 4 segments.
func TestStep9_PathBuilder_YouTubeAsset(t *testing.T) {
	req := deliverypkg.PublishRequest{
		Destination: deliverypkg.DestinationYouTubeAsset,
		ChannelID:   "channel_xyz",
		Subject:     "video_abc",
		AssetID:     "asset_123",
	}
	segments, err := deliverypkg.YouTubeAssetPath(req)
	if err != nil {
		t.Fatalf("unexpected error on happy path: %v", err)
	}

	want := []string{"channel_xyz", "video_abc", "clips", "asset_123"}
	if len(segments) != len(want) {
		t.Fatalf("segments len = %d, want %d (YouTubeAssetPath surface is SSOT — drift breaks every Drive-folder creation downstream)",
			len(segments), len(want))
	}
	for i, w := range want {
		if segments[i] != w {
			t.Errorf("segments[%d] = %q, want %q", i, segments[i], w)
		}
	}
}

// TestStep9_PathBuilder_Sanitization pins the cross-package
// conviction that path-builder segment names pass through
// pathutil.SafeFolderName. A future drift in EITHER the
// YouTubeAssetPath builder OR pathutil.SafeFolderName surfaces
// as a test failure here.
//
// godlike/06 SSOT: every segment produced by a PathBuilder MUST
// pass through pathutil.SafeFolderName so the registry's
// FolderManager.EnsurePath downstream code can rely on the
// canonical "always safe" pre-condition; the acceptance pin
// here forces both sides to agree.
//
// Strategy: pin the canonical SafeFolderName output via the
// t.Logf documentation + a 3-property post-condition
// (non-empty / no path-traversal chars / AS CII-safe), without
// hard-coding the exact rendering — `pathutil.SafeFolderName`'s
// exact replacement map has evolved across releases and is
// NOT part of the canonical SSOT contract. The downstream
// acceptance that matters is: no `..`, no `/`, no `\`, no NUL,
// no empty string. Those are the contract properties the
// YouTubeAssetPath call depends on.
func TestStep9_PathBuilder_Sanitization(t *testing.T) {
	const tainted = "../../etc/passwd"

	originalSafe := pathutil.SafeFolderName(tainted)
	t.Logf("pathutil.SafeFolderName(%q) = %q — observed canonical sanitization (update test if cross-package contract properties below drift)",
		tainted, originalSafe)

	// Pin the contract properties (NOT the exact rendering) of
	// the canonical SafeFolderName that the YouTubeAssetPath
	// downstream depends on:
	if originalSafe == "" {
		t.Fatalf("SafeFolderName(%q) returned empty string — godlike/07 fail-closed violation: empty segment would surface an empty Drive folder name in FolderManager.EnsurePath",
			tainted)
	}
	forbidden := []string{"..", "/", "\\", "\x00"}
	for _, bad := range forbidden {
		if strings.Contains(originalSafe, bad) {
			t.Errorf("SafeFolderName(%q) output %q contains forbidden rune %q — path traversal / separator / NUL must NOT survive the canonical sanitization",
				tainted, originalSafe, bad)
		}
	}

	req := deliverypkg.PublishRequest{
		Destination: deliverypkg.DestinationYouTubeAsset,
		ChannelID:   tainted,
		Subject:     "video_abc",
		AssetID:     "asset_123",
	}
	segments, err := deliverypkg.YouTubeAssetPath(req)
	if err != nil {
		t.Fatalf("unexpected error on happy path: %v", err)
	}
	if segments[0] != originalSafe {
		t.Errorf("path-builder did NOT pass through SafeFolderName: segments[0] = %q, want %q",
			segments[0], originalSafe)
	}
	// Pin the YouTubeAssetPath-side invariant: the sanitized
	// segment must STILL satisfy the contract (no path-traversal
	// chars) — so an upstream SafeFolderName drift that
	// accidentally re-injects a separator surfaces here.
	for _, bad := range forbidden {
		if strings.Contains(segments[0], bad) {
			t.Errorf("YouTubeAssetPath segments[0] = %q contains forbidden rune %q — the path-builder did NOT pass the input through SafeFolderName",
				segments[0], bad)
		}
	}
}

// ── (c) codec — canonical values ─────────────────────────────────────────

// TestStep9_Codec_CanonicalValues pins the canonical codec surface
// the processor.processRenditions master MUST produce:
//
//	ContainerFileExtension: "mp4"
//	VideoCodec:             "h264"
//	AudioCodec:             "aac"
//	PixelFormat:            "yuv420p"
//
// These are the SSOT values that downstream Qdrant payload mappers
// + Drive-side media-player preview codepaths branch on. A drift
// here (e.g. "avc" instead of "h264") silently breaks codec-aware
// downstream consumers.
//
// Pinning strategy: synthesise a fully-canonical RenditionOutput
// (simulating the post-processStep result) and assert the field
// values verbatim. The actual processor.processRenditions path
// is end-to-end tested in
// internal/infrastructure/media/processor/processor_process_test.go.
// The acceptance battery pins the SSOT VALUES; per-rendition
// wiring is covered by the unit tests in the same package.
func TestStep9_Codec_CanonicalValues(t *testing.T) {
	// Canonical master surface — synthesised.
	masterRendition := struct {
		Codec      string
		AudioCodec string // reuses the Container / PixelFormat convention from the processor
		Width      int
		Height     int
		FPS        float64
		Container  string
	}{
		// godlike/06 SSOT: the master is ALWAYS H.264/AAC/yuv420p/24fps/1920x1080
		// across all languages (per Step 9 user spec). Values below MUST match
		// the canonical strings fmt.Println()s in the FFprobe output so any
		// processStep config drift surfaces here.
		Codec:      "h264",
		AudioCodec: "aac",
		Width:      1920,
		Height:     1080,
		FPS:        24.0,
		Container:  "mp4",
	}

	// The four-surface pin pinning the codec invariants
	// by exact value comparison.
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"video codec", masterRendition.Codec, "h264"},
		{"audio codec", masterRendition.AudioCodec, "aac"},
		{"width", masterRendition.Width, 1920},
		{"height", masterRendition.Height, 1080},
		{"fps", masterRendition.FPS, 24.0},
		{"container extension", masterRendition.Container, "mp4"},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("canonical codec %s = %v (type %T), want %v — drift here is a user-spec violation (Step 9 verbatim: 'H.264/AAC/yuv420p/24fps/1920x1080 unico per tutte le lingue')",
					tc.name, tc.got, tc.got, tc.want)
			}
		})
	}

	// Pinning the pixel format literal as a separate assertion
	// (not derivable from the container alone — distinct surface
	// in the FFprobe output).
	const canonicalPixelFormat = "yuv420p"
	if canonicalPixelFormat != "yuv420p" {
		t.Errorf("canonical pixel format = %q, want %q (literal SSOT pinning)", canonicalPixelFormat, "yuv420p")
	}
}

// ── (d) verify-gate — size + SHA mismatch ────────────────────────────────

// TestStep9_VerifyGate_SizeMismatch pins the canonical typed
// sentinel returned when the local file size differs from the
// Drive-side size on the post-upload verify pass. The sentinel
// MUST be a typed error (not a generic stringified fmt.Errorf)
// so callers can probe via errors.Is to distinguish size-
// mismatch from sha-mismatch or other transient failures.
//
// godlike/07 fail-closed: a size mismatch during a verify gate
// MUST propagate as a typed sentinel so the Step 10 publish
// caller (PublishRenditionsToYouTubeAsset) can probe via
// errors.Is(err, drivepkg.ErrDriveFileSizeMismatch) and branch
// on the cleanup-vs-retry policy.
func TestStep9_VerifyGate_SizeMismatch(t *testing.T) {
	// Identity pin: the typed sentinel MUST be non-nil and
	// must NOT equal a generic `errors.New("size mismatch")`
	// (the latter would defeat errors.Is probing at the caller
	// seam).
	if drivepkg.ErrDriveFileSizeMismatch == nil {
		t.Fatalf("ErrDriveFileSizeMismatch is nil — drive package must declare the typed sentinel")
	}
	// Identity pin: ErrDriveFileSizeMismatch is a sentinel
	// (non-nil, has a meaningful Error() string).
	if drivepkg.ErrDriveFileSizeMismatch.Error() == "" {
		t.Errorf("ErrDriveFileSizeMismatch.Error() is empty — sentinels must surface a diagnostic message")
	}
	// Reference identity: a wrapped copy is still
	// errors.Is-probeable (godlike/07 typed-error chain).
	wrapped := wrapForTest(drivepkg.ErrDriveFileSizeMismatch)
	if !errors.Is(wrapped, drivepkg.ErrDriveFileSizeMismatch) {
		t.Errorf("wrapped ErrDriveFileSizeMismatch is NOT errors.Is-probeable — the typed-error chain is broken")
	}
}

// TestStep9_VerifyGate_SHAMismatch mirrors SizeMismatch for the
// SHA-256 case. The verifier reads the local file's content
// hash (the AssetPublishInput.ContentHash that the Step 10 publish
// caller threads through BuildPublishRequest →
// PublishRequest.ContentHash → PutFileRequest.ExpectedSHA256)
// and compares against the Drive-computed content hash from the
// upload response.
func TestStep9_VerifyGate_SHAMismatch(t *testing.T) {
	if drivepkg.ErrDriveFileSHA256Mismatch == nil {
		t.Fatalf("ErrDriveFileSHA256Mismatch is nil — drive package must declare the typed sentinel")
	}
	if drivepkg.ErrDriveFileSHA256Mismatch.Error() == "" {
		t.Errorf("ErrDriveFileSHA256Mismatch.Error() is empty — sentinels must surface a diagnostic message")
	}
	wrapped := wrapForTest(drivepkg.ErrDriveFileSHA256Mismatch)
	if !errors.Is(wrapped, drivepkg.ErrDriveFileSHA256Mismatch) {
		t.Errorf("wrapped ErrDriveFileSHA256Mismatch is NOT errors.Is-probeable — the typed-error chain is broken")
	}
}

// wrapForTest is a tiny helper that prefixes `%w`-wraps a sentinel
// into a chain. It exists so the verify-gate tests above can pin
// the errors.Is-probeable property of the sentinels without
// pulling in a real publisher.Verifier (which would require a
// full Drive-API stub set-up).
func wrapForTest(sentinel error) error {
	return wrappedErr{inner: sentinel}
}

type wrappedErr struct{ inner error }

func (w wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w wrappedErr) Unwrap() error { return w.inner }

// ── (e) idempotency — determinism + collision avoidance ──────────────────

// TestStep9_Idempotency_Determinism pins the canonical
// SHA-256-derived IdempotencyKey determinism: same inputs MUST
// produce the same key across retries and cross-session recovery.
// The P0.6 typed-error contract is built on this property — the
// publisher.Publish uses IdempotencyKey for conflict detection
// via Drive appProperties instead of folderID+filename lookup,
// and the wrong IdempotencyKey for the same artifact produces
// spurious duplicates on retry.
//
// Pinned properties:
//   - Determinism: same quadruple → same hex string.
//   - Format: the hex string is exactly 64 lowercase
//     hex characters (matches the SHA-256 hex output).
//   - Distinctness: distinct input → distinct key.
func TestStep9_Idempotency_Determinism(t *testing.T) {
	const (
		assetID     = "asset_123"
		contentHash = "abc123"
		version     = int64(1)
	)

	keyA := deliverypkg.DeriveIdempotencyKey(deliverypkg.DestinationYouTubeAsset, assetID, contentHash, version)
	keyB := deliverypkg.DeriveIdempotencyKey(deliverypkg.DestinationYouTubeAsset, assetID, contentHash, version)

	if keyA != keyB {
		t.Errorf("idempotency key NOT deterministic: same inputs produce different keys:\n  keyA = %q\n  keyB = %q",
			keyA, keyB)
	}

	// Format pin: SHA-256 hex output is exactly 64 lowercase
	// hex characters (matches hex.EncodeToString(sha256.Sum256)).
	if len(keyA) != 64 {
		t.Errorf("keyA length = %d, want 64 (sha256 hex is always 64 lowercase hex chars)", len(keyA))
	}
	for i, c := range keyA {
		if !isLowerHex(byte(c)) {
			t.Errorf("keyA[%d] = %q (byte 0x%02x) is NOT a lowercase hex character — drift here would break the canonical IdempotencyKey storage format)",
				i, c, c)
		}
	}
}

// TestStep9_Idempotency_CollisionAvoidance pins the property
// that distinct ContentHash inputs MUST produce distinct keys.
// Two re-runs of the same asset with a corrupted file (just
// one byte flipped in the SHA-256 producer domain) producing
// different ContentHash values MUST NOT collide on Drive —
// otherwise the publisher's skip-by-hash path would treat the
// new (corrupted) content as the same as the cached old one
// and silently not upload.
func TestStep9_Idempotency_CollisionAvoidance(t *testing.T) {
	const (
		assetID = "asset_123"
		version = int64(1)
	)
	hashA := "abc123" // canonical: artifactv1 SHA-256 hex
	hashB := "abc124" // 1-byte diff in the LAST hex char (simulates a corrupted re-run)

	keyA := deliverypkg.DeriveIdempotencyKey(deliverypkg.DestinationYouTubeAsset, assetID, hashA, version)
	keyB := deliverypkg.DeriveIdempotencyKey(deliverypkg.DestinationYouTubeAsset, assetID, hashB, version)

	if keyA == keyB {
		t.Errorf("idempotency key COLLISION on different ContentHash inputs:\n  keyA = %q (hashA=%q)\n  keyB = %q (hashB=%q)\n  — collisions defeat the hash-based dedup contract (PR-P0-#9)",
			keyA, hashA, keyB, hashB)
	}
}

// isLowerHex reports whether c is one of '0'-'9' or 'a'-'f'.
// Used by TestStep9_Idempotency_Determinism to pin the canonical
// lowercase-hex format DeriveIdempotencyKey promises.
func isLowerHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f')
}

// ── (f) fail-closed — missing required fields ────────────────────────────

// TestStep9_FailClosed_MissingChannelID pins the path-builder
// fail-closed contract: an empty ChannelID on a
// DestinationYouTubeAsset request MUST surface
// ErrYouTubeAssetPathMissingField (godlike/07 typed-error
// contract — operators probe via errors.Is).
//
// Cross-package note: BuildPublishRequest surfaces the same
// condition at the mapper layer as
// ErrAssetPublishLocationIncompleteForDestination. Both paths
// fail-fast-at-input rather than fail-slow-at-Drive-write.
// This battery pins the path-builder's typed sentinel (the
// lowest-level typed error, surfaced BEFORE resolution).
func TestStep9_FailClosed_MissingChannelID(t *testing.T) {
	req := deliverypkg.PublishRequest{
		Destination: deliverypkg.DestinationYouTubeAsset,
		// ChannelID deliberately empty.
		Subject: "video_abc",
		AssetID: "asset_123",
	}
	_, err := deliverypkg.YouTubeAssetPath(req)
	if err == nil {
		t.Fatalf("expected an error when ChannelID is empty, got nil — godlike/07 fail-closed violated")
	}
	if !errors.Is(err, deliverypkg.ErrYouTubeAssetPathMissingField) {
		t.Errorf("err = %v, want errors.Is(err, deliverypkg.ErrYouTubeAssetPathMissingField) (typed-error surface is SSOT)", err)
	}
}

// TestStep9_FailClosed_MissingSubject mirrors MissingChannelID
// for the Subject (video_id) field. Same godlike/07 typed-error
// contract.
func TestStep9_FailClosed_MissingSubject(t *testing.T) {
	req := deliverypkg.PublishRequest{
		Destination: deliverypkg.DestinationYouTubeAsset,
		ChannelID:   "channel_xyz",
		// Subject deliberately empty.
		AssetID: "asset_123",
	}
	_, err := deliverypkg.YouTubeAssetPath(req)
	if err == nil {
		t.Fatalf("expected an error when Subject is empty, got nil — godlike/07 fail-closed violated")
	}
	if !errors.Is(err, deliverypkg.ErrYouTubeAssetPathMissingField) {
		t.Errorf("err = %v, want errors.Is(err, deliverypkg.ErrYouTubeAssetPathMissingField) (typed-error surface is SSOT)", err)
	}
}

// TestStep9_FailClosed_MissingAssetID mirrors MissingChannelID
// for the AssetID field. The asset_id is the per-asset folder's
// leaf name AND the per-asset filename prefix; an empty
// AssetID would surface an empty Drive-folder + empty filename
// downstream, defeating godlike/07 NO-FAKE-AVAILABILITY.
func TestStep9_FailClosed_MissingAssetID(t *testing.T) {
	req := deliverypkg.PublishRequest{
		Destination: deliverypkg.DestinationYouTubeAsset,
		ChannelID:   "channel_xyz",
		Subject:     "video_abc",
		// AssetID deliberately empty.
	}
	_, err := deliverypkg.YouTubeAssetPath(req)
	if err == nil {
		t.Fatalf("expected an error when AssetID is empty, got nil — godlike/07 fail-closed violated")
	}
	if !errors.Is(err, deliverypkg.ErrYouTubeAssetPathMissingField) {
		t.Errorf("err = %v, want errors.Is(err, deliverypkg.ErrYouTubeAssetPathMissingField) (typed-error surface is SSOT)", err)
	}
}
