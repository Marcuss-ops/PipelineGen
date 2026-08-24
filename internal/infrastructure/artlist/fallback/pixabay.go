// Package fallback implements the artlist.Searcher port for free
// public video APIs that act as last-resort live-search sources
// when the primary Artlist feed is down or returns empty.
//
// PR2.4 scope:
//
//   - pixabay.go (this file): Pixabay /api/videos/ endpoint.
//   - pexels.go: Pexels /v1/videos/search endpoint.
//
// The application layer's fallback chain in
// internal/capabilities/assets/providers/artlist/search_fallback.go composes these
// along with the DB + cached-scraper providers. As of PR2.4 these
// are exposed as orphans (no direct consumer in this package); the
// chain still uses the legacy SourceProvider shape, and the legacy
// provider_pixabay.go / provider_pexels.go in the application layer
// wrap these to bridge back to the []ScraperClip contract that the
// chain consumes. PR2.5 will rewrite the chain on the new port.
//
// Architecture enforcement (PR2.1 contract):
//
//   - No os/exec, no net/http on application paths.
//   - No knowledge of ScraperClip, ScraperResponse, or DB schema.
//   - Sentinel errors are returned (never raw http errors) so the
//     chain can decide retry/skipped/fallback per intent.
package fallback

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Pixabay is an HTTP-clamped implementation of artlist.Searcher
// backed by the Pixabay /api/videos/ endpoint.
type Pixabay struct {
	client *http.Client
	cfg    Config
	// SourceName is the SourceName field each returned Candidate
	// carries; reserved so the chain can tag candidate origin.
	SourceName string
}

// Config carries wiring the application passes to New. APIKey and
// BaseURL are mandatory; the other fields fall back to defaults.
type Config struct {
	APIKey  string
	BaseURL string // default "https://pixabay.com/api"
	// Timeout bounds the entire request (DNS + TLS + body read).
	// Default 45s. 0 means use default.
	Timeout time.Duration
	// SourceName overrides the SourceName attached to each
	// returned Candidate. Default: "pixabay".
	SourceName string
}

// New constructs a Pixabay instance.
func NewPixabay(cfg Config) *Pixabay {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://pixabay.com/api"
	} else {
		cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.SourceName == "" {
		cfg.SourceName = "pixabay"
	}
	return &Pixabay{
		client:     &http.Client{Timeout: cfg.Timeout},
		cfg:        cfg,
		SourceName: cfg.SourceName,
	}
}

// Compile-time port assertion.
var _ artapp.Searcher = (*Pixabay)(nil)

// Search queries the Pixabay API. Per PR2.1 contract: returns only
// the centralised sentinels on transport failures; HTTP shape is
// never leaked to the caller.
func (p *Pixabay) Search(ctx context.Context, req artapp.SearchRequest) ([]artapp.Candidate, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: pixabay api key not configured", artapp.ErrUnavailable)
	}
	// The application chain (search_fallback.go) normalises before
	// calling the chain; we just trim again as defence-in-depth. We
	// deliberately do NOT call into the unexported
	// normalizeSearchTermLower from run_helpers because infra must
	// not depend on application internals.
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, fmt.Errorf("%w: term required", artapp.ErrEmpty)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	endpoint := p.cfg.BaseURL + "/videos/"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid pixabay base url: %v", artapp.ErrInvalidResponse, err)
	}
	q := u.Query()
	q.Set("key", p.cfg.APIKey)
	q.Set("q", term)
	q.Set("video_type", "all")
	q.Set("per_page", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", artapp.ErrInvalidResponse, err)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, mapTransportErr(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
	}

	if resp.StatusCode == http.StatusOK {
		return p.decode(body, term, limit)
	}
	return nil, mapStatusErr(resp.StatusCode, body)
}

func (p *Pixabay) decode(body []byte, term string, limit int) ([]artapp.Candidate, error) {
	var payload struct {
		Hits []struct {
			ID      int    `json:"id"`
			PageURL string `json:"pageURL"`
			Tags    string `json:"tags"`
			Videos  struct {
				Medium struct {
					URL string `json:"url"`
				} `json:"medium"`
				Large struct {
					URL string `json:"url"`
				} `json:"large"`
				Small struct {
					URL string `json:"url"`
				} `json:"small"`
			} `json:"videos"`
		} `json:"hits"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: decode pixabay: %v", artapp.ErrInvalidResponse, err)
	}

	out := make([]artapp.Candidate, 0, len(payload.Hits))
	for _, hit := range payload.Hits {
		videoURL := firstNonEmpty(hit.Videos.Medium.URL, hit.Videos.Large.URL, hit.Videos.Small.URL)
		if videoURL == "" {
			continue
		}
		title := strings.TrimSpace(hit.Tags)
		if title == "" {
			title = term
		}
		bestURL := firstNonEmpty(hit.Videos.Large.URL, hit.Videos.Medium.URL, hit.Videos.Small.URL)
		rendition := providerassets.ProviderRendition{
			Kind:      "master",
			Container: "mp4",
			URL:       bestURL,
		}

		pa := providerassets.ProviderAsset{
			Provider:   p.SourceName,
			ExternalID: fmt.Sprintf("%d", hit.ID),
			ID:         fmt.Sprintf("pixabay-%d", hit.ID),
			Title:      fmt.Sprintf("Pixabay: %s", title),
			PageURL:    hit.PageURL,
			PreviewURL: hit.PageURL,
			SourceRef:  videoURL,
			SourceName: p.SourceName,
			MediaType:  asset.MediaTypeClip,
			Keywords:   splitTags(hit.Tags),
			Renditions: []providerassets.ProviderRendition{rendition},
		}

		out = append(out, pa)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no usable videos", artapp.ErrEmptyResult)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// mapStatusErr turns a non-200 status code into the right sentinel.
// 404 is treated as ErrNotFound (term has no hits), 4xx as
// ErrInvalidResponse, 5xx as ErrTransportFallback (chain should try
// the next provider).
func mapStatusErr(status int, body []byte) error {
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	switch {
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: pixabay status=%d body=%q", artapp.ErrNotFound, status, snippet)
	case status >= 400 && status < 500:
		return fmt.Errorf("%w: pixabay status=%d body=%q", artapp.ErrInvalidResponse, status, snippet)
	default:
		// 5xx (or unusual codes) - surface as transport fallback so
		// the chain tries the next source.
		return fmt.Errorf("%w: pixabay status=%d body=%q", artapp.ErrTransportFallback, status, snippet)
	}
}

// mapTransportErr converts dial / TLS / timeout errors into the
// right sentinel. Timeout shapes a "" context.DeadlineExceeded
// fallback; everything else is transport-fallback (network down).
func mapTransportErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "context deadline exceeded") ||
		strings.Contains(err.Error(), "Client.Timeout") {
		return fmt.Errorf("%w: %v", artapp.ErrTimeout, err)
	}
	return fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// splitTags splits a comma-separated tag string into trimmed tokens.
func splitTags(tags string) []string {
	parts := strings.Split(tags, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
