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
	if result, classified, ok, err := readCached(cachePath); err != nil {
		return capgeocoding.Result{}, err
	} else if ok && (result.Scope != "" || classified) {
		return result, nil
	}
	// Pre-scope cache entries predate geographic-level zooms. Refresh them
	// once so city/country/region classification reaches the map planner.
	// Entries the classifier has already inspected keep their Scope-less value
	// (an endpoint may legitimately omit addresstype), so the refresh happens
	// once per entry instead of on every read.

	query, err := url.Parse(n.baseURL)
	if err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: parse endpoint: %w", err)
	}
	values := query.Query()
	values.Set("q", req.Query)
	values.Set("format", "jsonv2")
	values.Set("limit", "1")
	values.Set("accept-language", req.Language)
	values.Set("addressdetails", "1")
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
		Latitude    string            `json:"lat"`
		Longitude   string            `json:"lon"`
		DisplayName string            `json:"display_name"`
		Category    string            `json:"category"`
		Type        string            `json:"type"`
		Addresstype string            `json:"addresstype"`
		Address     map[string]string `json:"address"`
	}
	if err := json.Unmarshal(body, &hits); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: decode response: %w", err)
	}
	if len(hits) == 0 {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: no result for grounded place %q: %w", req.Query, capgeocoding.ErrNoResult)
	}
	var result capgeocoding.Result
	if _, err := fmt.Sscan(hits[0].Latitude, &result.Latitude); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: invalid latitude: %w", err)
	}
	if _, err := fmt.Sscan(hits[0].Longitude, &result.Longitude); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: invalid longitude: %w", err)
	}
	result.DisplayName = hits[0].DisplayName
	result.Scope = classifyScope(req.Query, hits[0].Category, hits[0].Type, hits[0].Addresstype, hits[0].Address)
	if err := result.Validate(); err != nil {
		return capgeocoding.Result{}, err
	}
	if err := writeCached(cachePath, result); err != nil {
		return capgeocoding.Result{}, err
	}
	return result, nil
}

func classifyScope(query, category, typ, addressType string, address map[string]string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	category = strings.ToLower(category)
	typ = strings.ToLower(typ)
	addressType = strings.ToLower(addressType)
	if typ == "continent" || strings.EqualFold(address["continent"], query) {
		return "continent"
	}
	if typ == "country" || addressType == "country" || strings.EqualFold(address["country"], query) {
		return "country"
	}
	if typ == "state" || typ == "region" || typ == "province" || addressType == "state" || addressType == "region" || strings.EqualFold(address["state"], query) || strings.EqualFold(address["region"], query) || strings.EqualFold(address["province"], query) {
		return "region"
	}
	if typ == "city" || typ == "town" || typ == "village" || typ == "municipality" || typ == "administrative" || addressType == "city" || addressType == "town" || addressType == "village" {
		return "city"
	}
	if category == "place" {
		return "city"
	}
	return ""
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

func readCached(path string) (capgeocoding.Result, bool, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return capgeocoding.Result{}, false, false, nil
	}
	if err != nil {
		return capgeocoding.Result{}, false, false, fmt.Errorf("nominatim: read cache: %w", err)
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return capgeocoding.Result{}, false, false, fmt.Errorf("nominatim: decode cache %q: %w", path, err)
	}
	if err := entry.Result.Validate(); err != nil {
		return capgeocoding.Result{}, false, false, fmt.Errorf("nominatim: invalid cached result: %w", err)
	}
	return entry.Result, entry.ScopeClassified, true, nil
}

// cacheEntry is the on-disk cache record. ScopeClassified records that
// classifyScope already inspected the source response, so a Scope-less hit
// from an endpoint that omits addresstype is served instead of refetched on
// every read. Legacy records without the flag are refreshed once and
// rewritten with it set.
type cacheEntry struct {
	capgeocoding.Result
	ScopeClassified bool `json:"scope_classified,omitempty"`
}

func writeCached(path string, result capgeocoding.Result) error {
	data, err := json.Marshal(cacheEntry{Result: result, ScopeClassified: true})
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
