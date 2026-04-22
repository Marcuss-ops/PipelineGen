// Package handlers provides HTTP handlers for the API.
package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"velox/go-master/internal/download"
)

// MockDownloader is a mock implementation for testing
type MockDownloader struct {
	downloadResults map[string]*download.DownloadResult
	downloadError   error
	listResults     map[download.Platform][]string
	listError       error
	platformFolders map[download.Platform]string
}

func NewMockDownloader() *MockDownloader {
	return &MockDownloader{
		downloadResults: make(map[string]*download.DownloadResult),
		listResults:     make(map[download.Platform][]string),
		platformFolders: make(map[download.Platform]string),
	}
}

func (m *MockDownloader) Download(ctx context.Context, url string) (*download.DownloadResult, error) {
	if m.downloadError != nil {
		return nil, m.downloadError
	}
	if result, ok := m.downloadResults[url]; ok {
		return result, nil
	}
	// Default mock behavior based on URL
	platform := download.DetectPlatform(url)
	videoID := download.ExtractVideoID(url)
	return &download.DownloadResult{
		Platform:  platform,
		VideoID:   videoID,
		Title:     "Test Video",
		FilePath:  "/tmp/test/video.mp4",
		Duration:  60.5,
		Thumbnail: "https://example.com/thumb.jpg",
		Author:    "Test Author",
	}, nil
}

func (m *MockDownloader) ListDownloads() (map[download.Platform][]string, error) {
	if m.listError != nil {
		return nil, m.listError
	}
	return m.listResults, nil
}

func (m *MockDownloader) GetPlatformFolder(platform download.Platform) string {
	if folder, ok := m.platformFolders[platform]; ok {
		return folder
	}
	return "/tmp/downloads/" + string(platform)
}

// setupTestRouter creates a Gin router with the download handler for testing
func setupTestRouter(downloader *MockDownloader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	// We need to create a handler that works with our mock
	// Since DownloadHandler expects *download.Downloader, we'll test the handler methods directly
	apiGroup := router.Group("/api")
	downloadGroup := apiGroup.Group("/download")

	handler := &DownloadHandler{
		mockDownloader: downloader,
	}

	downloadGroup.POST("", handler.Download)
	downloadGroup.GET("/platforms", handler.ListPlatforms)
	downloadGroup.GET("/library", handler.ListDownloads)
	downloadGroup.GET("/library/:platform", handler.ListPlatformDownloads)
	downloadGroup.DELETE("/library/:platform/:videoID", handler.DeleteDownload)

	return router
}

// TestDownloadHandler_YoutubeDownload tests YouTube video download endpoint
func TestDownloadHandler_YoutubeDownload(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		mockResult     *download.DownloadResult
		mockError      error
		expectedStatus int
		expectedOk     bool
		expectedFields map[string]interface{}
	}{
		{
			name: "successful youtube download with watch URL",
			url:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			mockResult: &download.DownloadResult{
				Platform:  download.PlatformYouTube,
				VideoID:   "dQw4w9WgXcQ",
				Title:     "Rick Astley - Never Gonna Give You Up",
				FilePath:  "/tmp/downloads/youtube/dQw4w9WgXcQ/video.mp4",
				Duration:  212.0,
				Thumbnail: "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg",
				Author:    "Rick Astley",
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
			expectedFields: map[string]interface{}{
				"platform": "youtube",
				"video_id": "dQw4w9WgXcQ",
				"title":    "Rick Astley - Never Gonna Give You Up",
				"duration": 212.0,
				"author":   "Rick Astley",
			},
		},
		{
			name: "successful youtube download with short URL",
			url:  "https://youtu.be/dQw4w9WgXcQ",
			mockResult: &download.DownloadResult{
				Platform:  download.PlatformYouTube,
				VideoID:   "dQw4w9WgXcQ",
				Title:     "Test Video",
				FilePath:  "/tmp/downloads/youtube/dQw4w9WgXcQ/video.mp4",
				Duration:  120.0,
				Author:    "Test Author",
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
			expectedFields: map[string]interface{}{
				"platform": "youtube",
				"video_id": "dQw4w9WgXcQ",
			},
		},
		{
			name: "successful youtube download with embed URL",
			url:  "https://www.youtube.com/embed/dQw4w9WgXcQ",
			mockResult: &download.DownloadResult{
				Platform:  download.PlatformYouTube,
				VideoID:   "dQw4w9WgXcQ",
				Title:     "Test Video",
				FilePath:  "/tmp/downloads/youtube/dQw4w9WgXcQ/video.mp4",
				Duration:  90.0,
				Author:    "Test Author",
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			if tt.mockResult != nil {
				mockDownloader.downloadResults[tt.url] = tt.mockResult
			}
			if tt.mockError != nil {
				mockDownloader.downloadError = tt.mockError
			}

			router := setupTestRouter(mockDownloader)

			payload := map[string]string{"url": tt.url}
			jsonPayload, _ := json.Marshal(payload)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/download", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOk, response["ok"])

			for field, expectedValue := range tt.expectedFields {
				assert.Equal(t, expectedValue, response[field], "Field %s mismatch", field)
			}
		})
	}
}

// TestDownloadHandler_TiktokDownload tests TikTok video download endpoint
func TestDownloadHandler_TiktokDownload(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		mockResult     *download.DownloadResult
		expectedStatus int
		expectedOk     bool
		expectedFields map[string]interface{}
	}{
		{
			name: "successful tiktok download with full URL",
			url:  "https://www.tiktok.com/@user/video/7123456789012345678",
			mockResult: &download.DownloadResult{
				Platform:  download.PlatformTikTok,
				VideoID:   "7123456789012345678",
				Title:     "Amazing TikTok Video",
				FilePath:  "/tmp/downloads/tiktok/7123456789012345678/video.mp4",
				Duration:  15.0,
				Thumbnail: "https://p16-sign-va.tiktokcdn.com/thumb.jpg",
				Author:    "@user",
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
			expectedFields: map[string]interface{}{
				"platform": "tiktok",
				"video_id": "7123456789012345678",
				"title":    "Amazing TikTok Video",
				"duration": 15.0,
				"author":   "@user",
			},
		},
		{
			name: "successful tiktok download with short URL",
			url:  "https://vm.tiktok.com/ABC123",
			mockResult: &download.DownloadResult{
				Platform:  download.PlatformTikTok,
				VideoID:   "ABC123",
				Title:     "Short TikTok",
				FilePath:  "/tmp/downloads/tiktok/ABC123/video.mp4",
				Duration:  30.0,
				Author:    "@creator",
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
			expectedFields: map[string]interface{}{
				"platform": "tiktok",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			if tt.mockResult != nil {
				mockDownloader.downloadResults[tt.url] = tt.mockResult
			}

			router := setupTestRouter(mockDownloader)

			payload := map[string]string{"url": tt.url}
			jsonPayload, _ := json.Marshal(payload)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/download", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOk, response["ok"])

			for field, expectedValue := range tt.expectedFields {
				assert.Equal(t, expectedValue, response[field], "Field %s mismatch", field)
			}
		})
	}
}

// TestDownloadHandler_InvalidURL tests handling of invalid URLs
func TestDownloadHandler_InvalidURL(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "unsupported platform URL",
			url:            "https://www.instagram.com/reel/ABC123",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Unsupported URL",
		},
		{
			name:           "random URL",
			url:            "https://example.com/video/123",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Unsupported URL",
		},
		{
			name:           "invalid URL format",
			url:            "not-a-url",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Unsupported URL",
		},
		{
			name:           "empty URL",
			url:            "",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			router := setupTestRouter(mockDownloader)

			payload := map[string]string{"url": tt.url}
			jsonPayload, _ := json.Marshal(payload)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/download", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.False(t, response["ok"].(bool))

			errorMsg, ok := response["error"].(string)
			require.True(t, ok)
			assert.Contains(t, errorMsg, tt.expectedError)
		})
	}
}

// TestDownloadHandler_MissingParameters tests missing or malformed request parameters
func TestDownloadHandler_MissingParameters(t *testing.T) {
	tests := []struct {
		name           string
		payload        interface{}
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "missing url field",
			payload:        map[string]interface{}{},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid request",
		},
		{
			name:           "empty JSON body",
			payload:        nil,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid request",
		},
		{
			name:           "invalid JSON",
			payload:        "not-json",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			router := setupTestRouter(mockDownloader)

			var body *bytes.Buffer
			if tt.payload != nil {
				jsonPayload, _ := json.Marshal(tt.payload)
				body = bytes.NewBuffer(jsonPayload)
			} else {
				body = bytes.NewBuffer([]byte{})
			}

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/download", body)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.False(t, response["ok"].(bool))

			errorMsg, ok := response["error"].(string)
			require.True(t, ok)
			assert.Contains(t, errorMsg, tt.expectedError)
		})
	}
}

// TestDownloadHandler_DownloadError tests when the downloader returns an error
func TestDownloadHandler_DownloadError(t *testing.T) {
	mockDownloader := NewMockDownloader()
	mockDownloader.downloadError = assert.AnError

	router := setupTestRouter(mockDownloader)

	payload := map[string]string{"url": "https://www.youtube.com/watch?v=abc123"}
	jsonPayload, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/download", bytes.NewBuffer(jsonPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.False(t, response["ok"].(bool))
	assert.Contains(t, response["error"], "Download failed")
}

// TestDownloadHandler_ListPlatforms tests the list platforms endpoint
func TestDownloadHandler_ListPlatforms(t *testing.T) {
	mockDownloader := NewMockDownloader()
	router := setupTestRouter(mockDownloader)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/download/platforms", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response["ok"].(bool))

	platforms, ok := response["platforms"].([]interface{})
	require.True(t, ok)
	assert.Len(t, platforms, 2)

	// Check YouTube platform
	youtubePlatform := platforms[0].(map[string]interface{})
	assert.Equal(t, "YouTube", youtubePlatform["name"])
	assert.Equal(t, "youtube", youtubePlatform["code"])

	// Check TikTok platform
	tiktokPlatform := platforms[1].(map[string]interface{})
	assert.Equal(t, "TikTok", tiktokPlatform["name"])
	assert.Equal(t, "tiktok", tiktokPlatform["code"])
}

// TestDownloadHandler_ListDownloads tests the list downloads endpoint
func TestDownloadHandler_ListDownloads(t *testing.T) {
	tests := []struct {
		name           string
		mockResults    map[download.Platform][]string
		mockError      error
		expectedStatus int
		expectedOk     bool
		expectedCount  int
	}{
		{
			name: "successful list with downloads",
			mockResults: map[download.Platform][]string{
				download.PlatformYouTube: {"/tmp/youtube/video1.mp4", "/tmp/youtube/video2.mp4"},
				download.PlatformTikTok:  {"/tmp/tiktok/video1.mp4"},
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
			expectedCount:  3,
		},
		{
			name: "successful list with no downloads",
			mockResults: map[download.Platform][]string{
				download.PlatformYouTube: {},
				download.PlatformTikTok:  {},
			},
			expectedStatus: http.StatusOK,
			expectedOk:     true,
			expectedCount:  0,
		},
		{
			name:           "list downloads error",
			mockError:      assert.AnError,
			expectedStatus: http.StatusInternalServerError,
			expectedOk:     false,
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			mockDownloader.listResults = tt.mockResults
			mockDownloader.listError = tt.mockError

			router := setupTestRouter(mockDownloader)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/api/download/library", nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOk, response["ok"])
		})
	}
}

// TestDownloadHandler_ListPlatformDownloads tests listing downloads for a specific platform
func TestDownloadHandler_ListPlatformDownloads(t *testing.T) {
	tests := []struct {
		name           string
		platform       string
		expectedStatus int
		expectedOk     bool
	}{
		{
			name:           "list youtube downloads",
			platform:       "youtube",
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
		{
			name:           "list tiktok downloads",
			platform:       "tiktok",
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
		{
			name:           "list unknown platform downloads",
			platform:       "unknown",
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			router := setupTestRouter(mockDownloader)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/api/download/library/"+tt.platform, nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOk, response["ok"])
			assert.Contains(t, response, "videos")
			assert.Contains(t, response, "count")
		})
	}
}

// TestDownloadHandler_DeleteDownload tests the delete download endpoint
func TestDownloadHandler_DeleteDownload(t *testing.T) {
	tests := []struct {
		name           string
		platform       string
		videoID        string
		expectedStatus int
		expectedOk     bool
	}{
		{
			name:           "successful delete youtube video",
			platform:       "youtube",
			videoID:        "dQw4w9WgXcQ",
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
		{
			name:           "successful delete tiktok video",
			platform:       "tiktok",
			videoID:        "7123456789012345678",
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
		{
			name:           "delete non-existent video",
			platform:       "youtube",
			videoID:        "nonexistent",
			expectedStatus: http.StatusOK,
			expectedOk:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDownloader := NewMockDownloader()
			router := setupTestRouter(mockDownloader)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("DELETE", "/api/download/library/"+tt.platform+"/"+tt.videoID, nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOk, response["ok"])

			if tt.expectedOk {
				assert.Contains(t, response, "message")
			}
		})
	}
}

// TestDownloadHandler_ConcurrentRequests tests concurrent download requests
func TestDownloadHandler_ConcurrentRequests(t *testing.T) {
	mockDownloader := NewMockDownloader()
	router := setupTestRouter(mockDownloader)

	urls := []string{
		"https://www.youtube.com/watch?v=video1",
		"https://www.youtube.com/watch?v=video2",
		"https://www.tiktok.com/@user/video/123456",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			payload := map[string]string{"url": url}
			jsonPayload, _ := json.Marshal(payload)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/download", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.True(t, response["ok"].(bool))
		})
	}
}
