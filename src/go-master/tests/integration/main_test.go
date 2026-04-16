// Package integration provides integration tests for the Velox API
package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"velox/go-master/internal/api/handlers"
	"velox/go-master/internal/stock"
	"velox/go-master/tests/mocks"
)

// APITestSuite is the main test suite
type APITestSuite struct {
	suite.Suite
	router    *gin.Engine
	mockDrive *mocks.MockDriveClient
	mockRust  *mocks.MockRustBinary
}

// SetupSuite runs once before all tests
func (s *APITestSuite) SetupSuite() {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create mocks
	s.mockDrive = mocks.NewMockDriveClient()
	s.mockRust = mocks.NewMockRustBinary()

	// Create server with mocked dependencies
	s.router = s.createTestServer()
}

// createTestServer creates a test server with mocked handlers
func (s *APITestSuite) createTestServer() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	// Create API routes
	apiGroup := router.Group("/api")

	// Stock routes with mock
	stockManager, _ := stock.NewManager("/tmp/velox-test-stock", &mocks.MockYouTubeClient{})
	stockHandler := handlers.NewStockSearchHandler(stockManager)
	stockHandler.RegisterRoutes(apiGroup)

	// Clip routes with mock
	// Note: NewClipHandler requires (rootFolderID, credentialsFile, tokenFile)
	clipHandler := handlers.NewClipHandler("test-root-folder", "test-credentials.json", "test-token.json")
	clipHandler.RegisterRoutes(apiGroup)

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true, "status": "healthy"})
	})

	return router
}

// TestAPI runs the test suite
func TestAPI(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION_TESTS") != "" {
		t.Skip("Skipping integration tests")
	}
	suite.Run(t, new(APITestSuite))
}

// Helper methods

// POST performs a POST request and returns the response recorder
func (s *APITestSuite) POST(path string, payload interface{}) *httptest.ResponseRecorder {
	jsonPayload, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", path, bytes.NewBuffer(jsonPayload))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(w, req)
	return w
}

// GET performs a GET request and returns the response recorder
func (s *APITestSuite) GET(path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", path, nil)
	s.router.ServeHTTP(w, req)
	return w
}

// AssertOK asserts that response has ok: true
func (s *APITestSuite) AssertOK(w *httptest.ResponseRecorder) {
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	s.NoError(err)
	s.True(response["ok"].(bool), "Expected ok to be true")
}

// AssertError asserts that response has ok: false
func (s *APITestSuite) AssertError(w *httptest.ResponseRecorder) {
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	s.NoError(err)
	s.False(response["ok"].(bool), "Expected ok to be false")
}

// AssertStatus asserts HTTP status code
func (s *APITestSuite) AssertStatus(w *httptest.ResponseRecorder, expected int) {
	s.Equal(expected, w.Code, "Expected status %d but got %d", expected, w.Code)
}

// MockDrive returns the mock drive client
func (s *APITestSuite) MockDrive() *mocks.MockDriveClient {
	return s.mockDrive
}

// MockRust returns the mock rust binary
func (s *APITestSuite) MockRust() *mocks.MockRustBinary {
	return s.mockRust
}

// AssertJSONField asserts a JSON field value
func (s *APITestSuite) AssertJSONField(w *httptest.ResponseRecorder, field string, expected interface{}) {
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	s.NoError(err)
	s.Equal(expected, response[field], "Field %s mismatch", field)
}

// Helper for assert package usage in subtests
func AssertJSONField(t *testing.T, w *httptest.ResponseRecorder, field string, expected interface{}) {
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, expected, response[field], "Field %s mismatch", field)
}