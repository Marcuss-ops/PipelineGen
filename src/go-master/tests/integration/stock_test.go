// Package integration provides integration tests
package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"
)

// StockTestSuite tests all Stock endpoints
type StockTestSuite struct {
	APITestSuite
}

// TestStock runs the stock test suite
func TestStock(t *testing.T) {
	suite.Run(t, new(StockTestSuite))
}

// TestCreateClip tests POST /api/stock/create
func (s *StockTestSuite) TestCreateClip() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful clip creation",
			payload: map[string]interface{}{
				"video_url":    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				"title":        "test_clip",
				"duration":     60,
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing video url",
			payload: map[string]interface{}{
				"title":        "test_clip",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "missing drive folder",
			payload: map[string]interface{}{
				"video_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				"title":     "test_clip",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/create", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestBatchCreateClips tests POST /api/stock/batch-create
func (s *StockTestSuite) TestBatchCreateClips() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful batch creation",
			payload: map[string]interface{}{
				"drive_folder": "test-folder-id",
				"clips": []map[string]interface{}{
					{"video_url": "https://youtube.com/watch?v=abc123", "title": "clip1", "duration": 60},
					{"video_url": "https://youtube.com/watch?v=def456", "title": "clip2", "duration": 30},
					{"video_url": "https://youtube.com/watch?v=ghi789", "title": "clip3", "duration": 45},
				},
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "empty clips array",
			payload: map[string]interface{}{
				"drive_folder": "test-folder-id",
				"clips":        []map[string]interface{}{},
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "missing clips field",
			payload: map[string]interface{}{
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/batch-create", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestCreateStudio tests POST /api/stock/create-studio
func (s *StockTestSuite) TestCreateStudio() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful studio creation",
			payload: map[string]interface{}{
				"links": []string{
					"https://www.youtube.com/watch?v=video1",
					"https://www.youtube.com/watch?v=video2",
				},
				"total_duration":  120,
				"clip_duration":   5,
				"drive_folder":    "test-folder-id",
				"title":           "studio_test",
				"smart_download":  true,
				"quality":         "1080p",
				"transition_type": "light-leak-sweep",
				"effects_enabled": true,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing links",
			payload: map[string]interface{}{
				"total_duration": 120,
				"drive_folder":   "test-folder-id",
				"title":          "studio_test",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "empty links array",
			payload: map[string]interface{}{
				"links":          []string{},
				"total_duration": 120,
				"drive_folder":   "test-folder-id",
				"title":          "studio_test",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/create-studio", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestSearchStock tests POST /api/stock/search
func (s *StockTestSuite) TestSearchStock() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful search",
			payload: map[string]interface{}{
				"title":        "nature documentary",
				"max_clips":    10,
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing title",
			payload: map[string]interface{}{
				"max_clips":    10,
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "default max_clips",
			payload: map[string]interface{}{
				"title": "mountain scenery",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/search", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestSearchYouTubePost tests POST /api/stock/search-youtube
func (s *StockTestSuite) TestSearchYouTubePost() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful youtube search",
			payload: map[string]interface{}{
				"urls": []string{
					"https://www.youtube.com/watch?v=video1",
					"https://www.youtube.com/watch?v=video2",
				},
				"clip_length":  5,
				"num_segments": 25,
				"total_length": 5,
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing urls",
			payload: map[string]interface{}{
				"clip_length":  5,
				"num_segments": 25,
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "empty urls array",
			payload: map[string]interface{}{
				"urls":         []string{},
				"clip_length":  5,
				"num_segments": 25,
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/search-youtube", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestProcessSimple tests POST /api/stock/process-simple
func (s *StockTestSuite) TestProcessSimple() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful simple process",
			payload: map[string]interface{}{
				"videos":       []string{"/tmp/test1.mp4", "/tmp/test2.mp4"},
				"output_dir":   "/tmp/output",
				"duration":     120,
				"transitions":  true,
				"effects":      true,
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing videos",
			payload: map[string]interface{}{
				"output_dir": "/tmp/output",
				"duration":   120,
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "missing output_dir",
			payload: map[string]interface{}{
				"videos":   []string{"/tmp/test1.mp4"},
				"duration": 120,
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/process-simple", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestFindAndCreate tests POST /api/stock/find-and-create
func (s *StockTestSuite) TestFindAndCreate() {
	tests := []struct {
		name       string
		payload    map[string]interface{}
		wantStatus int
		wantOk     bool
	}{
		{
			name: "successful find and create",
			payload: map[string]interface{}{
				"query":          "nature documentary",
				"max_videos":     3,
				"clip_duration":  5,
				"num_segments":   12,
				"drive_folder":   "test-folder-id",
				"min_duration":   60,
				"title":          "nature_clip",
				"smart_download": true,
				"shuffle":        true,
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
		{
			name: "missing query",
			payload: map[string]interface{}{
				"max_videos":    3,
				"clip_duration": 5,
				"drive_folder":  "test-folder-id",
			},
			wantStatus: http.StatusBadRequest,
			wantOk:     false,
		},
		{
			name: "with defaults",
			payload: map[string]interface{}{
				"query":        "wildlife",
				"drive_folder": "test-folder-id",
			},
			wantStatus: http.StatusOK,
			wantOk:     true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.POST("/api/stock/find-and-create", tt.payload)
			s.AssertStatus(w, tt.wantStatus)
			if tt.wantOk {
				s.AssertOK(w)
			} else {
				s.AssertError(w)
			}
		})
	}
}

// TestStockHealth tests the stock health endpoint
func (s *StockTestSuite) TestStockHealth() {
	w := s.GET("/api/stock/health")
	s.AssertStatus(w, http.StatusOK)
	s.AssertOK(w)
}

// TestStockProjects tests project management endpoints
func (s *StockTestSuite) TestStockProjects() {
	// Test list projects
	s.Run("list projects", func() {
		w := s.GET("/api/stock/projects")
		s.AssertStatus(w, http.StatusOK)
		s.AssertOK(w)
	})

	// Test create project
	s.Run("create project", func() {
		payload := map[string]interface{}{
			"name":        "test_project",
			"description": "Test project for integration tests",
			"tags":        []string{"test", "integration"},
		}
		w := s.POST("/api/stock/project", payload)
		s.AssertStatus(w, http.StatusCreated)
		s.AssertOK(w)
	})

	// Test get project
	s.Run("get project", func() {
		w := s.GET("/api/stock/project/test_project")
		s.AssertStatus(w, http.StatusOK)
		s.AssertOK(w)
	})
}