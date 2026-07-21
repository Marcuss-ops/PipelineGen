// Package pexels — provider.go is the canonical native Pexels
// *image* search adapter (Fase 4.1 — visual channel completion).
//
// godlike/06 SSOT (canonical owner per fact): this file owns
// the image-search surface of Pexels; the historical
// artlist/fallback/pexels.go owns the video-search surface. They
// are distinct: Pexels `/v1/videos/search` returns video files,
// `/v1/images/search` returns still images. Calling the wrong
// surface silently degrades the linker's visual channel to
// non-frame candidates — godlike/07 forbids that.
//
// godlike/06 SSOT (provider registry): this concrete
// implements the canonical providers.SearchProvider interface
// (from internal/application/assets/providers). It is THE
// canonical owner of the "images" / "pexels_images" provider
// name in providers.Registry.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty API key returns
// ErrUnavailable (godlike/06 closed-set sentinel); HTTP errors
// map to ErrUnavailable / ErrInvalidResponse. The native wire
// shape is never leaked to the caller.
package pexels

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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ProviderName is the canonical canonical name registered in
// providers.Registry. Persistent across versions.
const ProviderName = "pexels_images"

// PexelsImageFile mirrors Pexels' image-search response item.
type PexelsImageFile struct {
	ID              int    `json:"id"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	URL             string `json:"url"`
	Photographer    string `json:"photographer"`
	PhotographerURL string `json:"photographer_url"`
	AvgColor        string `json:"avg_color"`
	Src             struct {
		Original  string `json:"original"`
		Large2x   string `json:"large2x"`
		Large     string `json:"large"`
		Medium    string `json:"medium"`
		Small     string `json:"small"`
		Portrait  string `json:"portrait"`
		Landscape string `json:"landscape"`
		Tiny      string `json:"tiny"`
	} `json:"src"`
}

// Config is the canonical Pexels adapter config.
type Config struct {
	BaseURL    string
	Timeout    time.Duration
	APIKey     string
	SourceName string
}

// Provider is the canonical Pexels image SearchProvider.
type Provider struct {
	client *http.Client
	cfg    Config
}

// NewProvider constructs the canonical Pexels image provider.
func NewProvider(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.pexels.com/v1"
	} else {
		cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.SourceName == "" {
		cfg.SourceName = ProviderName
	}
	return &Provider{
		client: &http.Client{Timeout: cfg.Timeout},
		cfg:    cfg,
	}
}

// Compile-time assertion: Provider satisfies providers.SearchProvider.
var _ providers.SearchProvider = (*Provider)(nil)

// Name returns the canonical registry identifier.
func (p *Provider) Name() string { return p.SourceName() }

// SourceName returns the provider-source name stamped onto
// MediaCandidate.Source for projection.
func (p *Provider) SourceName() string { return p.cfg.SourceName }

// Capabilities advertises search + image so the canonical
// SearchFanOut aggregator routes image-typed searches here.
func (p *Provider) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityImage,
	}
}

// Search runs the canonical Pexels image search and returns
// up to req.Limit ProviderAsset candidates with MediaType=image.
func (p *Provider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return providers.SearchResult{}, fmt.Errorf("%w: pexels image api key not configured",
			artapp.ErrUnavailable)
	}
	term := strings.TrimSpace(req.Query)
	if term == "" {
		return providers.SearchResult{}, fmt.Errorf("%w: term required", artapp.ErrEmpty)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 80 {
		limit = 80
	}

	endpoint := p.cfg.BaseURL + "/images/search"
	u, err := url.Parse(endpoint)
	if err != nil {
		return providers.SearchResult{}, fmt.Errorf("%w: invalid pexels base url: %v",
			artapp.ErrInvalidResponse, err)
	}
	q := u.Query()
	q.Set("query", term)
	q.Set("per_page", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return providers.SearchResult{}, fmt.Errorf("%w: build request: %v",
			artapp.ErrInvalidResponse, err)
	}
	httpReq.Header.Set("Authorization", p.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return providers.SearchResult{}, mapTransportErr(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.SearchResult{}, fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
	}
	if resp.StatusCode == http.StatusOK {
		return p.decode(body, term, limit)
	}
	return providers.SearchResult{}, mapStatusErr(resp.StatusCode, body)
}

func (p *Provider) decode(body []byte, term string, limit int) (providers.SearchResult, error) {
	var payload struct {
		Photos       []PexelsImageFile `json:"photos"`
		TotalResults int               `json:"total_results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return providers.SearchResult{}, fmt.Errorf("%w: decode pexels images: %v",
			artapp.ErrInvalidResponse, err)
	}
	if len(payload.Photos) == 0 {
		return providers.SearchResult{}, fmt.Errorf("%w: no usable images", artapp.ErrEmptyResult)
	}

	out := providers.SearchResult{}
	candidates := make([]providers.Candidate, 0, len(payload.Photos))
	for _, photo := range payload.Photos {
		imageURL := photo.Src.Large
		if imageURL == "" {
			imageURL = photo.Src.Original
		}
		if imageURL == "" {
			continue
		}
		title := term
		if photo.Photographer != "" {
			title = fmt.Sprintf("%s by %s", term, photo.Photographer)
		}
		pa := providerassets.ProviderAsset{
			Provider:     p.cfg.SourceName,
			ExternalID:   strconv.Itoa(photo.ID),
			ID:           fmt.Sprintf("pexels-image-%d", photo.ID),
			Title:        fmt.Sprintf("Pexels image: %s", title),
			Creator:      photo.Photographer,
			PageURL:      photo.URL,
			PreviewURL:   imageURL,
			ThumbnailURL: photo.Src.Tiny,
			SourceRef:    imageURL,
			SourceName:   p.cfg.SourceName,
			MediaType:    asset.MediaTypeImage,
			Width:        photo.Width,
			Height:       photo.Height,
			Orientation:  orientationFor(photo.Width, photo.Height),
		}
		candidates = append(candidates, pa)
		if len(candidates) >= limit {
			break
		}
	}
	if len(candidates) == 0 {
		return providers.SearchResult{}, fmt.Errorf("%w: no usable images", artapp.ErrEmptyResult)
	}
	out.Candidates = candidates
	// godlike/06: NextPageToken stays empty for Pexels images —
	// the canonical Artlist contract is symmetric: "" means
	// "last page".
	return out, nil
}

// mapTransportErr normalises transport-layer errors to
// ErrUnavailable (godlike/07 typed-sentinel surface).
func mapTransportErr(err error) error {
	return fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
}

// mapStatusErr normalises HTTP status-code failures to
// the typed envelope.
func mapStatusErr(status int, body []byte) error {
	if status == http.StatusTooManyRequests {
		return fmt.Errorf("%w: pexels rate-limited (429)", artapp.ErrUnavailable)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w: pexels auth %d", artapp.ErrUnavailable, status)
	}
	if status >= 500 {
		return fmt.Errorf("%w: pexels upstream %d body=%s", artapp.ErrUnavailable, status, body)
	}
	return fmt.Errorf("%w: pexels status %d body=%s", artapp.ErrInvalidResponse, status, body)
}

// orientationFor returns the canonical orientation label.
func orientationFor(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if width == height {
		return "square"
	}
	if width > height {
		return "landscape"
	}
	return "portrait"
}
