// Package youtube adapts internal/sources/youtube.Service to the
// canonical providers.Provider contract in
// internal/application/assets/providers.
//
// Spec: Agent 3 / PR 3F — single entry point replaces the four legacy
// SearchByTopic / SearchByTopicWithFilter / SearchTopicVideos /
// SearchTopicVideosWithFilter entry points at the application-layer
// boundary. The wrapper duplicates are removed in the same commit;
// callers route through providers.Provider instead.
//
// CapabilityFetch is intentionally NOT declared: YouTube asset
// download lives in the channel-monitor / clipresolver pipeline
// (yt-dlp extraction + Drive upload) which is out of scope for the
// Provider contract.
package youtube

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	youtubesrc "github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time assertion: *Adapter satisfies providers.Provider.
// Catches interface drift at build time.
var _ providers.Provider = (*Adapter)(nil)

// ErrSourceNotWired is returned by Search when the Adapter has no
// underlying searcher wired (nil interface OR typed-nil pointer).
var ErrSourceNotWired = errors.New("youtube adapter: source not wired")

// youtubeMediaType is the canonical MediaType assigned to every
// YouTube search candidate. YouTube live search returns video
// content only.
var youtubeMediaType = assets.MediaType("clip")

// searcher is the minimal internal interface the adapter depends on.
// Defining it private to this package lets the unit tests inject a
// stub without constructing a full *youtubesrc.Service (which carries
// a heavy scraper + repository + config chain).
//
// *youtubesrc.Service satisfies searcher via its public
// SearchByTopicWithFilter method.
type searcher interface {
	SearchByTopicWithFilter(ctx context.Context, query string, limit int, sortMode, publishedAfter string) (*youtubesrc.TopicSearchResponse, error)
}

// Adapter wraps a searcher (production: *youtubesrc.Service) and
// exposes it as a providers.Provider. The Adapter does NOT invent
// new search semantics: discovery logic stays in
// internal/sources/youtube, and this layer only normalises the
// boundary request/response shapes.
type Adapter struct {
	src searcher
}

// NewAdapter returns a production Adapter wrapping the given YouTube
// service. Composition-root responsibility to pass a fully wired
// *youtubesrc.Service; a nil pointer is tolerated by the runtime
// search guard but should be avoided.
func NewAdapter(src *youtubesrc.Service) *Adapter {
	return &Adapter{src: src}
}

// Name implements providers.Provider. Stable across calls.
// Registry.Register rejects empty names; "youtube" is the
// canonical identifier.
func (a *Adapter) Name() string { return "youtube" }

// Capabilities implements providers.Provider.
//
// CapabilityFetch is intentionally omitted: yt-dlp extraction +
// Drive upload is the channel-monitor's responsibility, NOT a
// Provider concern. Direct calls to Fetch always return a plain
// unrecoverable error (no sentinel — PR 3E forbids sentinels for
// "fetch unsupported").
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityVideo,
	}
}

// Search is the unified entry point replacing the four legacy
// SearchByTopic / SearchByTopicWithFilter / SearchTopicVideos /
// SearchTopicVideosWithFilter methods.
//
// Translation rules:
//
//   - req.Query               -> search term (required, non-empty).
//   - req.Limit               -> native limit. 0 / negative clamped
//                                to default 10; > 50 clamped to 50.
//   - req.PageToken           -> unsupported (no native cursor).
//   - req.TopicOnly           -> ignored (YouTube IS topic-based).
//   - req.Filters.Sort        -> native sortMode string.
//                                SortByRelevance + empty both map
//                                to the native default (""); other
//                                known modes pass through verbatim.
//   - req.Filters.PublishedAfter
//                              -> RFC3339 string forwarded to the
//                                underlying service. Nil = no filter.
//   - req.Filters.MediaTypes  -> not honoured; YouTube returns
//                                video content always. Producers
//                                may filter downstream by
//                                Candidate.MediaType.
//   - req.Filters.MinDuration / MaxDuration
//                              -> not honoured as a pre-filter;
//                                YouTube live search has no
//                                duration-range filter.
//
// Result scoring: the underlying service returns raw integers
// (SimilarityScore 0..100, FormatMatchPercent 0..100) which the
// adapter combines as similarity*70 + format*30 (max 10000) then
// normalizes to a [0,1] float. SourceRef is the YouTube VideoID
// so downstream ingest can reconstruct the canonical URL.
func (a *Adapter) Search(ctx context.Context, req providers.SearchRequest) ([]providers.Candidate, error) {
	if err := a.checkWired(); err != nil {
		return nil, err
	}
	if req.Query == "" {
		return nil, fmt.Errorf("youtube: query is required")
	}

	limit := clampLimit(req.Limit)
	sortMode := mapSortMode(req.Filters.Sort)
	publishedAfter := formatPublishedAfter(req.Filters.PublishedAfter)

	resp, err := a.src.SearchByTopicWithFilter(ctx, req.Query, limit, sortMode, publishedAfter)
	if err != nil {
		return nil, fmt.Errorf("youtube search: %w", err)
	}
	if resp == nil {
		return nil, nil
	}

	out := make([]providers.Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, providers.Candidate{
			SourceName:   a.Name(),
			SourceRef:    r.VideoID,
			Title:        r.Title,
			PreviewURL:   r.DirectLink,
			ThumbnailURL: r.ThumbnailURL,
			MediaType:    youtubeMediaType,
			Duration:     time.Duration(r.Duration * float64(time.Second)),
			PublishedAt:  parseYouTubeUploadDate(r.UploadDate),
			Score:        combinedScore(r.SimilarityScore, r.FormatMatchPercent),
		})
	}
	return out, nil
}

// Fetch implements providers.Provider.
//
// Not declared in Capabilities(): callers MUST NOT route through it
// (Registry.ByCapability(CapabilityFetch) will never return this
// adapter). A direct interface call returns a plain unrecoverable
// error; no sentinel is exported per PR 3E.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	_ = ctx
	_ = req
	return nil, errors.New("youtube: fetch not supported (CapabilityFetch not declared; download lives in channel-monitor)")
}

// ── helpers ─────────────────────────────────────────────────────────

// checkWired returns ErrSourceNotWired when the adapter has no
// usable searcher. Guards against both nil interface and typed-nil
// pointer (matches the registry's typed-nil convention).
func (a *Adapter) checkWired() error {
	if a.src == nil {
		return ErrSourceNotWired
	}
	rv := reflect.ValueOf(a.src)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrSourceNotWired
	}
	return nil
}

// clampLimit clamps the user request to the legacy service's
// accepted range. Mirrors the limits enforced inside
// SearchByTopicWithFilter so the Adapter never forwards out-of-range
// values.
func clampLimit(req int) int {
	switch {
	case req <= 0:
		return 10
	case req > 50:
		return 50
	default:
		return req
	}
}

// mapSortMode maps providers.SortMode to the native YouTube sort
// string. The native API accepts the empty string as "no
// preference" (relevance); SortByRelevance collapses onto the same
// path. Unknown modes pass through verbatim — the underlying service
// will reject them at the parser boundary if it doesn't recognise
// the value.
func mapSortMode(s providers.SortMode) string {
	switch s {
	case providers.SortByRelevance, "":
		return ""
	case providers.SortByNewest:
		return "newest"
	case providers.SortByOldest:
		return "oldest"
	case providers.SortByLongest:
		return "longest"
	case providers.SortByShortest:
		return "shortest"
	default:
		return string(s)
	}
}

// formatPublishedAfter formats a time pointer as RFC3339 UTC, or
// returns "" for nil. SearchByTopicWithFilter treats "" as "no
// filter".
func formatPublishedAfter(t *time.Time) string {
	if t == nil {
		return ""
	}
	return timeutil.FormatRFC3339(t.UTC())
}

// combinedScore normalises the underlying service's
// similarity*70 + format*30 score (0..10000) to a [0,1] float.
func combinedScore(similarity, format int) float64 {
	score := similarity*70 + format*30
	if score > 10000 {
		score = 10000
	}
	if score < 0 {
		score = 0
	}
	return float64(score) / 10000.0
}

// parseYouTubeUploadDate parses the YouTube YYYYMMDD-like upload
// date string and returns nil if empty or unparseable.
func parseYouTubeUploadDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := timeutil.ParseYouTubeUploadDate(s)
	if err != nil {
		return nil
	}
	return &t
}
