package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Config holds Qdrant-specific configuration.
type Config struct {
	URL                  string
	Collection           string
	TextVectorName       string
	VisualVectorName     string
	AudioVectorName      string
	TranscriptVectorName string
	SparseVectorName     string
	TextDimensions       int
	VisualDimensions     int
	AudioDimensions      int
	TranscriptDimensions int
	MinInstantScore      float64
	TimeoutMs            int
	BatchSize            int // max assets per batch upsert (0 = no chunking, default 500)

	// CollectionVersion is the schema version suffix appended to the
	// physical collection name. Leave empty for legacy mode that operates
	// directly on Collection. When set, points are written into
	// `{Collection}_{CollectionVersion}` and exposed via the alias.
	CollectionVersion string

	// CollectionAlias is the alias clients read/write through while
	// migrations happen underneath. Defaults to `{Collection}_current`
	// when CollectionVersion is set.
	CollectionAlias string

	// DisableAlias forces writing directly to the logical Collection
	// even when CollectionVersion is set. Useful for one-off backfill
	// scripts that intentionally target a specific versioned collection.
	DisableAlias bool
}

// QdrantClient implements the Store interface via Qdrant's REST API.
// It communicates with the Qdrant HTTP endpoint (default port 6333).
//
// API reference: https://qdrant.tech/documentation/api/
type QdrantClient struct {
	baseURL    string
	collection string
	cfg        Config
	httpClient *http.Client
	log        *zap.Logger
}

// NewQdrantClient creates a new Qdrant HTTP client.
func NewQdrantClient(cfg Config) *QdrantClient {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	baseURL := strings.TrimRight(cfg.URL, "/")

	return &QdrantClient{
		baseURL:    baseURL,
		collection: cfg.Collection,
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
		log:        zap.NewNop(),
	}
}

// SetLogger sets the logger on the QdrantClient for migration/debug logs.
func (c *QdrantClient) SetLogger(log *zap.Logger) {
	if log != nil {
		c.log = log
	}
}

// qdrantRequest sends an HTTP request to Qdrant and decodes the response.
// Errors return *httpError so callers can extract the status code structurally
// via errors.As, instead of pattern-matching error-string text.
func (c *QdrantClient) qdrantRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	url := c.baseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return respBody, newHTTPError(resp.StatusCode, method, path, respBody)
	}

	return respBody, nil
}

// Health checks that Qdrant is reachable.
// Qdrant v1.18+ exposes /healthz (simple) and /readyz (checks shards).
// We use /readyz for a more informative liveness probe.
func (c *QdrantClient) Health(ctx context.Context) error {
	_, err := c.qdrantRequest(ctx, "GET", "/readyz", nil)
	return err
}

// Close is a no-op for the HTTP client (connections are pooled and auto-closed).
func (c *QdrantClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// httpError carries the HTTP status code from a Qdrant response so callers
// can branch on it without parsing the error text (which varies across
// Qdrant versions and locales).
type httpError struct {
	StatusCode int
	Method     string
	Path       string
	Body       []byte
}

func newHTTPError(status int, method, path string, body []byte) *httpError {
	return &httpError{StatusCode: status, Method: method, Path: path, Body: body}
}

func (e *httpError) Error() string {
	// Truncate body to keep logs readable while preserving enough context.
	body := string(e.Body)
	if len(body) > 256 {
		body = body[:256] + "...(truncated)"
	}
	return fmt.Sprintf("qdrant HTTP %d %s %s: %s", e.StatusCode, e.Method, e.Path, body)
}

// AsHTTPError extracts an *httpError from err if present. Returns nil otherwise.
// Use over strings.Contains(err.Error(), "status 409") so caller code survives
// Qdrant message wording changes across server versions.
func AsHTTPError(err error) *httpError {
	if err == nil {
		return nil
	}
	var he *httpError
	if errors.As(err, &he) {
		return he
	}
	return nil
}

// compile-time interface check
var _ Store = (*QdrantClient)(nil)
