// Package e2e provides end-to-end tests for stock endpoints
package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// StockE2ETestSuite tests the stock endpoints E2E
type StockE2ETestSuite struct {
	suite.Suite
	client *TestClient
	ctx    context.Context
}

// SetupSuite runs before all tests
func (s *StockE2ETestSuite) SetupSuite() {
	baseURL := os.Getenv("VELOX_API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	s.client = NewTestClient(baseURL)
	s.ctx = context.Background()

	// Wait for server to be ready
	s.T().Log("Waiting for server to be healthy...")
	_, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	if err := s.client.WaitForServer(30 * time.Second); err != nil {
		s.T().Skipf("Server not available: %v", err)
	}
	s.T().Log("Server is healthy!")
}

// TestHealth tests the health endpoint
func (s *StockE2ETestSuite) TestHealth() {
	resp, err := s.client.Health(s.ctx)
	require.NoError(s.T(), err)
	assert.True(s.T(), resp.OK)
	assert.Equal(s.T(), "healthy", resp.Status)
}

// TestSearchStock tests the search endpoint
func (s *StockE2ETestSuite) TestSearchStock() {
	// Skip if running in short mode
	if testing.Short() {
		s.T().Skip("Skipping E2E test in short mode")
	}

	req := SearchRequest{
		Title:    "nature documentary",
		MaxClips: 3,
	}

	resp, err := s.client.SearchStock(s.ctx, req)
	require.NoError(s.T(), err)
	assert.True(s.T(), resp.OK)
	// Note: Clips may be empty if no videos exist, but the request should succeed
}

// TestCreateClipHappyPath tests creating a clip (requires real YouTube URL and processing time)
func (s *StockE2ETestSuite) TestCreateClipHappyPath() {
	// Skip in short mode and CI
	if testing.Short() || os.Getenv("CI") != "" {
		s.T().Skip("Skipping long-running E2E test")
	}

	// Use a short, reliable test video
	req := StockCreateRequest{
		VideoURL:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Title:       "e2e_test_" + time.Now().Format("20060102_150405"),
		Duration:    10, // Short duration for testing
		DriveFolder: "TEST_E2E_STOCK",
	}

	s.T().Logf("Creating clip with title: %s", req.Title)
	
	// This will take time as it processes video
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()

	resp, err := s.client.CreateClip(ctx, req)
	require.NoError(s.T(), err)
	
	// The response should indicate success
	assert.True(s.T(), resp.OK, "Response should be OK")
	assert.NotEmpty(s.T(), resp.TaskID, "TaskID should not be empty")
}

// TestCreateClipInvalidURL tests error handling for invalid URLs
func (s *StockE2ETestSuite) TestCreateClipInvalidURL() {
	if testing.Short() {
		s.T().Skip("Skipping E2E test in short mode")
	}

	req := StockCreateRequest{
		VideoURL:    "https://www.youtube.com/watch?v=INVALID_ID_12345",
		Title:       "e2e_test_invalid",
		Duration:    10,
		DriveFolder: "TEST_E2E_ERROR",
	}

	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()

	resp, err := s.client.CreateClip(ctx, req)
	
	// Should either return error or OK=false
	if err != nil {
		s.T().Logf("Expected error for invalid URL: %v", err)
		return
	}
	
	assert.False(s.T(), resp.OK, "Response should not be OK for invalid URL")
}

// TestCreateClipValidation tests input validation
func (s *StockE2ETestSuite) TestCreateClipValidation() {
	tests := []struct {
		name    string
		req     StockCreateRequest
		wantErr bool
	}{
		{
			name: "empty URL",
			req: StockCreateRequest{
				VideoURL:    "",
				Title:       "test",
				Duration:    30,
				DriveFolder: "TEST",
			},
			wantErr: true,
		},
		{
			name: "invalid duration",
			req: StockCreateRequest{
				VideoURL:    "https://youtube.com/watch?v=abc",
				Title:       "test",
				Duration:    0,
				DriveFolder: "TEST",
			},
			wantErr: true,
		},
		{
			name: "empty title",
			req: StockCreateRequest{
				VideoURL:    "https://youtube.com/watch?v=abc",
				Title:       "",
				Duration:    30,
				DriveFolder: "TEST",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.T().Run(tt.name, func(t *testing.T) {
			// Skip in short mode
			if testing.Short() {
				t.Skip("Skipping in short mode")
			}

			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()

			_, err := s.client.CreateClip(ctx, tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Run the test suite
func TestStockE2E(t *testing.T) {
	if os.Getenv("SKIP_E2E_TESTS") != "" {
		t.Skip("Skipping E2E tests")
	}
	suite.Run(t, new(StockE2ETestSuite))
}