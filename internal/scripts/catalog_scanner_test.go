package scripts

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
)

// ── Mock vector store for testing hybrid search ─────────────────────────

// mockQdrantStore implements vectorstore.Store with configurable HybridSearch results.
type mockQdrantStore struct {
	hybridSearchResults []vectorstore.SearchResult
	hybridSearchErr     error
}

func (m *mockQdrantStore) EnsureCollection(ctx context.Context) error { return nil }
func (m *mockQdrantStore) UpsertAsset(ctx context.Context, a vectorstore.VectorAsset) error {
	return nil
}
func (m *mockQdrantStore) UpsertAssets(ctx context.Context, a []vectorstore.VectorAsset) error {
	return nil
}
func (m *mockQdrantStore) Search(ctx context.Context, req vectorstore.SearchRequest) ([]vectorstore.SearchResult, error) {
	return m.hybridSearchResults, m.hybridSearchErr
}
func (m *mockQdrantStore) DeleteAsset(ctx context.Context, id string) error { return nil }
func (m *mockQdrantStore) Health(ctx context.Context) error                 { return nil }
func (m *mockQdrantStore) CollectionInfo(ctx context.Context) (*vectorstore.CollectionInfo, error) {
	return &vectorstore.CollectionInfo{PointsCount: 0}, nil
}
func (m *mockQdrantStore) Close() error { return nil }
func (m *mockQdrantStore) HybridSearch(ctx context.Context, req vectorstore.HybridSearchRequest) ([]vectorstore.SearchResult, error) {
	return m.hybridSearchResults, m.hybridSearchErr
}
func (m *mockQdrantStore) IndexHealth(ctx context.Context) (*vectorstore.IndexHealthReport, error) {
	return &vectorstore.IndexHealthReport{OK: true}, nil
}
func (m *mockQdrantStore) CleanupStalePoints(ctx context.Context, fn func(assetID, driveFileID, driveLink string) (bool, error)) (int, error) {
	return 0, nil
}

// DeletePoints satisfies vectorstore.Store (PR3-5b batch-delete). Stub: the
// test scope never asserts on DeletePoints behaviour, only on the build
// succeeding.
func (m *mockQdrantStore) DeletePoints(ctx context.Context, assetIDs []string) error {
	return nil
}

// ListPointIDs satisfies vectorstore.Store (PR3-5b cross-check sampling).
// Stub: the test scope never inspects the returned slice.
func (m *mockQdrantStore) ListPointIDs(ctx context.Context, limit int) ([]string, error) {
	return nil, nil
}

// ScrollAssetIDsPage satisfies vectorstore.Store (ghost sweeper, internal/app).
// Stub: calls fn with a single empty batch and returns nil.
func (m *mockQdrantStore) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error {
	if fn != nil {
		_ = fn(nil)
	}
	return nil
}

// ── Test helpers ─────────────────────────────────────────────────────────

// insertTestClipFrom is a helper to insert a test clip into the DB.
func insertTestClipFrom(t *testing.T, repo *sqlite.ClipsRepository, clip *assets.Asset) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertClip(ctx, clip); err != nil {
		t.Fatalf("failed to insert test clip %q: %v", clip.ID, err)
	}
}

// makeClip creates a MediaAsset with a transcript, suitable for catalog search.
func makeClip(id, name, transcript, summary, topics string) *assets.Asset {
	clip := &assets.Asset{
		ID:             id,
		Name:           name,
		Source:         "youtube",
		Duration:       120 * time.Second,
		Tags:           []string{},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if transcript != "" {
		clip.SetMetadataString("clean_transcript", transcript)
	}
	if summary != "" {
		clip.SetMetadataString("clip_summary", summary)
	}
	if topics != "" {
		clip.SetMetadataString("topics", topics)
	}
	return clip
}

// newCatalogTestBuilder creates a CatalogScanner test fixture with an in-memory DB.
// Returns the builder, repository, and a cleanup function.
func newCatalogTestBuilder(t *testing.T) (*ClipSourceBuilder, *sqlite.ClipsRepository) {
	t.Helper()
	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())
	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	return builder, repo
}

// ── Tests ───────────────────────────────────────────────────────────────

func TestSelectClipsForTopic_LIKEOnly(t *testing.T) {
	ctx := context.Background()
	builder, repo := newCatalogTestBuilder(t)

	// Insert clips with names matching "Pompeii"
	insertTestClipFrom(t, repo, makeClip("c1", "Pompeii Eruption",
		"Mount Vesuvius erupted in 79 AD. The city of Pompeii was buried under ash.",
		"The eruption that destroyed Pompeii.", `["volcano","ancient rome"]`))
	insertTestClipFrom(t, repo, makeClip("c2", "Roman Architecture",
		"Roman architects developed revolutionary techniques.",
		"Roman architectural innovations.", `["architecture","engineering"]`))
	insertTestClipFrom(t, repo, makeClip("c3", "Pompeii Daily Life",
		"Daily life in Pompeii before the eruption.",
		"Life in the shadow of Vesuvius.", `["daily life","pompeii"]`))

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Pompeii", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// Should find clips matching "Pompeii" — c1 and c3
	if len(clipIDs) < 2 {
		t.Fatalf("got %d clip IDs, expected at least 2 (Pompeii matches)", len(clipIDs))
	}

	if report.TotalClipsFound < 2 {
		t.Errorf("TotalClipsFound = %d, expected at least 2", report.TotalClipsFound)
	}
	if report.CoverageScore <= 0 {
		t.Errorf("CoverageScore = %f, expected > 0", report.CoverageScore)
	}
}

func TestSelectClipsForTopic_QdrantOnly(t *testing.T) {
	ctx := context.Background()

	// Insert clips into DB (needed for searchViaQdrant's GetClip calls)
	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	// Insert a clip that Qdrant will return
	insertTestClipFrom(t, repo, makeClip("qdrant-clip", "Hidden Gem",
		"This clip has no text match for the topic.",
		"A clip only discoverable via vector search.", `["hidden","semantic"]`))

	// Qdrant mock returns the clip
	qdrantResults := []vectorstore.SearchResult{
		{AssetID: "qdrant-clip", Score: 0.85, Name: "Hidden Gem"},
	}

	mock := &mockQdrantStore{
		hybridSearchResults: qdrantResults,
	}
	vecCfg := vectorstore.Config{
		URL:              "http://mock:6333",
		Collection:       "test",
		SparseVectorName: "bm25_text",
	}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())

	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	builder.SetVectorStore(svc)

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "some obscure topic with no like match", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	if len(clipIDs) != 1 {
		t.Fatalf("got %d clip IDs, want 1 (from Qdrant only)", len(clipIDs))
	}
	if clipIDs[0] != "qdrant-clip" {
		t.Errorf("clipIDs[0] = %q, want %q", clipIDs[0], "qdrant-clip")
	}
	if report.TotalClipsFound != 1 {
		t.Errorf("TotalClipsFound = %d, want 1", report.TotalClipsFound)
	}
}

func TestSelectClipsForTopic_MergeWithDedup(t *testing.T) {
	ctx := context.Background()

	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	// Insert 3 clips — "Pompeii" and "Rome-arch" match LIKE for "Pompeii",
	// while "third-clip" does not.
	insertTestClipFrom(t, repo, makeClip("pompeii-1", "Pompeii Eruption",
		"Mount Vesuvius erupted.", "The eruption.", `["volcano"]`))
	insertTestClipFrom(t, repo, makeClip("pompeii-2", "Pompeii Ruins",
		"The ruins of Pompeii.", "Exploring ruins.", `["archaeology"]`))

	// Qdrant also returns pompeii-1 (overlap) and a unique clip
	qdrantResults := []vectorstore.SearchResult{
		{AssetID: "pompeii-1", Score: 0.92},
		{AssetID: "qdrant-only-clip", Score: 0.88},
	}

	// Insert the Qdrant-only clip so GetClip succeeds
	insertTestClipFrom(t, repo, makeClip("qdrant-only-clip", "Secret Find",
		"This clip has no direct text match.",
		"Found only by Qdrant.", `["secret"]`))

	mock := &mockQdrantStore{hybridSearchResults: qdrantResults}
	vecCfg := vectorstore.Config{URL: "http://mock:6333", Collection: "test", SparseVectorName: "bm25_text"}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())

	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	builder.SetVectorStore(svc)

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Pompeii", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// Expected: pompeii-1 (LIKE), pompeii-2 (LIKE), qdrant-only-clip (Qdrant unique) = 3
	// pompeii-1 appears in both but should be deduplicated
	if len(clipIDs) != 3 {
		t.Fatalf("got %d clip IDs, want 3 (2 LIKE + 1 Qdrant unique)", len(clipIDs))
	}

	if report.TotalClipsFound != 3 {
		t.Errorf("TotalClipsFound = %d, want 3", report.TotalClipsFound)
	}
	if report.ClipsSelected != 3 {
		t.Errorf("ClipsSelected = %d, want 3", report.ClipsSelected)
	}

	// Verify dedup: pompeii-1 should appear exactly once
	count := 0
	for _, id := range clipIDs {
		if id == "pompeii-1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("pompeii-1 appears %d times, want 1 (dedup failed)", count)
	}
}

func TestSelectClipsForTopic_MergeQdrantAddsNewClips(t *testing.T) {
	ctx := context.Background()

	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	// LIKE finds one clip
	insertTestClipFrom(t, repo, makeClip("like-only", "Pompeii History",
		"History of Pompeii.", "Pompeii overview.", `["history"]`))

	// Qdrant finds 2 clips — one overlap, one new
	insertTestClipFrom(t, repo, makeClip("qdrant-new", "Incredible Discovery",
		"A recent discovery in Pompeii.",
		"New findings.", `["discovery"]`))

	qdrantResults := []vectorstore.SearchResult{
		{AssetID: "like-only", Score: 0.85},
		{AssetID: "qdrant-new", Score: 0.78},
	}

	mock := &mockQdrantStore{hybridSearchResults: qdrantResults}
	vecCfg := vectorstore.Config{URL: "http://mock:6333", Collection: "test", SparseVectorName: "bm25_text"}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())

	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	builder.SetVectorStore(svc)

	clipIDs, _, err := builder.SelectClipsForTopic(ctx, "Pompeii", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	if len(clipIDs) != 2 {
		t.Fatalf("got %d clip IDs, want 2 (1 LIKE + 1 Qdrant unique)", len(clipIDs))
	}

	// Verify both expected IDs are present
	found := make(map[string]bool)
	for _, id := range clipIDs {
		found[id] = true
	}
	if !found["like-only"] {
		t.Error("missing 'like-only' in results")
	}
	if !found["qdrant-new"] {
		t.Error("missing 'qdrant-new' in results")
	}
}

func TestSelectClipsForTopic_LIKEFailsQdrantFallback(t *testing.T) {
	ctx := context.Background()

	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	// Insert clip only found by Qdrant
	insertTestClipFrom(t, repo, makeClip("q-clip", "Qdrant Result",
		"This clip is found by Qdrant.", "Qdrant fallback.", `[]`))

	qdrantResults := []vectorstore.SearchResult{
		{AssetID: "q-clip", Score: 0.75},
	}

	mock := &mockQdrantStore{hybridSearchResults: qdrantResults}
	vecCfg := vectorstore.Config{URL: "http://mock:6333", Collection: "test", SparseVectorName: "bm25_text"}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())

	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	builder.SetVectorStore(svc)

	// Search for something that won't match LIKE at all
	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "zzz_nonexistent_topic_zzz", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	if len(clipIDs) != 1 {
		t.Fatalf("got %d clip IDs, want 1 (Qdrant fallback)", len(clipIDs))
	}
	if clipIDs[0] != "q-clip" {
		t.Errorf("clipIDs[0] = %q, want %q", clipIDs[0], "q-clip")
	}
	if report.TotalClipsFound != 1 {
		t.Errorf("TotalClipsFound = %d, want 1", report.TotalClipsFound)
	}
}

func TestSelectClipsForTopic_QdrantFailsLIKEOnly(t *testing.T) {
	ctx := context.Background()
	builder, repo := newCatalogTestBuilder(t)

	// Insert LIKE-matching clips
	insertTestClipFrom(t, repo, makeClip("like-1", "Pompeii Artifacts",
		"Artifacts found in Pompeii.", "Pompeii artifacts.", `["artifacts"]`))

	// Set vectorSvc to a store that returns an error
	mock := &mockQdrantStore{
		hybridSearchResults: nil,
		hybridSearchErr:     context.DeadlineExceeded,
	}
	vecCfg := vectorstore.Config{URL: "http://mock:6333", Collection: "test", SparseVectorName: "bm25_text"}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())
	builder.SetVectorStore(svc)

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Pompeii", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// Should still get LIKE results even though Qdrant failed
	if len(clipIDs) != 1 {
		t.Fatalf("got %d clip IDs, want 1 (LIKE only, Qdrant failed)", len(clipIDs))
	}
	if clipIDs[0] != "like-1" {
		t.Errorf("clipIDs[0] = %q, want %q", clipIDs[0], "like-1")
	}
	if report.TotalClipsFound != 1 {
		t.Errorf("TotalClipsFound = %d, want 1", report.TotalClipsFound)
	}
}

func TestSelectClipsForTopic_NoClips(t *testing.T) {
	ctx := context.Background()
	builder, _ := newCatalogTestBuilder(t) // Empty DB

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Nonexistent Topic", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	if clipIDs != nil {
		t.Errorf("clipIDs = %v, want nil", clipIDs)
	}
	if report == nil {
		t.Fatal("report is nil, expected a report with zero counts")
	}
	if report.TotalClipsFound != 0 {
		t.Errorf("TotalClipsFound = %d, want 0", report.TotalClipsFound)
	}
	if report.ClipsSelected != 0 {
		t.Errorf("ClipsSelected = %d, want 0", report.ClipsSelected)
	}
	if report.CoverageScore != 0 {
		t.Errorf("CoverageScore = %f, want 0", report.CoverageScore)
	}
}

func TestSelectClipsForTopic_SmallSetSkipsLLM(t *testing.T) {
	ctx := context.Background()
	builder, repo := newCatalogTestBuilder(t)

	// Insert ≤ maxClips clips — should skip LLM clustering
	insertTestClipFrom(t, repo, makeClip("a1", "Alpha Topic",
		"Transcript for alpha topic.", "Alpha summary.", `["alpha"]`))
	insertTestClipFrom(t, repo, makeClip("a2", "Alpha Details",
		"More details about alpha.", "Beta summary.", `["alpha"]`))

	// Use maxClips=2 so the small-set path is taken
	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Alpha", 2)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	if len(clipIDs) != 2 {
		t.Fatalf("got %d clip IDs, want 2 (small set, no LLM clustering)", len(clipIDs))
	}
	if report.ClustersFound != 1 {
		t.Errorf("ClustersFound = %d, want 1 (auto-cluster for small set)", report.ClustersFound)
	}
	if report.CoverageScore != 1.0 {
		t.Errorf("CoverageScore = %f, want 1.0", report.CoverageScore)
	}
	if len(report.Clusters) != 1 {
		t.Fatalf("len(Clusters) = %d, want 1", len(report.Clusters))
	}
	if report.Clusters[0].Coverage != "sufficient" {
		t.Errorf("Cluster[0].Coverage = %q, want %q", report.Clusters[0].Coverage, "sufficient")
	}
}

func TestSelectClipsForTopic_EmptyTopic(t *testing.T) {
	ctx := context.Background()
	builder, _ := newCatalogTestBuilder(t)

	_, _, err := builder.SelectClipsForTopic(ctx, "", 10)
	if err == nil {
		t.Fatal("expected error for empty topic, got nil")
	}
	if err.Error() != "topic is required" {
		t.Errorf("error = %q, want %q", err.Error(), "topic is required")
	}
}

func TestSelectClipsForTopic_ZeroMaxClipsDefaults(t *testing.T) {
	ctx := context.Background()
	builder, repo := newCatalogTestBuilder(t)

	// Insert 5 clips — fewer than default maxClips (10), so small-set path is taken
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("bulk-%d", i)
		insertTestClipFrom(t, repo, makeClip(id, "Bulk Clip "+id,
			"This is a bulk clip with enough transcript.",
			"Bulk summary.", `["bulk"]`))
	}

	// maxClips=0 should default to 10, and with 5 <= 10 we take the small-set path
	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Bulk", 0)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	if len(clipIDs) != 5 {
		t.Errorf("got %d clip IDs, want 5 (all clips, maxClips defaulted to 10)", len(clipIDs))
	}
	if report.ClustersFound != 1 {
		t.Errorf("ClustersFound = %d, want 1 (small-set path, no LLM)", report.ClustersFound)
	}
}

func TestSelectClipsForTopic_FiltersClipsWithoutTranscript(t *testing.T) {
	ctx := context.Background()
	builder, repo := newCatalogTestBuilder(t)

	// Clip without transcript — should be filtered out by toSearchSummary
	noTranscript := &assets.Asset{
		ID:             "no-transcript-tag",
		Name:           "No Transcript Pompeii",
		Source:         "youtube",
		Duration:       120 * time.Second,
		Tags:           []string{},
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	insertTestClipFrom(t, repo, noTranscript)

	// Clip WITH transcript — should be included
	insertTestClipFrom(t, repo, makeClip("with-transcript", "Pompeii With Transcript",
		"This clip has a usable transcript.", "Summary.", `[]`))

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Pompeii", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// Only the clip with transcript should be in results
	if len(clipIDs) != 1 {
		t.Fatalf("got %d clip IDs, want 1 (clip without transcript filtered out)", len(clipIDs))
	}
	if clipIDs[0] != "with-transcript" {
		t.Errorf("clipIDs[0] = %q, want %q", clipIDs[0], "with-transcript")
	}
	if report.TotalClipsFound != 1 {
		t.Errorf("TotalClipsFound = %d, want 1", report.TotalClipsFound)
	}
}

func TestSelectClipsForTopic_DedupWithQdrantAndLIKE(t *testing.T) {
	ctx := context.Background()

	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	// LIKE finds 2 clips matching "Pompeii"
	insertTestClipFrom(t, repo, makeClip("dup-1", "Pompeii Overview",
		"Overview of Pompeii.", "Overview.", `["pompeii"]`))
	insertTestClipFrom(t, repo, makeClip("dup-2", "Pompeii Videos",
		"Various videos about Pompeii.", "Videos.", `["pompeii"]`))

	// Qdrant also returns dup-1 (overlap) and dup-2 (overlap) — both already in LIKE
	qdrantResults := []vectorstore.SearchResult{
		{AssetID: "dup-1", Score: 0.90},
		{AssetID: "dup-2", Score: 0.85},
	}

	mock := &mockQdrantStore{hybridSearchResults: qdrantResults}
	vecCfg := vectorstore.Config{URL: "http://mock:6333", Collection: "test", SparseVectorName: "bm25_text"}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())

	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	builder.SetVectorStore(svc)

	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "Pompeii", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// Should have exactly 2 clips (both dup-1 and dup-2 found by LIKE, Qdrant adds nothing new)
	if len(clipIDs) != 2 {
		t.Fatalf("got %d clip IDs, want 2 (all Qdrant results were already in LIKE)", len(clipIDs))
	}
	if report.TotalClipsFound != 2 {
		t.Errorf("TotalClipsFound = %d, want 2", report.TotalClipsFound)
	}
}

func TestSelectClipsForTopic_QdrantGetClipNotFound(t *testing.T) {
	ctx := context.Background()

	db := drive.NewTestDBWithSchema(t, clipSourceTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewClipsRepository(db, zap.NewNop())

	// Insert one clip in DB
	insertTestClipFrom(t, repo, makeClip("exists", "Existing Clip",
		"This clip exists in DB.", "Exists.", `[]`))

	// Qdrant returns one clip that exists and one that doesn't
	qdrantResults := []vectorstore.SearchResult{
		{AssetID: "exists", Score: 0.90},
		{AssetID: "does-not-exist", Score: 0.80},
	}

	mock := &mockQdrantStore{hybridSearchResults: qdrantResults}
	vecCfg := vectorstore.Config{URL: "http://mock:6333", Collection: "test", SparseVectorName: "bm25_text"}
	svc := vectorstore.NewService(mock, vecCfg, zap.NewNop())

	builder := NewClipSourceBuilder(repo, nil, zap.NewNop())
	builder.SetVectorStore(svc)

	clipIDs, _, err := builder.SelectClipsForTopic(ctx, "exists", 10)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// "does-not-exist" should be skipped (GetClip returns nil)
	if len(clipIDs) != 1 {
		t.Fatalf("got %d clip IDs, want 1 (missing clip should be skipped)", len(clipIDs))
	}
	if clipIDs[0] != "exists" {
		t.Errorf("clipIDs[0] = %q, want %q", clipIDs[0], "exists")
	}
}

func TestSelectClipsForTopic_LargeSetUsesLLM(t *testing.T) {
	ctx := context.Background()
	builder, repo := newCatalogTestBuilder(t)

	// Insert > maxClips clips (more than 3) matching a single topic
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("llm-clip-%d", i)
		insertTestClipFrom(t, repo, makeClip(id, "LLM Test Clip "+id,
			"This is a clip for LLM clustering test with enough transcript content to be considered usable.",
			"LLM test summary for "+id, `["llm-test"]`))
	}

	// maxClips=3 means we have 5 clips > 3 → triggers LLM clustering path
	// Since we have no ollamaCli, the LLM call will fail and fallback to using all clips
	clipIDs, report, err := builder.SelectClipsForTopic(ctx, "LLM", 3)
	if err != nil {
		t.Fatalf("SelectClipsForTopic failed: %v", err)
	}

	// LLM clustering failed (no ollamaCli), so fallback should return all 5 clips
	if len(clipIDs) < 3 {
		t.Fatalf("got %d clip IDs, expected at least 3 (fallback uses all clips)", len(clipIDs))
	}
	if len(report.Warnings) == 0 {
		t.Error("expected Warnings about LLM clustering fallback, got none")
	}
}

// ── Test: toSearchSummary filters clips without transcripts correctly ───

func TestToSearchSummary_FiltersNoTranscript(t *testing.T) {
	builder, repo := newCatalogTestBuilder(t)
	_ = repo // not needed for this test

	// Clip without transcript → nil
	noTranscript := &assets.Asset{
		ID:   "no-tx",
		Name: "No Transcript",
	}
	result := builder.toSearchSummary(noTranscript)
	if result != nil {
		t.Errorf("expected nil for clip without transcript, got %+v", result)
	}

	// Clip with transcript → non-nil summary
	withTranscript := &assets.Asset{
		ID:   "has-tx",
		Name: "Has Transcript",
	}
	withTranscript.SetMetadataString("clean_transcript", "This clip has a transcript.")
	result = builder.toSearchSummary(withTranscript)
	if result == nil {
		t.Fatal("expected non-nil summary for clip with transcript")
	}
	if result.ID != "has-tx" {
		t.Errorf("result.ID = %q, want %q", result.ID, "has-tx")
	}
}

func (m *mockQdrantStore) OperationCollectionInfo(ctx context.Context) (*vectorstore.CollectionInfo, error) {
	return &vectorstore.CollectionInfo{PointsCount: 0}, nil
}

func (m *mockQdrantStore) PhysicalCollectionInfo(ctx context.Context) (*vectorstore.CollectionInfo, error) {
	return &vectorstore.CollectionInfo{PointsCount: 0}, nil
}
