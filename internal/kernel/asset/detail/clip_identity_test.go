package detail

import (
	"strings"
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

func TestNewYouTubeClipIdentity_HappyPath(t *testing.T) {
	t.Parallel()
	id, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID:     "vdC5GXxS-qU",
		StartSec:    146,
		EndSec:      155,
		PolicyVer:   "v1",
		ContentHash: "sha256-abc123",
		Model:       "text-embedding-3-small",
		Version:     "v6",
		Collection:  "media_assets_current",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantAssetID := "yt_vdC5GXxS-qU_146_155_v1"
	if id.AssetID != wantAssetID {
		t.Errorf("AssetID = %q, want %q", id.AssetID, wantAssetID)
	}
	if id.ContentHash != "sha256-abc123" {
		t.Errorf("ContentHash = %q, want %q", id.ContentHash, "sha256-abc123")
	}
	if !strings.HasPrefix(id.IndexEventKey, "index:yt_vdC5GXxS-qU_146_155_v1:sha256-abc123:") {
		t.Errorf("IndexEventKey = %q, want prefix index:yt_vdC5GXxS-qU_146_155_v1:sha256-abc123:", id.IndexEventKey)
	}
}

func TestNewYouTubeClipIdentity_EmptyVideoID_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "", StartSec: 0, EndSec: 60, PolicyVer: "v1", ContentHash: "hash",
		Model: "m", Version: "v", Collection: "c",
	})
	if err != ErrClipIdentityEmptyAssetID {
		t.Errorf("err = %v, want ErrClipIdentityEmptyAssetID", err)
	}
}

func TestNewYouTubeClipIdentity_EmptyContentHash_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "abc", StartSec: 0, EndSec: 60, PolicyVer: "v1", ContentHash: "",
		Model: "m", Version: "v", Collection: "c",
	})
	if err != ErrClipIdentityEmptyContentHash {
		t.Errorf("err = %v, want ErrClipIdentityEmptyContentHash", err)
	}
}

func TestNewYouTubeClipIdentity_DefaultPolicyVersion(t *testing.T) {
	t.Parallel()
	id, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "abc", StartSec: 0, EndSec: 60, PolicyVer: "", ContentHash: "hash",
		Model: "m", Version: "v", Collection: "c",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(id.AssetID, "_v1") {
		t.Errorf("AssetID = %q, should contain default policy '_v1'", id.AssetID)
	}
}

func TestNewYouTubeClipIdentity_EndBeforeStart_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "abc", StartSec: 60, EndSec: 30, PolicyVer: "v1", ContentHash: "hash",
		Model: "m", Version: "v", Collection: "c",
	})
	if err != ErrClipIdentityInvalidTimestamps {
		t.Errorf("err = %v, want ErrClipIdentityInvalidTimestamps", err)
	}
}

func TestNewYouTubeClipIdentity_EqualStartEnd_OK(t *testing.T) {
	t.Parallel()
	id, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "abc", StartSec: 60, EndSec: 60, PolicyVer: "v1", ContentHash: "hash",
		Model: "m", Version: "v", Collection: "c",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(id.AssetID, "_60_60_") {
		t.Errorf("AssetID = %q, should contain _60_60_", id.AssetID)
	}
}

func TestNewYouTubeClipIdentity_WhitespaceVideoID_Trimmed(t *testing.T) {
	t.Parallel()
	id, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "  abc123  ", StartSec: 0, EndSec: 60, PolicyVer: "v1", ContentHash: "hash",
		Model: "m", Version: "v", Collection: "c",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(id.AssetID, "yt_abc123_") {
		t.Errorf("AssetID = %q, should start with yt_abc123_", id.AssetID)
	}
}

func TestNewYouTubeClipIdentity_MatchesLegacyFormat(t *testing.T) {
	t.Parallel()
	// Verify the format matches the legacy process_segment.go:208 derivation:
	// fmt.Sprintf("yt_%s_%d_%d_%s", cmd.VideoID, startSec, endSec, policyVer)
	legacy := "yt_vdC5GXxS-qU_146_155_v1"
	id, err := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "vdC5GXxS-qU", StartSec: 146, EndSec: 155, PolicyVer: "v1",
		ContentHash: "hash", Model: "m", Version: "v", Collection: "c",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.AssetID != legacy {
		t.Errorf("AssetID = %q, want %q (legacy format)", id.AssetID, legacy)
	}
}

// ── Stock builder ───────────────────────────────────────────────────

func TestNewStockClipIdentity_HappyPath(t *testing.T) {
	t.Parallel()
	id, err := NewStockClipIdentity(
		"a1b2c3d4e5f67890", 2,
		"sha256-video-hash",
		"text-embedding-3-small", "v6", "media_assets_current",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantAssetID := "planner:a1b2c3d4e5f67890:2"
	if id.AssetID != wantAssetID {
		t.Errorf("AssetID = %q, want %q", id.AssetID, wantAssetID)
	}
	if id.ContentHash != "sha256-video-hash" {
		t.Errorf("ContentHash = %q, want %q", id.ContentHash, "sha256-video-hash")
	}
	if !strings.HasPrefix(id.IndexEventKey, "index:planner:a1b2c3d4e5f67890:2:sha256-video-hash:") {
		t.Errorf("IndexEventKey = %q, want prefix index:planner:a1b2c3d4e5f67890:2:sha256-video-hash:", id.IndexEventKey)
	}
}

func TestNewStockClipIdentity_EmptyHashPrefix_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewStockClipIdentity("", 0, "hash", "m", "v", "c")
	if err != ErrClipIdentityEmptyAssetID {
		t.Errorf("err = %v, want ErrClipIdentityEmptyAssetID", err)
	}
}

func TestNewStockClipIdentity_EmptyContentHash_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewStockClipIdentity("abc", 0, "", "m", "v", "c")
	if err != ErrClipIdentityEmptyContentHash {
		t.Errorf("err = %v, want ErrClipIdentityEmptyContentHash", err)
	}
}

func TestNewStockClipIdentity_MatchesLegacyFormat(t *testing.T) {
	t.Parallel()
	// Verify the format matches planner.go:buildClipPlan:
	// fmt.Sprintf("planner:%x:%d", h[:8], idx)
	// h[:8] produces 16 hex chars.
	legacy := "planner:a1b2c3d4e5f67890:3"
	id, err := NewStockClipIdentity(
		"a1b2c3d4e5f67890", 3,
		"hash", "m", "v", "c",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.AssetID != legacy {
		t.Errorf("AssetID = %q, want %q (legacy format)", id.AssetID, legacy)
	}
}

func TestNewStockClipIdentity_ZeroIndex(t *testing.T) {
	t.Parallel()
	id, err := NewStockClipIdentity("abcdef0123456789", 0, "hash", "m", "v", "c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.AssetID != "planner:abcdef0123456789:0" {
		t.Errorf("AssetID = %q, want planner:abcdef0123456789:0", id.AssetID)
	}
}

// ── Generic builder ─────────────────────────────────────────────────

func TestNewClipIdentity_HappyPath(t *testing.T) {
	t.Parallel()
	id, err := NewClipIdentity("artlist_abc123", "hash", "m", "v", "c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.AssetID != "artlist_abc123" {
		t.Errorf("AssetID = %q, want %q", id.AssetID, "artlist_abc123")
	}
	if id.ContentHash != "hash" {
		t.Errorf("ContentHash = %q, want %q", id.ContentHash, "hash")
	}
}

func TestNewClipIdentity_EmptyAssetID_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewClipIdentity("", "hash", "m", "v", "c")
	if err != ErrClipIdentityEmptyAssetID {
		t.Errorf("err = %v, want ErrClipIdentityEmptyAssetID", err)
	}
}

func TestNewClipIdentity_EmptyContentHash_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewClipIdentity("abc", "", "m", "v", "c")
	if err != ErrClipIdentityEmptyContentHash {
		t.Errorf("err = %v, want ErrClipIdentityEmptyContentHash", err)
	}
}

func TestNewClipIdentity_WhitespaceOnlyAssetID_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewClipIdentity("   ", "hash", "m", "v", "c")
	if err != ErrClipIdentityEmptyAssetID {
		t.Errorf("err = %v, want ErrClipIdentityEmptyAssetID", err)
	}
}

func TestNewClipIdentity_WhitespaceOnlyContentHash_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewClipIdentity("abc", "   ", "m", "v", "c")
	if err != ErrClipIdentityEmptyContentHash {
		t.Errorf("err = %v, want ErrClipIdentityEmptyContentHash", err)
	}
}

// ── BuildIndexEventKey ──────────────────────────────────────────────

func TestBuildIndexEventKey_DeterministicAcrossRetries(t *testing.T) {
	t.Parallel()
	k1 := BuildIndexEventKey("yt_abc_0_60_v1", "hash123", "model", "v1", "coll")
	k2 := BuildIndexEventKey("yt_abc_0_60_v1", "hash123", "model", "v1", "coll")
	if k1 != k2 {
		t.Errorf("keys differ across retries: %q != %q", k1, k2)
	}
}

func TestBuildIndexEventKey_DifferentInputsProduceDifferentKeys(t *testing.T) {
	t.Parallel()
	k1 := BuildIndexEventKey("yt_abc_0_60_v1", "hash1", "m", "v", "c")
	k2 := BuildIndexEventKey("yt_abc_0_60_v1", "hash2", "m", "v", "c")
	if k1 == k2 {
		t.Errorf("different content hashes should produce different keys, both = %q", k1)
	}
	k3 := BuildIndexEventKey("yt_abc_0_60_v1", "hash1", "m", "v", "c")
	k4 := BuildIndexEventKey("yt_abc_0_90_v1", "hash1", "m", "v", "c")
	if k3 == k4 {
		t.Errorf("different asset IDs should produce different keys, both = %q", k3)
	}
}

func TestBuildIndexEventKey_SixSegments(t *testing.T) {
	t.Parallel()
	key := BuildIndexEventKey("id1", "hash1", "model1", "ver1", "coll1")
	parts := strings.Split(key, ":")
	if len(parts) != 6 {
		t.Errorf("expected 6 colon-separated segments, got %d in %q", len(parts), key)
	}
	if parts[0] != "index" {
		t.Errorf("parts[0] = %q, want %q", parts[0], "index")
	}
	if parts[1] != "id1" {
		t.Errorf("parts[1] = %q, want %q", parts[1], "id1")
	}
	if parts[2] != "hash1" {
		t.Errorf("parts[2] = %q, want %q", parts[2], "hash1")
	}
	if parts[3] != "model1" {
		t.Errorf("parts[3] = %q, want %q", parts[3], "model1")
	}
	if parts[4] != "ver1" {
		t.Errorf("parts[4] = %q, want %q", parts[4], "ver1")
	}
	if parts[5] != "coll1" {
		t.Errorf("parts[5] = %q, want %q", parts[5], "coll1")
	}
}

func TestBuildIndexEventKey_EmptyInputs_ReturnsValidFormat(t *testing.T) {
	t.Parallel()
	// Empty inputs produce a valid format with empty segments.
	// Callers are responsible for non-empty checks via New*ClipIdentity.
	key := BuildIndexEventKey("", "", "", "", "")
	parts := strings.Split(key, ":")
	if len(parts) != 6 {
		t.Errorf("expected 6 segments even on empty input, got %d", len(parts))
	}
}

// ── Sentinels ───────────────────────────────────────────────────────

func TestSentinels_ErrorsIsReachable(t *testing.T) {
	t.Parallel()
	// Verify that both sentinels are reachable via errors.Is.
	_, err1 := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "", StartSec: 0, EndSec: 0, PolicyVer: "", ContentHash: "hash",
		Model: "m", Version: "v", Collection: "c",
	})
	if err1 != ErrClipIdentityEmptyAssetID {
		t.Errorf("YouTube empty videoID: err = %v, want ErrClipIdentityEmptyAssetID", err1)
	}
	_, err2 := NewStockClipIdentity("", 0, "hash", "m", "v", "c")
	if err2 != ErrClipIdentityEmptyAssetID {
		t.Errorf("Stock empty hashPrefix: err = %v, want ErrClipIdentityEmptyAssetID", err2)
	}
	_, err3 := NewClipIdentity("", "hash", "m", "v", "c")
	if err3 != ErrClipIdentityEmptyAssetID {
		t.Errorf("Generic empty assetID: err = %v, want ErrClipIdentityEmptyAssetID", err3)
	}
	_, err4 := NewYouTubeClipIdentity(YouTubeClipIdentityParams{
		VideoID: "abc", StartSec: 0, EndSec: 0, PolicyVer: "", ContentHash: "",
		Model: "m", Version: "v", Collection: "c",
	})
	if err4 != ErrClipIdentityEmptyContentHash {
		t.Errorf("YouTube empty contentHash: err = %v, want ErrClipIdentityEmptyContentHash", err4)
	}
}

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

// TestParseYouTubeClipAssetID_RoundTrip pins the inverse of the SOLE
// format owner: builder output parses back to its inputs, including the
// two hard shapes (underscored video IDs, multi-token policy versions).
func TestParseYouTubeClipAssetID_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		videoID, policy  string
		startSec, endSec int
	}{
		{"dQw4w9WgXcQ", "v1", 0, 60},
		{"a_b_c_12", "v1", 42, 57},              // videoID with underscores
		{"Di-Awl0XyQs", "whisper_v1", 125, 250}, // multi-token policy
	} {
		id, err := YouTubeClipAssetID(tc.videoID, tc.startSec, tc.endSec, tc.policy)
		if err != nil {
			t.Fatalf("build(%q): %v", tc.videoID, err)
		}
		vid, start, end, policy, err := ParseYouTubeClipAssetID(id)
		if err != nil {
			t.Fatalf("parse(%q): %v", id, err)
		}
		if vid != tc.videoID || start != tc.startSec || end != tc.endSec || policy != tc.policy {
			t.Errorf("round trip %q = (%q,%d,%d,%q), want (%q,%d,%d,%q)",
				id, vid, start, end, policy, tc.videoID, tc.startSec, tc.endSec, tc.policy)
		}
	}
}

// TestParseYouTubeClipAssetID_FailClosed pins godlike/07: malformed ids
// return ErrYouTubeClipAssetIDShape instead of partial values.
func TestParseYouTubeClipAssetID_FailClosed(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "yt_", "abc_0_60_v1", "yt_abc_0_60", "yt_abc_x_60_v1", "yt_abc_60_0_v1"} {
		if _, _, _, _, err := ParseYouTubeClipAssetID(bad); err == nil {
			t.Errorf("ParseYouTubeClipAssetID(%q) = nil error, want shape failure", bad)
		}
	}
}
