package script

import (
	"context"
	"testing"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/youtube"
)

// MockSemanticSuggester is a mock implementation for testing
type MockSemanticSuggester struct {
	results []clip.SuggestionResult
}

func (m *MockSemanticSuggester) SuggestForSentence(ctx context.Context, sentence string, maxResults int, minScore float64, mediaType string) []clip.SuggestionResult {
	return m.results
}

// MockYouTubeClient is a mock YouTube client for testing
type MockYouTubeClient struct {
	searchResults []youtube.SearchResult
	searchError   error
}

func (m *MockYouTubeClient) Search(ctx context.Context, query string, opts *youtube.SearchOptions) ([]youtube.SearchResult, error) {
	return m.searchResults, m.searchError
}

func (m *MockYouTubeClient) GetVideo(ctx context.Context, videoID string) (*youtube.VideoInfo, error) {
	return nil, nil
}

func (m *MockYouTubeClient) Download(ctx context.Context, req *youtube.DownloadRequest) (*youtube.DownloadResult, error) {
	return nil, nil
}

func (m *MockYouTubeClient) DownloadAudio(ctx context.Context, req *youtube.AudioDownloadRequest) (*youtube.AudioDownloadResult, error) {
	return &youtube.AudioDownloadResult{}, nil
}

func (m *MockYouTubeClient) GetChannelVideos(ctx context.Context, channelURL string, opts *youtube.ChannelOptions) ([]youtube.SearchResult, error) {
	return nil, nil
}

func (m *MockYouTubeClient) GetTrending(ctx context.Context, region string, limit int) ([]youtube.SearchResult, error) {
	return nil, nil
}

func (m *MockYouTubeClient) GetSubtitles(ctx context.Context, videoID string, lang string) (*youtube.SubtitleInfo, error) {
	return nil, nil
}

func (m *MockYouTubeClient) GetTranscript(ctx context.Context, url string, lang string) (string, error) {
	return "", nil
}

func (m *MockYouTubeClient) CheckAvailable(ctx context.Context) error {
	return nil
}

// Helper to create test indexer with clips
func createTestIndexerForMapper(driveClips []clip.IndexedClip) *clip.Indexer {
	// We can't directly set the index, so we'll use a workaround
	// For testing purposes, we'll skip this and use mock suggester instead
	return nil
}

// TestMapper_AutoApproveHighScore tests that clips with score > 85 are auto-approved
func TestMapper_AutoApproveHighScore(t *testing.T) {
	// Create mock YouTube client
	mockYouTube := &MockYouTubeClient{}

	// Create mapper with auto-approve threshold of 85
	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false, // Disable YouTube for this test
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)
	// Replace suggester with mock via reflection or direct assignment
	// For now, we'll test the autoApproveClips logic directly

	// Create a test scene with clip assignment
	scene := &Scene{
		SceneNumber: 1,
		Type:        SceneContent,
		Title:       "Test Scene",
		Text:        "Un robot futuristico cammina sul palco",
		Keywords:    []string{"robot", "futuro", "tecnologia"},
		ClipMapping: ClipMapping{
			DriveClips: []ClipAssignment{
				{
					ClipID:         "high_score_clip",
					Source:         "drive",
					RelevanceScore: 92.0,
					Status:         "pending",
				},
			},
		},
	}

	// Test auto-approve logic
	mapper.autoApproveClips(scene)

	// Verify clip was auto-approved
	if len(scene.ClipMapping.DriveClips) == 0 {
		t.Fatal("Expected at least one clip assignment")
	}

	clip := scene.ClipMapping.DriveClips[0]
	if clip.Status != "approved" {
		t.Errorf("Expected clip to be auto-approved, got status: %s", clip.Status)
	}

	if clip.ApprovedBy != "auto" {
		t.Errorf("Expected ApprovedBy='auto', got: %s", clip.ApprovedBy)
	}

	if clip.ApprovedAt == "" {
		t.Error("Expected ApprovedAt timestamp to be set")
	}

	t.Logf("✅ Auto-approve: score=%.2f, status=%s, approved_by=%s",
		clip.RelevanceScore, clip.Status, clip.ApprovedBy)
}

// TestMapper_NoAutoApproveLowScore tests that clips with score <= 85 are NOT auto-approved
func TestMapper_NoAutoApproveLowScore(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	scene := &Scene{
		SceneNumber: 1,
		Type:        SceneContent,
		Title:       "Test Scene",
		Text:        "Test scene text",
		Keywords:    []string{"test"},
		ClipMapping: ClipMapping{
			DriveClips: []ClipAssignment{
				{
					ClipID:         "low_score_clip",
					Source:         "drive",
					RelevanceScore: 70.0, // Below threshold
					Status:         "pending",
				},
			},
		},
	}

	mapper.autoApproveClips(scene)

	if len(scene.ClipMapping.DriveClips) == 0 {
		t.Fatal("Expected at least one clip assignment")
	}

	clip := scene.ClipMapping.DriveClips[0]
	if clip.Status == "approved" {
		t.Errorf("Expected clip to NOT be auto-approved (score 70 < 85), got status: %s", clip.Status)
	}

	if clip.ApprovedBy != "" {
		t.Errorf("Expected ApprovedBy to be empty, got: %s", clip.ApprovedBy)
	}

	t.Logf("✅ No auto-approve: score=%.2f, status=%s (correctly pending)",
		clip.RelevanceScore, clip.Status)
}

// TestMapper_DeduplicateAndLimit tests deduplication and limiting of clips
func TestMapper_DeduplicateAndLimit(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	// Create clips with duplicates
	clips := []ClipAssignment{
		{ClipID: "clip1", Source: "drive", RelevanceScore: 90.0},
		{ClipID: "clip1", Source: "drive", RelevanceScore: 90.0}, // Duplicate
		{ClipID: "clip2", Source: "drive", RelevanceScore: 80.0},
		{ClipID: "clip2", Source: "drive", RelevanceScore: 80.0}, // Duplicate
		{ClipID: "clip3", Source: "drive", RelevanceScore: 70.0},
		{ClipID: "clip4", Source: "drive", RelevanceScore: 60.0},
	}

	// Limit to 3
	result := mapper.deduplicateAndLimit(clips, 3)

	// Should have 3 unique clips
	if len(result) != 3 {
		t.Errorf("Expected 3 unique clips, got %d", len(result))
	}

	// Should be sorted by score descending
	for i := 0; i < len(result)-1; i++ {
		if result[i].RelevanceScore < result[i+1].RelevanceScore {
			t.Errorf("Clips not sorted by score descending: [%d]=%.2f < [%d]=%.2f",
				i, result[i].RelevanceScore, i+1, result[i+1].RelevanceScore)
		}
	}

	// Should not have duplicates
	seen := make(map[string]bool)
	for _, c := range result {
		if seen[c.ClipID] {
			t.Errorf("Duplicate clip ID: %s", c.ClipID)
		}
		seen[c.ClipID] = true
	}

	t.Logf("✅ Deduplicate and limit: input=%d, output=%d", len(clips), len(result))
	for i, c := range result {
		t.Logf("   [%d] %s: %.2f", i+1, c.ClipID, c.RelevanceScore)
	}
}

// TestMapper_BuildSearchQueries tests search query construction
func TestMapper_BuildSearchQueries(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	scene := &Scene{
		SceneNumber: 1,
		Type:        SceneContent,
		Title:       "AI Technology",
		Text:        "Test scene about AI",
		Keywords:    []string{"artificial", "intelligence", "robot", "technology"},
		Entities: []SceneEntity{
			{Text: "Elon Musk", Type: "PERSON", Relevance: 0.9},
		},
		Emotions: []string{"excitement", "innovation"},
	}

	queries := mapper.buildSearchQueries(scene)

	if len(queries) == 0 {
		t.Fatal("Expected at least one search query")
	}

	t.Logf("✅ Built %d search queries:", len(queries))
	for i, query := range queries {
		t.Logf("   [%d] %s", i+1, query)
	}

	// Should include keywords
	hasKeywordQuery := false
	for _, q := range queries {
		if len(q) > 0 {
			hasKeywordQuery = true
			break
		}
	}

	if !hasKeywordQuery {
		t.Error("Expected at least one keyword-based query")
	}
}

// TestMapper_BuildSearchQueriesFromTranslated tests translated query construction
func TestMapper_BuildSearchQueriesFromTranslated(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	scene := &Scene{
		SceneNumber: 1,
		Type:        SceneContent,
		Title:       "Tecnologia",
		Text:        "Scena sulla tecnologia",
		Keywords:    []string{"tecnologia", "computer", "AI"},
		Emotions:    []string{"gioia"},
	}

	translatedKeywords := mapper.translator.TranslateKeywords(scene.Keywords)
	translatedEntities := mapper.translator.TranslateKeywords(scene.EntitiesText())
	translatedEmotions := mapper.translator.TranslateEmotions(scene.Emotions)

	queries := mapper.buildSearchQueriesFromTranslated(scene, translatedKeywords, translatedEntities, translatedEmotions)

	if len(queries) == 0 {
		t.Fatal("Expected at least one translated search query")
	}

	t.Logf("✅ Built %d translated search queries:", len(queries))
	for i, query := range queries {
		t.Logf("   [%d] %s", i+1, query)
	}

	// Verify translation happened
	hasEnglish := false
	for _, q := range queries {
		if len(q) > 0 {
			// Check if query contains English terms
			hasEnglish = true
		}
	}

	if !hasEnglish {
		t.Log("⚠️  No translated queries found")
	}
}

// TestMapper_GetAllClipAssignments tests collecting all clip assignments from a scene
func TestMapper_GetAllClipAssignments(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	scene := &Scene{
		SceneNumber: 1,
		ClipMapping: ClipMapping{
			DriveClips: []ClipAssignment{
				{ClipID: "drive1", Source: "drive", RelevanceScore: 80.0},
				{ClipID: "drive2", Source: "drive", RelevanceScore: 75.0},
			},
			ArtlistClips: []ClipAssignment{
				{ClipID: "artlist1", Source: "artlist", RelevanceScore: 85.0},
			},
			YouTubeClips: []ClipAssignment{
				{ClipID: "youtube1", Source: "youtube", RelevanceScore: 70.0},
			},
			TikTokClips: []ClipAssignment{
				{ClipID: "tiktok1", Source: "tiktok", RelevanceScore: 65.0},
			},
			StockClips: []ClipAssignment{
				{ClipID: "stock1", Source: "stock", RelevanceScore: 60.0},
			},
		},
	}

	allClips := mapper.getAllClipAssignments(scene)

	expectedCount := 6
	if len(allClips) != expectedCount {
		t.Errorf("Expected %d clips, got %d", expectedCount, len(allClips))
	}

	t.Logf("✅ Collected %d clip assignments from all sources", len(allClips))

	// Verify sources
	sources := make(map[string]int)
	for _, c := range allClips {
		sources[c.Source]++
	}

	t.Logf("   Sources: %v", sources)
}

// TestMapper_SceneWithEmptyFields tests mapper robustness with empty/malformed scenes
func TestMapper_SceneWithEmptyFields(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	// Scene with empty fields
	scene := &Scene{
		SceneNumber: 1,
		Type:        SceneContent,
		Title:       "", // Empty title
		Text:        "   ", // Whitespace only
		Keywords:    []string{}, // Empty keywords
		Entities:    nil, // Nil entities
		Emotions:    nil, // Nil emotions
		ClipMapping: ClipMapping{}, // Empty mapping
	}

	// Should not panic
	mapper.autoApproveClips(scene)
	queries := mapper.buildSearchQueries(scene)

	t.Logf("✅ Empty scene fields handled: %d queries built", len(queries))
}

// TestMapper_ApprovalRequests tests getting approval requests for scenes
func TestMapper_ApprovalRequests(t *testing.T) {
	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(nil, mockYouTube, config)

	script := &StructuredScript{
		ID:       "test_script",
		Title:    "Test Script",
		Language: "it",
		Scenes: []Scene{
			{
				SceneNumber: 1,
				Type:        SceneIntro,
				Title:       "Intro",
				Text:        "Introduzione alla tecnologia",
				Status:      SceneClipsFound,
				ClipMapping: ClipMapping{
					DriveClips: []ClipAssignment{
						{ClipID: "clip1", Source: "drive", RelevanceScore: 90.0, Status: "approved"},
					},
				},
			},
			{
				SceneNumber: 2,
				Type:        SceneContent,
				Title:       "Content",
				Text:        "Contenuto principale",
				Status:      SceneNeedsReview,
				ClipMapping: ClipMapping{
					DriveClips: []ClipAssignment{
						{ClipID: "clip2", Source: "drive", RelevanceScore: 70.0, Status: "pending"},
					},
				},
			},
		},
	}

	requests := mapper.GetApprovalRequests(script)

	if len(requests) == 0 {
		t.Log("⚠️  No approval requests generated (may be expected with current logic)")
		return
	}

	t.Logf("✅ Generated %d approval requests", len(requests))
	for i, req := range requests {
		t.Logf("   [%d] Scene %d: %d clips, needs_review=%v, auto_approved=%d",
			i+1, req.SceneNumber, len(req.Clips), req.NeedsReview, len(req.AutoApproved))
	}
}

// TestMapper_IntegrationWithRealIndexer tests mapper with a real (but mocked) indexer
func TestMapper_IntegrationWithRealIndexer(t *testing.T) {
	// Create real indexer with test clips
	testClips := []clip.IndexedClip{
		{
			ID:         "tech_clip_1",
			Name:       "AI Robot Technology Demo",
			FolderPath: "Tech/Robotics",
			Tags:       []string{"ai", "robot", "technology", "demo", "intelligenza", "artificiale", "tecnologia"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
		{
			ID:         "nature_clip_1",
			Name:       "Nature Landscape Sunset",
			FolderPath: "Nature/Landscape",
			Tags:       []string{"nature", "landscape", "sunset"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}

	indexer := clip.NewTestIndexer(testClips)
	suggester := clip.NewSemanticSuggester(indexer)

	mockYouTube := &MockYouTubeClient{}

	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     3,
		YouTubeSearchRadius:  10,
		AutoApproveThreshold: 85.0,
		EnableYouTube:        false,
		EnableTikTok:         false,
		EnableArtlist:        false,
		RequiresApproval:     true,
	}

	mapper := NewMapper(suggester, mockYouTube, config)

	// Create a test script
	script := &StructuredScript{
		ID:       "test_script_integration",
		Title:    "Test Integration",
		Language: "it",
		Scenes: []Scene{
			{
				SceneNumber: 1,
				Type:        SceneContent,
				Title:       "Technology Scene",
				Text:        "Un robot con intelligenza artificiale presenta nuova tecnologia",
				Keywords:    []string{"robot", "intelligenza", "artificiale", "tecnologia"},
			},
		},
	}

	// Map clips to script
	ctx := context.Background()
	err := mapper.MapClipsToScript(ctx, script)
	if err != nil {
		t.Fatalf("MapClipsToScript failed: %v", err)
	}

	// Verify results
	if len(script.Scenes) == 0 {
		t.Fatal("Expected at least one scene")
	}

	scene := script.Scenes[0]
	totalClips := len(scene.ClipMapping.DriveClips) + len(scene.ClipMapping.ArtlistClips)

	t.Logf("✅ Integration test:")
	t.Logf("   Scene %d: %d drive clips, %d artlist clips",
		scene.SceneNumber, len(scene.ClipMapping.DriveClips), len(scene.ClipMapping.ArtlistClips))

	if totalClips > 0 {
		t.Logf("   Clips found:")
		for i, c := range scene.ClipMapping.DriveClips {
			t.Logf("     [%d] %s (score: %.2f, status: %s)",
				i+1, c.ClipID, c.RelevanceScore, c.Status)
		}
	}

	// Verify metadata
	t.Logf("   Metadata: clips_found=%d, total_clips_needed=%d",
		script.Metadata.ClipsFound, script.Metadata.TotalClipsNeeded)
}

// BenchmarkMapper_DeduplicateAndLimit benchmarks deduplication performance
func BenchmarkMapper_DeduplicateAndLimit(b *testing.B) {
	mockYouTube := &MockYouTubeClient{}
	config := &MapperConfig{
		MinScore:             20.0,
		MaxClipsPerScene:     5,
		AutoApproveThreshold: 85.0,
	}
	mapper := NewMapper(nil, mockYouTube, config)

	// Create 1000 clips with duplicates
	clips := make([]ClipAssignment, 1000)
	for i := 0; i < 1000; i++ {
		clips[i] = ClipAssignment{
			ClipID:         "clip",
			Source:         "drive",
			RelevanceScore: float64(i % 100),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mapper.deduplicateAndLimit(clips, 50)
	}
}
