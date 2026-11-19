package detail

import (
	"testing"
)

// ── YouTube asset id (format SSOT) ─────────────────────────────────

// TestYouTubeClipAssetID_CanonicalFormat pins the single owner of the
// yt_<videoID>_<start>_<end>_<policy> format. Both ingest paths (the canonical
// extraction pipeline and the sourcing registration path) call this function,
// so a format change MUST land here and nowhere else.
func TestYouTubeClipAssetID_CanonicalFormat(t *testing.T) {
	t.Parallel()
	got, err := YouTubeClipAssetID("vLRjqTIiMjc", 100, 184, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "yt_vLRjqTIiMjc_100_184_v1"; got != want {
		t.Errorf("YouTubeClipAssetID = %q, want %q", got, want)
	}
}

// TestYouTubeClipAssetID_PureFunctionOfWindowAndPolicy locks the dedup
// property: the identity depends ONLY on (videoID, window, policyVersion) and
// never on the downloaded bytes. The same window re-cut with different bytes
// must keep the same asset id (content changes are handled by the supersede
// gate, not by minting a second asset row).
func TestYouTubeClipAssetID_PureFunctionOfWindowAndPolicy(t *testing.T) {
	t.Parallel()
	first, err := YouTubeClipAssetID("vLRjqTIiMjc", 39, 90, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := YouTubeClipAssetID("vLRjqTIiMjc", 39, 90, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Errorf("identity is not deterministic: %q vs %q", first, second)
	}

	variants := map[string]string{
		"other window":  mustYouTubeClipAssetID(t, "vLRjqTIiMjc", 39, 91, "v1"),
		"other start":   mustYouTubeClipAssetID(t, "vLRjqTIiMjc", 40, 90, "v1"),
		"other video":   mustYouTubeClipAssetID(t, "aaaaaaaaaaa", 39, 90, "v1"),
		"other policy:": mustYouTubeClipAssetID(t, "vLRjqTIiMjc", 39, 90, "whisper_v1"),
	}
	for name, id := range variants {
		if id == first {
			t.Errorf("%s produced the SAME identity %q; distinct clips would collide on one media_assets row", name, id)
		}
	}
}

func TestYouTubeClipAssetID_DefaultPolicyVersion(t *testing.T) {
	t.Parallel()
	got, err := YouTubeClipAssetID("vLRjqTIiMjc", 0, 25, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "yt_vLRjqTIiMjc_0_25_" + DefaultYouTubeClipPolicyVersion; got != want {
		t.Errorf("YouTubeClipAssetID = %q, want %q", got, want)
	}
}

func TestYouTubeClipAssetID_TrimmedVideoID(t *testing.T) {
	t.Parallel()
	got, err := YouTubeClipAssetID("  vLRjqTIiMjc\n", 0, 25, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "yt_vLRjqTIiMjc_0_25_v1"; got != want {
		t.Errorf("YouTubeClipAssetID = %q, want %q", got, want)
	}
}

func TestYouTubeClipAssetID_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		videoID string
		start   int
		end     int
		wantErr error
	}{
		{name: "empty video id", videoID: "", start: 0, end: 25, wantErr: ErrClipIdentityEmptyAssetID},
		{name: "whitespace video id", videoID: "   ", start: 0, end: 25, wantErr: ErrClipIdentityEmptyAssetID},
		{name: "end before start", videoID: "abc", start: 60, end: 30, wantErr: ErrClipIdentityInvalidTimestamps},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := YouTubeClipAssetID(tc.videoID, tc.start, tc.end, "v1")
			if err != tc.wantErr {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// mustYouTubeClipAssetID is the test-local wrapper that fails on error.
func mustYouTubeClipAssetID(t *testing.T, videoID string, start, end int, policy string) string {
	t.Helper()
	id, err := YouTubeClipAssetID(videoID, start, end, policy)
	if err != nil {
		t.Fatalf("YouTubeClipAssetID(%q,%d,%d,%q): %v", videoID, start, end, policy, err)
	}
	return id
}

// ── YouTube builder ─────────────────────────────────────────────────

// ── Stock builder ───────────────────────────────────────────────────

// ── Generic builder ─────────────────────────────────────────────────

// ── BuildIndexEventKey ──────────────────────────────────────────────

// ── Sentinels ───────────────────────────────────────────────────────

// ── Compile-time pin ───────────────────────────────────────────────

func TestClipIdentityStruct_FieldsAccessible(t *testing.T) {
	t.Parallel()
	id := ClipIdentity{
		AssetID:       "test",
		ContentHash:   "hash",
		IndexEventKey: "key",
	}
	if id.AssetID != "test" || id.ContentHash != "hash" || id.IndexEventKey != "key" {
		t.Errorf("struct fields not accessible: %+v", id)
	}
}
