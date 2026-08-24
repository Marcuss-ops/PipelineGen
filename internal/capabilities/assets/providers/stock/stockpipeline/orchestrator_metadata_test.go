package stockpipeline

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── SourceProvider / SourceVideoID inference (planner.go) ────────
//
// PR-003 (July 2026) promotes these helpers to the canonical
// inference site. They MUST be the only place that classifies
// URLs — step_publish.go reads plan.SourceProvider verbatim, no
// re-parse.

func TestInferSourceProvider_YouTubeDotCom(t *testing.T) {
	if got := inferSourceProvider("https://www.youtube.com/watch?v=abc123"); got != SourceProviderYouTube {
		t.Fatalf("inferSourceProvider(youtube.com) = %q, want %q", got, SourceProviderYouTube)
	}
}

func TestInferSourceProvider_YouTubeBe(t *testing.T) {
	if got := inferSourceProvider("https://youtu.be/abc123"); got != SourceProviderYouTube {
		t.Fatalf("inferSourceProvider(youtu.be) = %q, want %q", got, SourceProviderYouTube)
	}
}

func TestInferSourceProvider_Pexels(t *testing.T) {
	if got := inferSourceProvider("https://www.pexels.com/video/foo-123/"); got != SourceProviderPexels {
		t.Fatalf("inferSourceProvider(pexels.com) = %q, want %q", got, SourceProviderPexels)
	}
}

func TestInferSourceProvider_Pixabay(t *testing.T) {
	if got := inferSourceProvider("https://pixabay.com/videos/foo-123/"); got != SourceProviderPixabay {
		t.Fatalf("inferSourceProvider(pixabay.com) = %q, want %q", got, SourceProviderPixabay)
	}
}

func TestInferSourceProvider_UnknownForUnrecognizedDomain(t *testing.T) {
	cases := []string{
		"https://vimeo.com/123456789",
		"https://example.com/video.mp4",
		"https://archive.org/details/foo",
		"blob:https://example.com/12345-67890",
	}
	for _, u := range cases {
		if got := inferSourceProvider(u); got != SourceProviderUnknown {
			t.Errorf("inferSourceProvider(%q) = %q, want %q", u, got, SourceProviderUnknown)
		}
	}
}

// Lock the godlike/07 NO-FAKE-AVAILABILITY / SSRF-adjacent fix:
// substring-matching the full URL would let attacker-controlled
// domains like `fake-youtube.com.attacker.io` or
// `evil.example/?redirect=youtube.com` slip into the YouTube
// bucket. The hostname-parse + suffix-match implementation rejects
// these. This is the canonical regression guard for the fix.
func TestInferSourceProvider_RejectsSSRFStyleSubdomainAttack(t *testing.T) {
	cases := []string{
		"https://fake-youtube.com.attacker.io/video.mp4",
		"https://evil.example.com/?redirect=youtube.com",
		"https://cdn.evil.com/youtube.com-thumb.jpg",
		"https://fake-pexels.attacker.io/foo",
		"https://fake-pixabay.attacker.io/foo",
		"https://notyoutube.com.gc/",
	}
	for _, u := range cases {
		if got := inferSourceProvider(u); got != SourceProviderUnknown {
			t.Errorf("inferSourceProvider(%q) = %q, want %q (SSRF vector)",
				u, got, SourceProviderUnknown)
		}
	}
}

func TestInferSourceProvider_AcceptsProviderSubdomains(t *testing.T) {
	// The suffix-match must accept real provider subdomains
	// (m.youtube.com, www.pixabay.com, it.pexels.com).
	cases := map[string]string{
		"https://m.youtube.com/watch?v=abc":    SourceProviderYouTube,
		"https://music.youtube.com/watch?v=ab": SourceProviderYouTube,
		"https://www.pexels.com/video/foo/":    SourceProviderPexels,
		"https://it.pexels.com/video/foo/":     SourceProviderPexels,
		"https://www.pixabay.com/videos/foo/":  SourceProviderPixabay,
		"https://de.pixabay.com/videos/foo/":   SourceProviderPixabay,
	}
	for u, want := range cases {
		if got := inferSourceProvider(u); got != want {
			t.Errorf("inferSourceProvider(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestInferSourceProvider_EmptyURLFallsToUnknown(t *testing.T) {
	if got := inferSourceProvider(""); got != SourceProviderUnknown {
		t.Fatalf("inferSourceProvider(empty) = %q, want %q", got, SourceProviderUnknown)
	}
}

func TestInferSourceVideoID_YouTubeWatchURL(t *testing.T) {
	if got := inferSourceVideoID("https://www.youtube.com/watch?v=abc123XYZ"); got != "abc123XYZ" {
		t.Fatalf("inferSourceVideoID(watch URL) = %q, want %q", got, "abc123XYZ")
	}
}

func TestInferSourceVideoID_YouTubeShortsURL(t *testing.T) {
	if got := inferSourceVideoID("https://www.youtube.com/shorts/shortID42"); got != "shortID42" {
		t.Fatalf("inferSourceVideoID(shorts URL) = %q, want %q", got, "shortID42")
	}
}

func TestInferSourceVideoID_YouTuBeShortURL(t *testing.T) {
	// Parity with pkg/urlutil: youtu.be shortlinks extract the
	// path-stripped slug as the canonical video ID.
	if got := inferSourceVideoID("https://youtu.be/abc123"); got != "abc123" {
		t.Fatalf("inferSourceVideoID(youtu.be) = %q, want %q", got, "abc123")
	}
}

func TestInferSourceVideoID_NonYouTubeEmpty(t *testing.T) {
	if got := inferSourceVideoID("https://www.pexels.com/video/foo/"); got != "" {
		t.Fatalf("inferSourceVideoID(pexels) = %q, want empty (provider mismatch → fail-open)", got)
	}
}

func TestInferSourceVideoID_YouTubeChannelPageEmpty(t *testing.T) {
	// Channel URL is YouTube domain but pkg/urlutil::ExtractVideoID
	// returns an error. Fail-open → SourceVideoID = "" (godlike/07
	// observability field, not a gate).
	if got := inferSourceVideoID("https://www.youtube.com/channel/UCabc123"); got != "" {
		t.Fatalf("inferSourceVideoID(channel URL) = %q, want empty (fail-open)", got)
	}
}

// ── buildStockRunMetadata propagation ────────────────────────────
//
// Step_publish.go populates ChunkState from ClipPlan; this is the
// shape-aware surface that TDD pins. Existing test (below) keeps
// the timestamp-fields lock; new tests pin the 3 PR-003 additions.

func TestBuildStockRunMetadata_IncludesTimestampFields(t *testing.T) {
	in := &RunInput{
		FolderID:      "workflow-123",
		Subfolder:     "boxing",
		ClipDuration:  12,
		ChunkDuration: 12,
		PolicyVersion: "policy-v1",
	}
	chunks := []ChunkState{
		{
			Index:        0,
			ArtifactID:   "stock:fp:timestamp:0:video",
			Filename:     "stock_fp_chunk_0.mp4",
			LocalPath:    "/tmp/clip-0.mp4",
			SourceURL:    "https://www.youtube.com/watch?v=abc",
			StartSec:     32,
			EndSec:       51,
			Title:        "Round 1",
			Description:  "Pacquiao steps in with a quick left cross and angles off.",
			SHA256:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SizeBytes:    1234,
			RemoteFileID: "drive-file-1",
		},
	}

	meta := buildStockRunMetadata(in, chunks, "run-fp")
	if len(meta.Chunks) != 1 {
		t.Fatalf("expected 1 chunk entry, got %d", len(meta.Chunks))
	}
	entry := meta.Chunks[0]
	if entry.SourceURL != chunks[0].SourceURL {
		t.Fatalf("expected SourceURL %q, got %q", chunks[0].SourceURL, entry.SourceURL)
	}
	if entry.StartSec != chunks[0].StartSec {
		t.Fatalf("expected StartSec %v, got %v", chunks[0].StartSec, entry.StartSec)
	}
	if entry.EndSec != chunks[0].EndSec {
		t.Fatalf("expected EndSec %v, got %v", chunks[0].EndSec, entry.EndSec)
	}
	if entry.Title != chunks[0].Title {
		t.Fatalf("expected Title %q, got %q", chunks[0].Title, entry.Title)
	}
	if entry.Description != chunks[0].Description {
		t.Fatalf("expected Description %q, got %q", chunks[0].Description, entry.Description)
	}
}

func TestBuildStockRunMetadata_PropagatesSourceProvider(t *testing.T) {
	in := &RunInput{FolderID: "wf", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{
			Index:          0,
			ArtifactID:     "stock:fp:c:0",
			SourceURL:      "https://www.youtube.com/watch?v=vid123",
			SourceProvider: SourceProviderYouTube,
			TotalChunks:    2,
			SHA256:         "a64chars_filler________________________________________________0",
			SizeBytes:      100,
			RemoteFileID:   "drive-0",
		},
		{
			Index:          1,
			ArtifactID:     "stock:fp:c:1",
			SourceURL:      "https://www.pexels.com/video/foo/",
			SourceProvider: SourceProviderPexels,
			TotalChunks:    2,
			SHA256:         "a64chars_filler________________________________________________1",
			SizeBytes:      200,
			RemoteFileID:   "drive-1",
		},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")
	if got, want := meta.Chunks[0].SourceProvider, SourceProviderYouTube; got != want {
		t.Errorf("chunk[0].SourceProvider = %q, want %q", got, want)
	}
	if got, want := meta.Chunks[1].SourceProvider, SourceProviderPexels; got != want {
		t.Errorf("chunk[1].SourceProvider = %q, want %q", got, want)
	}
}

func TestBuildStockRunMetadata_PropagatesSourceVideoIDForYouTubeOnly(t *testing.T) {
	in := &RunInput{FolderID: "wf", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{
			Index:          0,
			ArtifactID:     "stock:fp:c:0",
			SourceURL:      "https://www.youtube.com/watch?v=vidABC",
			SourceProvider: SourceProviderYouTube,
			SourceVideoID:  "vidABC",
			TotalChunks:    3,
			SHA256:         "a64chars_filler________________________________________________0",
			SizeBytes:      100,
			RemoteFileID:   "drive-0",
		},
		{
			Index:          1,
			ArtifactID:     "stock:fp:c:1",
			SourceURL:      "https://www.pexels.com/video/foo/",
			SourceProvider: SourceProviderPexels,
			SourceVideoID:  "", // canonical: non-YouTube → empty
			TotalChunks:    3,
			SHA256:         "a64chars_filler________________________________________________1",
			SizeBytes:      200,
			RemoteFileID:   "drive-1",
		},
		{
			Index:          2,
			ArtifactID:     "stock:fp:c:2",
			SourceURL:      "https://vimeo.com/12345",
			SourceProvider: SourceProviderUnknown,
			SourceVideoID:  "", // canonical: unknown → empty
			TotalChunks:    3,
			SHA256:         "a64chars_filler________________________________________________2",
			SizeBytes:      300,
			RemoteFileID:   "drive-2",
		},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")
	if got, want := meta.Chunks[0].SourceVideoID, "vidABC"; got != want {
		t.Errorf("chunk[0].SourceVideoID = %q, want %q", got, want)
	}
	if got := meta.Chunks[1].SourceVideoID; got != "" {
		t.Errorf("chunk[1].SourceVideoID = %q, want empty (pexels)", got)
	}
	if got := meta.Chunks[2].SourceVideoID; got != "" {
		t.Errorf("chunk[2].SourceVideoID = %q, want empty (unknown)", got)
	}
}

func TestBuildStockRunMetadata_TotalChunksRepeatedPerEntry(t *testing.T) {
	// Per user spec: TotalChunks is repeated per-entry even though
	// it's logically a per-run scalar. This is intentional — see
	// ChunkState.TotalChunks godoc + ChunkMetadataEntry field doc.
	in := &RunInput{FolderID: "wf", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:fp:c:0", SourceURL: "https://example.com/v.mp4", SourceProvider: SourceProviderUnknown, TotalChunks: 5, SHA256: "a64chars_filler________________________________________________0", SizeBytes: 1, RemoteFileID: "d0"},
		{Index: 1, ArtifactID: "stock:fp:c:1", SourceURL: "https://example.com/v.mp4", SourceProvider: SourceProviderUnknown, TotalChunks: 5, SHA256: "a64chars_filler________________________________________________1", SizeBytes: 1, RemoteFileID: "d1"},
		{Index: 2, ArtifactID: "stock:fp:c:2", SourceURL: "https://example.com/v.mp4", SourceProvider: SourceProviderUnknown, TotalChunks: 5, SHA256: "a64chars_filler________________________________________________2", SizeBytes: 1, RemoteFileID: "d2"},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")
	for i, entry := range meta.Chunks {
		if entry.TotalChunks != 5 {
			t.Errorf("chunk[%d].TotalChunks = %d, want 5 (repeated per-entry)", i, entry.TotalChunks)
		}
	}
}

func TestBuildStockRunMetadata_EmptyProviderOmitempty(t *testing.T) {
	// When ChunkState.SourceProvider is empty (path unknown/null),
	// the JSON tag `omitempty` returns it as absent. Verify the
	// pure-Go struct field copy still preserves the empty string.
	in := &RunInput{FolderID: "wf", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:fp:c:0", SourceURL: "", SourceProvider: "", SourceVideoID: "", TotalChunks: 0, SHA256: "a64chars_filler________________________________________________0", SizeBytes: 1, RemoteFileID: "d0"},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")
	entry := meta.Chunks[0]
	if entry.SourceProvider != "" {
		t.Errorf("entry.SourceProvider = %q, want empty", entry.SourceProvider)
	}
	if entry.SourceVideoID != "" {
		t.Errorf("entry.SourceVideoID = %q, want empty", entry.SourceVideoID)
	}
	// Note: TotalChunks=0 is preserved at struct level; omitempty
	// only applies to JSON encoding (which we're not exercising here).
	if entry.TotalChunks != 0 {
		t.Errorf("entry.TotalChunks = %d, want 0 (zero value)", entry.TotalChunks)
	}
}

// ── StockPlan step integration (clipPlan → ChunkState plumbing) ──
//
// Per godlike/06 lockstep-discipline, the inference is at plan-build
// time. step_publish.go reads plan.SourceProvider/SourceVideoID
// verbatim (no re-parse). This integration test pins that contract
// by exercising ClipPlanner with a YouTube URL + asserting the plan
// shape carries the inferred fields.

func TestDeterministicPlanner_InfersProviderAndVideoIDAtPlanBuildTime(t *testing.T) {
	p := NewDeterministicPlanner()
	plans, err := p.Plan(nil, VideoSource{URL: "https://www.youtube.com/watch?v=VID78abc"}, 60, 10, "v1")
	if err != nil {
		t.Fatalf("plan.Plan returned err = %v, want nil", err)
	}
	if len(plans) == 0 {
		t.Fatal("expected non-empty plans")
	}
	for i, plan := range plans {
		if plan.SourceProvider != SourceProviderYouTube {
			t.Errorf("plans[%d].SourceProvider = %q, want %q", i, plan.SourceProvider, SourceProviderYouTube)
		}
		if plan.SourceVideoID != "VID78abc" {
			t.Errorf("plans[%d].SourceVideoID = %q, want %q", i, plan.SourceVideoID, "VID78abc")
		}
	}
}

func TestExplicitPlanner_InfersProviderAndVideoIDAtPlanBuildTime(t *testing.T) {
	clips := []ClipSpec{
		{URL: "https://www.youtube.com/watch?v=EXP99xyz", StartSec: 0, EndSec: 10, Title: "Clip A"},
		{URL: "https://www.youtube.com/watch?v=EXP99xyz", StartSec: 10, EndSec: 20, Title: "Clip B"},
	}
	plans, err := NewExplicitPlanner(clips).Plan(nil, VideoSource{URL: clips[0].URL}, 0, 0, "v1")
	if err != nil {
		t.Fatalf("explicit.Plan returned err = %v, want nil", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	for i, plan := range plans {
		if plan.SourceProvider != SourceProviderYouTube {
			t.Errorf("plans[%d].SourceProvider = %q, want %q", i, plan.SourceProvider, SourceProviderYouTube)
		}
		if plan.SourceVideoID != "EXP99xyz" {
			t.Errorf("plans[%d].SourceVideoID = %q, want %q", i, plan.SourceVideoID, "EXP99xyz")
		}
	}
}

// ── PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026) — 4 content fields ──
//
// Round / Tags / Category / Slug travel ClipSpec → ClipPlan →
// ChunkState → ChunkMetadataEntry verbatim. The buildStockRunMetadata
// pure function is the canonical seam where ChunkState → entry
// happens; the test below pins the propagation contract.

func TestBuildStockRunMetadata_PropagatesRoundTagsCategorySlug(t *testing.T) {
	in := &RunInput{FolderID: "wf-4fields", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{
			Index:        0,
			ArtifactID:   "stock:fp:c:0",
			SHA256:       "a64chars_filler________________________________________________0",
			SizeBytes:    100,
			RemoteFileID: "d0",
			Round:        7,
			Tags:         []string{"boxing", "pacquiao", "broner", "round-7"},
			Category:     "boxing",
			Slug:         "round-7-broner-barcolla",
		},
		{
			Index:        1,
			ArtifactID:   "stock:fp:c:1",
			SHA256:       "a64chars_filler________________________________________________1",
			SizeBytes:    200,
			RemoteFileID: "d1",
			Round:        1,
			Tags:         []string{"boxing", "pacquiao", "round-1"},
			Category:     "boxing",
			Slug:         "round-1-la-fase-di-studio",
		},
		{
			Index:        2,
			ArtifactID:   "stock:fp:c:2",
			SHA256:       "a64chars_filler________________________________________________2",
			SizeBytes:    300,
			RemoteFileID: "d2",
			// Round=0, Tags=nil, Category="", Slug="" — deterministic-planner case
		},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")
	if len(meta.Chunks) != 3 {
		t.Fatalf("expected 3 chunk entries, got %d", len(meta.Chunks))
	}
	// Chunk 0: all 4 fields populated.
	if got, want := meta.Chunks[0].Round, 7; got != want {
		t.Errorf("chunks[0].Round = %d, want %d", got, want)
	}
	if got, want := meta.Chunks[0].Category, "boxing"; got != want {
		t.Errorf("chunks[0].Category = %q, want %q", got, want)
	}
	if got, want := meta.Chunks[0].Slug, "round-7-broner-barcolla"; got != want {
		t.Errorf("chunks[0].Slug = %q, want %q", got, want)
	}
	wantTags0 := []string{"boxing", "pacquiao", "broner", "round-7"}
	if len(meta.Chunks[0].Tags) != len(wantTags0) {
		t.Fatalf("chunks[0].Tags length = %d, want %d (slice drift)", len(meta.Chunks[0].Tags), len(wantTags0))
	}
	for i, want := range wantTags0 {
		if meta.Chunks[0].Tags[i] != want {
			t.Errorf("chunks[0].Tags[%d] = %q, want %q", i, meta.Chunks[0].Tags[i], want)
		}
	}
	// Chunk 1: round 1 + distinct slug.
	if got, want := meta.Chunks[1].Round, 1; got != want {
		t.Errorf("chunks[1].Round = %d, want %d", got, want)
	}
	if got, want := meta.Chunks[1].Slug, "round-1-la-fase-di-studio"; got != want {
		t.Errorf("chunks[1].Slug = %q, want %q", got, want)
	}
	// Chunk 2: deterministic-planner case (zero values) preserved
	// at struct level. omitempty drops them from JSON wire, which
	// the next test pins.
	if got := meta.Chunks[2].Round; got != 0 {
		t.Errorf("chunks[2].Round = %d, want 0 (deterministic-planner zero value)", got)
	}
	if got := meta.Chunks[2].Category; got != "" {
		t.Errorf("chunks[2].Category = %q, want empty", got)
	}
	if got := meta.Chunks[2].Slug; got != "" {
		t.Errorf("chunks[2].Slug = %q, want empty", got)
	}
	if got := meta.Chunks[2].Tags; len(got) != 0 {
		t.Errorf("chunks[2].Tags = %v, want empty/nil", got)
	}
}

// TestBuildStockRunMetadata_ZeroRoundTagsCategorySlug_OmitsFromJSON pins
// the godlike/07 NO-FAKE-AVAILABILITY contract: when Round/Tags/
// Category/Slug are at zero-value (the deterministic-planner case),
// the JSON wire shape does NOT include those keys. Pre-PR baseline
// maintained: legacy search/direct-url paths produce the same
// wire shape they did before this front.
func TestBuildStockRunMetadata_ZeroRoundTagsCategorySlug_OmitsFromJSON(t *testing.T) {
	in := &RunInput{FolderID: "wf", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:fp:c:0", SourceProvider: SourceProviderUnknown, TotalChunks: 1, SHA256: "a64chars_filler________________________________________________0", SizeBytes: 1, RemoteFileID: "d0"},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")
	raw, mErr := json.Marshal(meta)
	if mErr != nil {
		t.Fatalf("json.Marshal(meta) failed: %v", mErr)
	}
	for _, absent := range []string{`"round"`, `"tags"`, `"category"`, `"slug"`} {
		if strings.Contains(string(raw), absent) {
			t.Errorf("JSON wire shape contains %q but should omit it (omitempty contract for zero-value Front 2 fields)", absent)
		}
	}
}

// ── IndexingStatus literal (PR-008) ───────────────────────────────
//
// stock.finalize must NOT do a media_assets.index_state DB SELECT in
// the hot path. The projection-time literal is hardcoded; the
// IndexingHandler downstream overwrites it to "INDEXED" in the
// Qdrant payload after a successful upsert. media_assets.index_state
// in the SQLite DB remains the canonical SSOT for retry/decision
// logic. This test pins the projection-time literal exactly.

func TestBuildStockRunMetadata_IndexingStatusLiteralIsPending(t *testing.T) {
	in := &RunInput{FolderID: "wf", ClipDuration: 10, ChunkDuration: 10, PolicyVersion: "v1"}
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:fp:c:0", SourceURL: "https://example.com/v.mp4", SourceProvider: SourceProviderUnknown, TotalChunks: 1, SHA256: "a64chars_filler________________________________________________0", SizeBytes: 1, RemoteFileID: "d0"},
	}
	meta := buildStockRunMetadata(in, chunks, "fp")

	// godlike/06 SSOT: the field uses the canonical constant.
	if got, want := meta.IndexingStatus, IndexingStatusPending; got != want {
		t.Errorf("meta.IndexingStatus = %q, want %q (canonical constant)", got, want)
	}
	// godlike/07 NO-FAKE-AVAILABILITY: the literal value is the
	// exact string "INDEXING_PENDING" (no stringly-typed drift).
	if got, want := meta.IndexingStatus, "INDEXING_PENDING"; got != want {
		t.Errorf("meta.IndexingStatus = %q, want %q (literal)", got, want)
	}
	// godlike/06 SSOT: the constant is the canonical SOLE owner
	// of the literal value (no shadow const anywhere).
	if IndexingStatusPending != "INDEXING_PENDING" {
		t.Errorf("IndexingStatusPending = %q, want %q (drift from canonical)", IndexingStatusPending, "INDEXING_PENDING")
	}
	// Wire-shape: also pin the JSON tag is "indexing_status"
	// (matches the qdrant payload key + the IndexingHandler
	// downstream expectation).
	raw, mErr := json.Marshal(&meta)
	if mErr != nil {
		t.Fatalf("json.Marshal(meta) failed: %v", mErr)
	}
	if !strings.Contains(string(raw), `"indexing_status":"INDEXING_PENDING"`) {
		t.Errorf("JSON wire shape missing indexing_status:INDEXING_PENDING — got %s", string(raw))
	}
}
