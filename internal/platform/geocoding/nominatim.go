// Package geocoding provides PipelineGen's Nominatim adapter. The public
// Nominatim server is a low-volume shared service: every request is paced to
// at most one per second per adapter instance, carries an identifying
// User-Agent, and positive results are cached on disk before reuse. The caller
// owns explicit opt-in; this adapter does not start background lookups.
package geocoding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	capgeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	defaultNominatimURL = "https://nominatim.openstreetmap.org/search"
	maxResponseBytes    = 1 << 20
)

// NominatimConfig controls the replaceable Nominatim-compatible endpoint.
type NominatimConfig struct {
	// BaseURL is the Search endpoint. It is configurable so an operator can
	// switch to a self-hosted or third-party compatible service without a
	// code change. Empty selects the public OSM endpoint.
	BaseURL string
	// UserAgent must identify the application and provide a contact address
	// or URL for production use; a stock HTTP-library User-Agent is not used.
	UserAgent string
	// CacheDir is a durable local directory. Empty is rejected rather than
	// silently turning repeated requests into shared-service load.
	CacheDir string
	// HTTPClient is an optional test/transport seam. Nil uses a bounded client.
	HTTPClient *http.Client
	// MinimumInterval is configurable for private test endpoints. Public
	// Nominatim's policy requires no more than one request per second.
	MinimumInterval time.Duration
}

// Nominatim implements the neutral Geocoder contract.
type Nominatim struct {
	baseURL         string
	userAgent       string
	cacheDir        string
	client          *http.Client
	minimumInterval time.Duration
	mu              sync.Mutex
	lastRequest     time.Time
}

// NewNominatim validates production identity/cache configuration and creates
// the cache directory. It does not make a network request.
func NewNominatim(cfg NominatimConfig) (*Nominatim, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultNominatimURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("nominatim: base URL must be an absolute http(s) URL")
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		return nil, fmt.Errorf("nominatim: identifying user agent is required")
	}
	cacheDir := strings.TrimSpace(cfg.CacheDir)
	if cacheDir == "" {
		return nil, fmt.Errorf("nominatim: durable cache directory is required")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("nominatim: create cache directory: %w", err)
	}
	minimumInterval := cfg.MinimumInterval
	if minimumInterval == 0 {
		minimumInterval = time.Second
	}
	if minimumInterval < 0 {
		return nil, fmt.Errorf("nominatim: minimum interval cannot be negative")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Nominatim{
		baseURL: baseURL, userAgent: userAgent, cacheDir: cacheDir,
		client: client, minimumInterval: minimumInterval,
	}, nil
}

var _ capgeocoding.Geocoder = (*Nominatim)(nil)

// Geocode searches one grounded place name. Only validated positive hits are
// cached, keyed by normalized (language, query); retries after an outage remain
// possible, while repeated successful generation jobs do not hit the service.
func (n *Nominatim) Geocode(ctx context.Context, request capgeocoding.Request) (capgeocoding.Result, error) {
	if n == nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: adapter is nil")
	}
	req, err := capgeocoding.NormalizeRequest(request)
	if err != nil {
		return capgeocoding.Result{}, err
	}
	cachePath := n.cachePath(req)
	if result, ok, err := readCached(cachePath); err != nil {
		return capgeocoding.Result{}, err
	} else if ok {
		return result, nil
	}

	query, err := url.Parse(n.baseURL)
	if err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: parse endpoint: %w", err)
	}
	values := query.Query()
	values.Set("q", req.Query)
	values.Set("format", "jsonv2")
	values.Set("limit", "1")
	values.Set("accept-language", req.Language)
	query.RawQuery = values.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, query.String(), nil)
	if err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: build request: %w", err)
	}
	httpRequest.Header.Set("User-Agent", n.userAgent)
	httpRequest.Header.Set("Accept", "application/json")

	if err := n.waitForPermit(ctx); err != nil {
		return capgeocoding.Result{}, err
	}
	response, err := n.client.Do(httpRequest)
	if err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return capgeocoding.Result{}, fmt.Errorf("nominatim: search returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: response exceeds %d bytes", maxResponseBytes)
	}
	var hits []struct {
		Latitude    string `json:"lat"`
		Longitude   string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(body, &hits); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: decode response: %w", err)
	}
	if len(hits) == 0 {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: no result for grounded place %q", req.Query)
	}
	var result capgeocoding.Result
	if _, err := fmt.Sscan(hits[0].Latitude, &result.Latitude); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: invalid latitude: %w", err)
	}
	if _, err := fmt.Sscan(hits[0].Longitude, &result.Longitude); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: invalid longitude: %w", err)
	}
	result.DisplayName = hits[0].DisplayName
	if err := result.Validate(); err != nil {
		return capgeocoding.Result{}, err
	}
	if err := writeCached(cachePath, result); err != nil {
		return capgeocoding.Result{}, err
	}
	return result, nil
}

// waitForPermit serializes public-service requests from this adapter and
// spaces them by the configured interval. The lock stays held while waiting
// and until lastRequest is updated so concurrent callers cannot burst.
func (n *Nominatim) waitForPermit(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lastRequest.IsZero() {
		n.lastRequest = time.Now()
		return nil
	}
	remaining := n.minimumInterval - time.Since(n.lastRequest)
	if remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	n.lastRequest = time.Now()
	return nil
}

func (n *Nominatim) cachePath(req capgeocoding.Request) string {
	key := strings.ToLower(req.Language) + "\x00" + strings.ToLower(strings.Join(strings.Fields(req.Query), " "))
	// The cache key digest goes through the canonical digest SSOT rather than
	// importing crypto/sha256 here (percheck_digest_sha256_ban).
	return filepath.Join(n.cacheDir, digest.SHA256String(key)+".json")
}

func readCached(path string) (capgeocoding.Result, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return capgeocoding.Result{}, false, nil
	}
	if err != nil {
		return capgeocoding.Result{}, false, fmt.Errorf("nominatim: read cache: %w", err)
	}
	var result capgeocoding.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return capgeocoding.Result{}, false, fmt.Errorf("nominatim: decode cache %q: %w", path, err)
	}
	if err := result.Validate(); err != nil {
		return capgeocoding.Result{}, false, fmt.Errorf("nominatim: invalid cached result: %w", err)
	}
	return result, true, nil
}

func writeCached(path string, result capgeocoding.Result) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("nominatim: encode cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nominatim-*.tmp")
	if err != nil {
		return fmt.Errorf("nominatim: create cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("nominatim: write cache: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("nominatim: sync cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("nominatim: close cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("nominatim: promote cache: %w", err)
	}
	return nil
}
