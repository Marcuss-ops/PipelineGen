// Package integration provides integration tests
package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"
)

// ClipTestSuite tests all Clip endpoints
type ClipTestSuite struct {
	APITestSuite
}

// TestClip runs the clip test suite
func TestClip(t *testing.T) {
	suite.Run(t, new(ClipTestSuite))
}

// TestSearchFolders tests POST /api/clip/search-folders
func (s *ClipTestSuite) TestSearchFolders() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "search all folders",
			payload: map[string]interface{}{
				"query": "",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "search with query",
			payload: map[string]interface{}{
				"query": "nature",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "search with group",
			payload: map[string]interface{}{
				"query":       "",
				"group":       "Nature",
				"max_results": 20,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "search with parent folder",
			payload: map[string]interface{}{
				"query":     "",
				"parent_id": "mock-root-folder-id",
				"max_depth": 3,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/search-folders", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestReadFolderClips tests POST /api/clip/read-folder-clips
func (s *ClipTestSuite) TestReadFolderClips() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "read by folder id",
			payload: map[string]interface{}{
				"folder_id": "mock-folder-1",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "read by folder name",
			payload: map[string]interface{}{
				"folder_name": "Test Folder",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "read with subfolders",
			payload: map[string]interface{}{
				"folder_id":          "mock-folder-1",
				"include_subfolders": true,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing folder info",
			payload: map[string]interface{}{
				"include_subfolders": true,
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/read-folder-clips", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestSuggest tests POST /api/clip/suggest
func (s *ClipTestSuite) TestSuggest() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "suggest with title only",
			payload: map[string]interface{}{
				"title":       "Amazing Nature Documentary",
				"max_results": 10,
				"min_score":   10.0,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "suggest with title and script",
			payload: map[string]interface{}{
				"title":       "Ocean Life",
				"script":      "Explore the deep ocean and discover amazing sea creatures",
				"group":       "Nature",
				"max_results": 5,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing title",
			payload: map[string]interface{}{
				"script":      "Some script text",
				"max_results": 10,
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "with defaults",
			payload: map[string]interface{}{
				"title": "Simple Title",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/suggest", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestCreateSubfolder tests POST /api/clip/create-subfolder
func (s *ClipTestSuite) TestCreateSubfolder() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "create simple subfolder",
			payload: map[string]interface{}{
				"folder_name": "New Test Folder",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "create with parent",
			payload: map[string]interface{}{
				"folder_name": "Child Folder",
				"parent_id":   "mock-root-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "create with group",
			payload: map[string]interface{}{
				"folder_name": "Group Folder",
				"group":       "Nature",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing folder name",
			payload: map[string]interface{}{
				"parent_id": "mock-root-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/create-subfolder", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestSubfolders tests POST /api/clip/subfolders
func (s *ClipTestSuite) TestSubfolders() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "list root subfolders",
			payload: map[string]interface{}{
				"parent_id":   "mock-root-folder-id",
				"max_depth":   2,
				"max_results": 50,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "list with defaults",
			payload: map[string]interface{}{
				"parent_id": "mock-root-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "deep listing",
			payload: map[string]interface{}{
				"parent_id":   "mock-folder-1",
				"max_depth":   5,
				"max_results": 100,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/subfolders", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestDownloadClip tests POST /api/clip/download
func (s *ClipTestSuite) TestDownloadClip() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "download with title",
			payload: map[string]interface{}{
				"youtube_url":  "https://www.youtube.com/watch?v=test123",
				"title":        "Downloaded Test Clip",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "download with time range",
			payload: map[string]interface{}{
				"youtube_url":  "https://www.youtube.com/watch?v=test456",
				"title":        "Trimmed Clip",
				"start_time":   "00:00:10",
				"end_time":     "00:00:30",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "download with group",
			payload: map[string]interface{}{
				"youtube_url":  "https://www.youtube.com/watch?v=test789",
				"title":        "Grouped Clip",
				"group":        "Nature",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing youtube url",
			payload: map[string]interface{}{
				"title":        "No URL Clip",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/download", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestUploadClip tests POST /api/clip/upload
func (s *ClipTestSuite) TestUploadClip() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "upload with title",
			payload: map[string]interface{}{
				"clip_path":    "/tmp/test_clip.mp4",
				"title":        "Uploaded Test Clip",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "upload with group",
			payload: map[string]interface{}{
				"clip_path":    "/tmp/nature_clip.mp4",
				"title":        "Nature Upload",
				"group":        "Nature",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing clip path",
			payload: map[string]interface{}{
				"title":        "No Path Clip",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/clip/upload", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestClipHealth tests GET /api/clip/health
func (s *ClipTestSuite) TestClipHealth() {
	w := s.GET("/api/clip/health")
	s.AssertStatus(w, http.StatusOK)
	s.AssertOK(w)
}

// TestGetGroups tests GET /api/clip/groups
func (s *ClipTestSuite) TestGetGroups() {
	w := s.GET("/api/clip/groups")
	s.AssertStatus(w, http.StatusOK)
	s.AssertOK(w)
}