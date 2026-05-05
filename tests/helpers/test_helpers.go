package helpers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"velox/go-master/pkg/config"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
)

// TestConfig creates a test configuration with sensible defaults
func TestConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
			GinMode:      "test",
		},
		Storage: config.StorageConfig{
			DataDir: "/tmp/pipelinegen-test",
		},
		Security: config.SecurityConfig{
			EnableAuth:           true,
			AdminToken:           "test-admin-token",
			CORSOrigins:          []string{},
			AllowedDownloadHosts: []string{},
		},
		Features: config.FeaturesConfig{
			ArtlistEnabled:    false,
			YouTubeEnabled:    false,
			DriveEnabled:      false,
			ScriptDocsEnabled: false,
			ImagesEnabled:     false,
		},
	}
}

// TestConfigWithFeatures creates a test config with specific features enabled
func TestConfigWithFeatures(artlist, youtube, drive, scriptDocs bool) *config.Config {
	cfg := TestConfig()
	cfg.Features.ArtlistEnabled = artlist
	cfg.Features.YouTubeEnabled = youtube
	cfg.Features.DriveEnabled = drive
	cfg.Features.ScriptDocsEnabled = scriptDocs
	return cfg
}

// CreateTestRouter creates a test router with the given config
func CreateTestRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	engine.Use(gin.Recovery())

	// Add health endpoints
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	engine.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// If handlers are available, register API routes
	// For now, just return the basic router

	return engine
}

// PerformRequest performs an HTTP request against a test router
func PerformRequest(r http.Handler, method, path string, headers map[string]string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// PerformAuthRequest performs an authenticated request
func PerformAuthRequest(r http.Handler, method, path string, token string, body io.Reader) *httptest.ResponseRecorder {
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	return PerformRequest(r, method, path, headers, body)
}

// JSONBody creates a JSON body for requests
func JSONBody(v interface{}) io.Reader {
	data, _ := json.Marshal(v)
	return bytes.NewReader(data)
}

// SetupTestDatabase creates a test database
func SetupTestDatabase(t *testing.T, dbPath string, schema string) *sql.DB {
	t.Helper()

	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	os.MkdirAll(dir, 0755)

	// Remove existing database
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	if schema != "" {
		_, err = db.Exec(schema)
		if err != nil {
			t.Fatalf("Failed to create schema: %v", err)
		}
	}

	return db
}

// CleanupTestDatabase cleans up a test database
func CleanupTestDatabase(t *testing.T, db *sql.DB, dbPath string) {
	t.Helper()
	db.Close()
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
}

// SetupTestDataDir creates a temporary data directory for testing
func SetupTestDataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pipelinegen-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	return dir
}

// CleanupTestDataDir cleans up a test data directory
func CleanupTestDataDir(t *testing.T, dir string) {
	t.Helper()
	os.RemoveAll(dir)
}

// AssertStatus asserts that the response has the expected status code
func AssertStatus(t *testing.T, w *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if w.Code != expected {
		t.Errorf("Expected status %d, got %d. Body: %s", expected, w.Code, w.Body.String())
	}
}

// AssertHeader asserts that the response has the expected header
func AssertHeader(t *testing.T, w *httptest.ResponseRecorder, header, expected string) {
	t.Helper()
	actual := w.Header().Get(header)
	if actual != expected {
		t.Errorf("Expected header %s to be %s, got %s", header, expected, actual)
	}
}

// AssertNoHeader asserts that the response does not have the expected header
func AssertNoHeader(t *testing.T, w *httptest.ResponseRecorder, header, notExpected string) {
	t.Helper()
	actual := w.Header().Get(header)
	if actual == notExpected {
		t.Errorf("Expected header %s to not be %s, but it was", header, notExpected)
	}
}

// StartTestServer starts a test server with the given config
func StartTestServer(ctx context.Context, cfg *config.Config) (*http.Server, error) {
	router := CreateTestRouter(cfg)
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Test server error: %v\n", err)
		}
	}()

	return srv, nil
}

// WaitForServer waits for the server to be ready
func WaitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Server not ready at %s after %v", url, timeout)
}

// GetFreePort returns a free port for testing
func GetFreePort() int {
	// Simple implementation - in production use net.Listen
	return 18080
}
