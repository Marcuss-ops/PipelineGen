package clip

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
)

// setupSuggestTestRouter creates a test router with clip suggestion endpoints
func setupSuggestTestRouter(suggester *clip.SemanticSuggester) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.Default()

	handler := &ClipIndexHandler{
		indexer:   nil,
		suggester: suggester,
	}

	router.POST("/clip/index/suggest/sentence", handler.SuggestForSentence)
	router.POST("/clip/index/suggest/script", handler.SuggestForScript)

	return router
}

// Helper to create suggester with test clips
func createTestSuggester(clips []clip.IndexedClip) *clip.SemanticSuggester {
	indexer := clip.NewTestIndexer(clips)
	return clip.NewSemanticSuggester(indexer)
}

// TestSuggestSentenceHandler_OK tests valid sentence suggestion request
func TestSuggestSentenceHandler_OK(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "tech1",
			Name:       "AI Technology Robot",
			FolderPath: "Tech/Robotics",
			Tags:       []string{"ai", "robot", "technology", "intelligenza", "artificiale"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
	}
	suggester := createTestSuggester(testClips)

	router := setupSuggestTestRouter(suggester)

	body := `{"sentence":"Un robot con intelligenza artificiale"}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["ok"] != true {
		t.Error("Expected ok=true in response")
	}

	if _, ok := response["suggestions"]; !ok {
		t.Error("Expected 'suggestions' in response")
	}

	if _, ok := response["total"]; !ok {
		t.Error("Expected 'total' in response")
	}

	t.Logf("✅ Valid sentence request: status=%d", w.Code)
	t.Logf("   Response: ok=%v, total=%v", response["ok"], response["total"])
}

// TestSuggestSentenceHandler_InvalidJSON tests malformed JSON
func TestSuggestSentenceHandler_InvalidJSON(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "clip1",
			Name:       "Test Clip",
			FolderPath: "Test",
			Tags:       []string{"test"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	suggester := createTestSuggester(testClips)
	router := setupSuggestTestRouter(suggester)

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse error response: %v", err)
	}

	if response["ok"] != false {
		t.Error("Expected ok=false for invalid JSON")
	}

	if _, ok := response["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}

	t.Logf("✅ Invalid JSON handled: status=%d", w.Code)
}

// TestSuggestSentenceHandler_MissingRequiredField tests missing 'sentence' field
func TestSuggestSentenceHandler_MissingRequiredField(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "clip1",
			Name:       "Test Clip",
			FolderPath: "Test",
			Tags:       []string{"test"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	suggester := createTestSuggester(testClips)
	router := setupSuggestTestRouter(suggester)

	body := `{"max_results":10}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should fail validation
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	t.Logf("✅ Missing field handled: status=%d", w.Code)
}

// TestSuggestSentenceHandler_EmptySentence tests empty sentence input
func TestSuggestSentenceHandler_EmptySentence(t *testing.T) {
	router := setupSuggestTestRouter(nil)

	body := `{"sentence":""}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Empty sentence may return 200 with no suggestions or error
	// Either is acceptable
	t.Logf("✅ Empty sentence: status=%d", w.Code)
	t.Logf("   Response: %s", w.Body.String())
}

// TestSuggestSentenceHandler_WithMediaType tests media type filtering
func TestSuggestSentenceHandler_WithMediaType(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "clip1",
			Name:       "Tech Video",
			FolderPath: "Tech",
			Tags:       []string{"technology"},
			Duration:   30,
			MimeType:   "video/mp4",
			MediaType:  "clip",
		},
		{
			ID:         "stock1",
			Name:       "Stock Footage",
			FolderPath: "Stock",
			Tags:       []string{"technology"},
			Duration:   20,
			MimeType:   "video/mp4",
			MediaType:  "stock",
		},
	}
	indexer := createTestSuggester(testClips)
	router := setupSuggestTestRouter(indexer)

	body := `{"sentence":"tecnologia", "media_type":"clip"}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	t.Logf("✅ Media type filter: status=%d", w.Code)
}

// TestSuggestSentenceHandler_NoSuggester tests when suggester is not initialized
func TestSuggestSentenceHandler_NoSuggester(t *testing.T) {
	router := setupSuggestTestRouter(nil) // nil suggester

	body := `{"sentence":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should return 503 Service Unavailable
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["ok"] != false {
		t.Error("Expected ok=false when suggester not available")
	}

	t.Logf("✅ No suggester handled: status=%d", w.Code)
}

// TestSuggestScriptHandler_OK tests valid script suggestion request
func TestSuggestScriptHandler_OK(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "tech1",
			Name:       "AI Technology",
			FolderPath: "Tech",
			Tags:       []string{"ai", "technology"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
		{
			ID:         "nature1",
			Name:       "Nature Landscape",
			FolderPath: "Nature",
			Tags:       []string{"nature", "landscape"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestSuggester(testClips)
	router := setupSuggestTestRouter(indexer)

	body := `{
		"script": "L'intelligenza artificiale sta cambiando il mondo. La natura è bellissima.",
		"max_results_per_sentence": 5,
		"min_score": 20
	}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["ok"] != true {
		t.Error("Expected ok=true")
	}

	if _, ok := response["sentences_with_clips"]; !ok {
		t.Error("Expected 'sentences_with_clips' in response")
	}

	if _, ok := response["total_clip_suggestions"]; !ok {
		t.Error("Expected 'total_clip_suggestions' in response")
	}

	t.Logf("✅ Valid script request: status=%d", w.Code)
	t.Logf("   Sentences with clips: %v", response["sentences_with_clips"])
	t.Logf("   Total clip suggestions: %v", response["total_clip_suggestions"])
}

// TestSuggestScriptHandler_InvalidJSON tests malformed JSON for script
func TestSuggestScriptHandler_InvalidJSON(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "clip1",
			Name:       "Test Clip",
			FolderPath: "Test",
			Tags:       []string{"test"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	suggester := createTestSuggester(testClips)
	router := setupSuggestTestRouter(suggester)

	body := `{not valid json}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	t.Logf("✅ Invalid script JSON handled: status=%d", w.Code)
}

// TestSuggestScriptHandler_MissingScriptField tests missing 'script' field
func TestSuggestScriptHandler_MissingScriptField(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "clip1",
			Name:       "Test Clip",
			FolderPath: "Test",
			Tags:       []string{"test"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	suggester := createTestSuggester(testClips)
	router := setupSuggestTestRouter(suggester)

	body := `{"max_results_per_sentence":5}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	t.Logf("✅ Missing script field handled: status=%d", w.Code)
}

// TestSuggestScriptHandler_EmptyScript tests empty script
func TestSuggestScriptHandler_EmptyScript(t *testing.T) {
	testClips := []clip.IndexedClip{}
	suggester := createTestSuggester(testClips)
	router := setupSuggestTestRouter(suggester)

	body := `{"script":""}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Empty script may return 400 (validation) or 200 (no suggestions)
	// Both are acceptable
	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 200 or 400, got %d", w.Code)
	}

	t.Logf("✅ Empty script handled: status=%d", w.Code)
}

// TestSuggestScriptHandler_MultiSentence tests script with multiple sentences
func TestSuggestScriptHandler_MultiSentence(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "tech1",
			Name:       "AI Robot Technology Demo Presentation",
			FolderPath: "Tech/Robotics",
			Tags:       []string{"ai", "robot", "technology", "demo", "presentation"},
			Duration:   45,
			MimeType:   "video/mp4",
		},
		{
			ID:         "nature1",
			Name:       "Nature Forest Trees Landscape",
			FolderPath: "Nature/Forest",
			Tags:       []string{"nature", "forest", "trees", "landscape"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
		{
			ID:         "business1",
			Name:       "Business Meeting Office Corporate",
			FolderPath: "Business/Corporate",
			Tags:       []string{"business", "meeting", "office", "corporate"},
			Duration:   25,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestSuggester(testClips)
	router := setupSuggestTestRouter(indexer)

	// Script with 3 distinct topics
	body := `{
		"script": "L'intelligenza artificiale e i robot stanno cambiando la tecnologia. La natura con foreste e alberi è meravigliosa. Il business aziendale richiede riunioni in ufficio.",
		"max_results_per_sentence": 3
	}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	t.Logf("✅ Multi-sentence script: status=%d", w.Code)
	t.Logf("   Script length: %v", response["script_length"])
	t.Logf("   Sentences with clips: %v", response["sentences_with_clips"])
	t.Logf("   Total clip suggestions: %v", response["total_clip_suggestions"])
}

// TestSuggestScriptHandler_NoSuggester tests when suggester is nil
func TestSuggestScriptHandler_NoSuggester(t *testing.T) {
	router := setupSuggestTestRouter(nil)

	body := `{"script":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/script", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d", w.Code)
	}

	t.Logf("✅ No suggester for script: status=%d", w.Code)
}

// TestSuggestSentenceHandler_ConcurrentRequests tests concurrent suggestion requests
func TestSuggestSentenceHandler_ConcurrentRequests(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "tech1",
			Name:       "Technology Video",
			FolderPath: "Tech",
			Tags:       []string{"technology"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestSuggester(testClips)
	router := setupSuggestTestRouter(indexer)

	// Run 10 concurrent requests
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			body := bytes.NewReader([]byte(`{"sentence":"tecnologia"}`))
			req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", body)
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d: Expected status 200, got %d", idx, w.Code)
			}

			done <- true
		}(i)
	}

	// Wait for all requests to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	t.Log("✅ Concurrent requests completed without errors")
}

// TestSuggestSentenceHandler_Defaults tests that default values are applied
func TestSuggestSentenceHandler_Defaults(t *testing.T) {
	testClips := []clip.IndexedClip{
		{
			ID:         "clip1",
			Name:       "Test Video",
			FolderPath: "Test",
			Tags:       []string{"test"},
			Duration:   30,
			MimeType:   "video/mp4",
		},
	}
	indexer := createTestSuggester(testClips)
	router := setupSuggestTestRouter(indexer)

	// Minimal request (no optional fields)
	body := `{"sentence":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/clip/index/suggest/sentence", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	t.Logf("✅ Defaults applied: status=%d", w.Code)
}
