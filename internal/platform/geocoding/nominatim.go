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
	"sort"
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

// Geocode searches one grounded place name. Ambiguous names are resolved
// against the request's grounded geographic context and refused (ErrNoResult)
// when no candidate passes the disambiguation criteria. Only validated
// positive hits are cached, keyed by normalized (language, query, context);
// retries after an outage remain possible, while repeated successful
// generation jobs do not hit the service.
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
	// Fetch SEVERAL candidates: with limit=1 the adapter cannot see that a
	// name is ambiguous, and the first provider hit silently becomes the map.
	values.Set("limit", fmt.Sprintf("%d", nominatimCandidateLimit))
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
	defer func() { _ = response.Body.Close() }()
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
	var hits []searchHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: decode response: %w", err)
	}
	if len(hits) == 0 {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: no result for grounded place %q: %w", req.Query, capgeocoding.ErrNoResult)
	}
	best, score, rationale, ok := selectCandidate(req.Query, req.Context, hits)
	if !ok {
		// Ambiguous and no grounded context resolves it: refuse rather than
		// silently pin the map on an arbitrary namesake.
		return capgeocoding.Result{}, fmt.Errorf("nominatim: grounded place %q is ambiguous across %d candidates and no grounded context resolves it: %w",
			req.Query, len(hits), capgeocoding.ErrNoResult)
	}
	var result capgeocoding.Result
	if _, err := fmt.Sscan(best.Latitude, &result.Latitude); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: invalid latitude: %w", err)
	}
	if _, err := fmt.Sscan(best.Longitude, &result.Longitude); err != nil {
		return capgeocoding.Result{}, fmt.Errorf("nominatim: invalid longitude: %w", err)
	}
	result.DisplayName = best.DisplayName
	result.Scope = classifyScope(req.Query, best.Category, best.Type, best.Addresstype, best.Address)
	result.Provider = n.providerIdentity()
	result.PlaceID = best.PlaceID.String()
	result.Score = score
	result.Rationale = rationale
	if err := result.Validate(); err != nil {
		return capgeocoding.Result{}, err
	}
	if err := writeCached(cachePath, result, req.Context); err != nil {
		return capgeocoding.Result{}, err
	}
	return result, nil
}

// nominatimCandidateLimit is how many provider candidates the adapter fetches
// to SEE an ambiguity. It bounds the disambiguation input only; exactly one
// result is ever accepted.
const nominatimCandidateLimit = 5

// searchHit is one provider candidate. PlaceID and Importance are optional in
// compatible endpoints, so both tolerate absence.
type searchHit struct {
	PlaceID     json.Number       `json:"place_id"`
	Name        string            `json:"name"`
	Latitude    string            `json:"lat"`
	Longitude   string            `json:"lon"`
	DisplayName string            `json:"display_name"`
	Category    string            `json:"category"`
	Type        string            `json:"type"`
	Addresstype string            `json:"addresstype"`
	Importance  float64           `json:"importance"`
	Address     map[string]string `json:"address"`
}

// Disambiguation scoring. A candidate must clear acceptanceScore to be
// eligible at all; between candidates the winner must either lead by
// ambiguityMargin or win on grounded context the runner-up lacks.
const (
	baseScore         = 0.50
	nameMatchScore    = 0.25
	countryMatchScore = 0.15
	regionMatchScore  = 0.10
	cityLevelScore    = 0.12
	importanceScore   = 0.10
	acceptanceScore   = 0.50
	ambiguityMargin   = 0.08
)

// selectCandidate scores every provider hit and returns the accepted one.
// ok is false when the name is ambiguous and neither score margin nor the
// grounded geographic context decides it — that is a certified refusal, not a
// guess.
func selectCandidate(query string, ctx capgeocoding.Context, hits []searchHit) (searchHit, float64, string, bool) {
	type scored struct {
		hit        searchHit
		score      float64
		matched    string
		ctxDecided bool
		cityLevel  bool
	}
	scoredHits := make([]scored, 0, len(hits))
	for _, hit := range hits {
		s := scored{hit: hit, score: baseScore}
		if nameMatches(query, hit) {
			s.score += nameMatchScore
			s.matched = "name"
		}
		// Nominatim often returns both a city's administrative boundary and a
		// same-name county. Prefer the city-level result when the requested
		// name exactly matches it; this is a structural specificity signal,
		// not a guess based on provider ordering or importance alone.
		if strings.EqualFold(hit.Addresstype, "city") ||
			strings.EqualFold(hit.Addresstype, "town") ||
			strings.EqualFold(hit.Addresstype, "municipality") {
			s.score += cityLevelScore
			s.cityLevel = true
		}
		if contextMatchesCountry(ctx, hit) {
			s.score += countryMatchScore
			s.ctxDecided = true
		}
		if contextMatchesRegion(ctx, hit) {
			s.score += regionMatchScore
			s.ctxDecided = true
		}
		if hit.Importance > 0 {
			importance := hit.Importance
			if importance > 1 {
				importance = 1
			}
			s.score += importanceScore * importance
		}
		if s.score > 1 {
			s.score = 1
		}
		scoredHits = append(scoredHits, s)
	}
	// Stable ranking: ties keep provider order, so the decision is
	// deterministic for identical responses.
	sort.SliceStable(scoredHits, func(i, j int) bool { return scoredHits[i].score > scoredHits[j].score })
	top := scoredHits[0]
	if top.score < acceptanceScore {
		return searchHit{}, 0, "", false
	}
	margin := 0.0
	if len(scoredHits) > 1 {
		margin = top.score - scoredHits[1].score
		// A clear lead decides; otherwise grounded context or a more specific
		// city-level result may decide. Two equally specific namesakes with no
		// context are refused.
		if margin < ambiguityMargin &&
			(!top.ctxDecided || scoredHits[1].ctxDecided) &&
			(!top.cityLevel || scoredHits[1].cityLevel) {
			return searchHit{}, 0, "", false
		}
	}
	rationale := fmt.Sprintf("candidates=%d score=%.2f margin=%.2f match=%s context_decided=%t",
		len(scoredHits), top.score, margin, orNone(top.matched), top.ctxDecided)
	return top.hit, top.score, rationale, true
}

func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func normalizePlaceToken(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func nameMatches(query string, hit searchHit) bool {
	want := normalizePlaceToken(query)
	if want == "" {
		return false
	}
	if normalizePlaceToken(hit.Name) == want {
		return true
	}
	// The display name's most specific component is the place's own name.
	first := strings.SplitN(hit.DisplayName, ",", 2)[0]
	return normalizePlaceToken(first) == want
}

func contextMatchesCountry(ctx capgeocoding.Context, hit searchHit) bool {
	want := normalizePlaceToken(ctx.Country)
	if want == "" {
		return false
	}
	return normalizePlaceToken(hit.Address["country"]) == want ||
		normalizePlaceToken(hit.Address["country_code"]) == want
}

func contextMatchesRegion(ctx capgeocoding.Context, hit searchHit) bool {
	want := normalizePlaceToken(ctx.Region)
	if want == "" {
		return false
	}
	for _, key := range []string{"state", "region", "province", "county"} {
		if normalizePlaceToken(hit.Address[key]) == want {
			return true
		}
	}
	return false
}

// providerIdentity records which adapter and endpoint produced a cached hit,
// so a cache entry can always be traced back to its provider.
func (n *Nominatim) providerIdentity() string {
	return "nominatim(" + n.baseURL + ")"
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
	// The cache must distinguish the geographic context used: the same name
	// resolved inside two different countries must never share one answer.
	// The empty context keeps the historical key so existing caches stay valid.
	if !req.Context.IsZero() {
		key += "\x00" + req.Context.Key()
	}
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
	if err := entry.Validate(); err != nil {
		return capgeocoding.Result{}, false, false, fmt.Errorf("nominatim: invalid cached result: %w", err)
	}
	return entry.Result, entry.ScopeClassified, true, nil
}

// cacheEntry is the on-disk cache record. ScopeClassified records that
// classifyScope already inspected the source response, so a Scope-less hit
// from an endpoint that omits addresstype is served instead of refetched on
// every read. Legacy records without the flag are refreshed once and
// rewritten with it set. The selection identity (provider, coordinates,
// rationale) rides inside the embedded Result.
type cacheEntry struct {
	capgeocoding.Result
	ScopeClassified bool `json:"scope_classified,omitempty"`
	// ContextKey is the grounded geographic context the answer was selected
	// under. A cache file is only ever keyed by that context (see cachePath);
	// recording it makes the provenance auditable from the entry itself.
	ContextKey string `json:"context_key,omitempty"`
}

func writeCached(path string, result capgeocoding.Result, ctx capgeocoding.Context) error {
	data, err := json.Marshal(cacheEntry{Result: result, ScopeClassified: true, ContextKey: ctx.Key()})
	if err != nil {
		return fmt.Errorf("nominatim: encode cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nominatim-*.tmp")
	if err != nil {
		return fmt.Errorf("nominatim: create cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
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
