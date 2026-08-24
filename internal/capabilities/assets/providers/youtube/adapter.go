// Package youtube adapts internal/sources/youtube.Service to the
// canonical providers.SearchProvider contract in
// internal/capabilities/assets/providers.
//
// Spec: Agent 3 / PR 3F — single entry point replaces the four legacy
// SearchByTopic / SearchByTopicWithFilter / SearchTopicVideos /
// SearchTopicVideosWithFilter entry points at the application-layer
// boundary. The wrapper duplicates are removed in the same commit;
// callers route through providers.SearchProvider instead.
//
// CapabilityFetch is intentionally NOT declared: YouTube asset
// download lives in the channel-monitor / clipresolver pipeline
// (yt-dlp extraction + Drive upload) which is out of scope for the
// Provider contract. As a result this adapter ONLY satisfies
// SearchProvider — it has no Fetch method and the registry must
// not return it for ByCapability(CapabilityFetch).
package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	// DTOs (VideoCutRequest/Result, DownloaderMetadata, etc.) live in ports/.
	// TopicSearchResponse/Result and Service stay top-level.
	youtubesrc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	youtubesrcports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time assertions: *Adapter satisfies both SearchProvider and
// FetchProvider. Catches interface drift at build time.
var (
	_ providers.SearchProvider = (*Adapter)(nil)
	_ providers.FetchProvider  = (*Adapter)(nil)
)

// ErrSourceNotWired is returned by Search when the Adapter has no
// underlying searcher wired (nil interface OR typed-nil pointer).
var ErrSourceNotWired = errors.New("youtube adapter: source not wired")

// youtubeMediaType is the canonical MediaType assigned to every
// YouTube search candidate. "clip" is an asset kind, never a media type.
var youtubeMediaType = asset.MediaType("video")

// searcher is the minimal internal interface the adapter depends on
// for Search. Defining it private to this package lets the unit tests
// inject a stub without constructing a full *youtubesrc.Service.
//
// *youtubesrc.Service satisfies searcher via its public
// SearchByTopicWithFilter method.
type searcher interface {
	SearchByTopicWithFilter(ctx context.Context, query string, limit int, sortMode, publishedAfter string) (*youtubesrc.TopicSearchResponse, error)
}

// fetcher is the minimal internal interface for Fetch (download).
// Separate from searcher so providers that only fetch (no search)
// can implement it without pulling in search dependencies.
//
// *youtubesrc.Service satisfies fetcher via its public
// DownloadAndCut method (added Punto 6).
type fetcher interface {
	DownloadAndCut(ctx context.Context, req youtubesrcports.VideoCutRequest) (*youtubesrcports.VideoCutResult, error)
}

// Adapter wraps a searcher + fetcher (production: both are the same
// *youtubesrc.Service) and exposes them as a providers.Provider.
// The Adapter does NOT invent new semantics: discovery logic stays
// in internal/sources/youtube, and this layer only normalises the
// boundary request/response shapes.
type Adapter struct {
	src     searcher
	fetcher fetcher
}

// NewAdapter returns a production Adapter wrapping the given YouTube
// service as both searcher and fetcher. Composition-root responsibility
// to pass a fully wired *youtubesrc.Service; a nil pointer is tolerated
// by the runtime guards but should be avoided.
func NewAdapter(src *youtubesrc.Service) *Adapter {
	return &Adapter{src: src, fetcher: src}
}

// Name implements providers.Provider. Stable across calls.
// Registry.Register rejects empty names; "youtube" is the
// canonical identifier.
func (a *Adapter) Name() string { return "youtube" }

// Capabilities implements providers.Provider.
//
// CapabilityFetch is now declared: the adapter exposes
// yt-dlp download + segment extraction via Fetch().
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityFetch,
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
//     to default 10; > 50 clamped to 50.
//   - req.TopicOnly           -> ignored (YouTube IS topic-based).
//   - req.Filters.Sort        -> native sortMode string.
//     SortByRelevance + empty both map
//     to the native default (""); other
//     known modes pass through verbatim.
//   - req.Filters.PublishedAfter
//     -> RFC3339 string forwarded to the
//     underlying service. Nil = no filter.
//   - req.Filters.MediaTypes  -> not honoured; YouTube returns
//     video content always. Producers
//     may filter downstream by
//     Candidate.MediaType.
//   - req.Filters.MinDuration / MaxDuration
//     -> not honoured as a pre-filter;
//     YouTube live search has no
//     duration-range filter.
//
// Result scoring: the underlying service returns raw integers
// (SimilarityScore 0..100, FormatMatchPercent 0..100) which the
// adapter combines as similarity*70 + format*30 (max 10000) then
// normalizes to a [0,1] float. SourceRef is the YouTube VideoID
// so downstream ingest can reconstruct the canonical URL.
//
// Pagination: NextPageToken in the returned SearchResult is always
// empty — YouTube has no native cursor. Callers treat empty as
// "no more pages available".
func (a *Adapter) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if err := a.checkWired(); err != nil {
		return providers.SearchResult{}, err
	}
	if req.Query == "" {
		return providers.SearchResult{}, fmt.Errorf("youtube: query is required")
	}

	limit := clampLimit(req.Limit)
	sortMode := mapSortMode(req.Filters.Sort)
	publishedAfter := formatPublishedAfter(req.Filters.PublishedAfter)

	resp, err := a.src.SearchByTopicWithFilter(ctx, req.Query, limit, sortMode, publishedAfter)
	if err != nil {
		return providers.SearchResult{}, fmt.Errorf("youtube search: %w", err)
	}
	if resp == nil {
		return providers.SearchResult{}, nil
	}

	candidates := make([]providers.Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		candidates = append(candidates, providers.Candidate{
			SourceName:   a.Name(),
			SourceRef:    r.VideoID,
			Title:        r.Title,
			PreviewURL:   r.DirectLink,
			ThumbnailURL: r.ThumbnailURL,
			MediaType:    youtubeMediaType,
			Duration:     time.Duration(float64(r.Duration) * float64(time.Second)),
			PublishedAt:  parseYouTubeUploadDate(r.UploadDate),
			Score:        combinedScore(r.SimilarityScore, r.FormatMatchPercent),
		})
	}
	return providers.SearchResult{Candidates: candidates}, nil
}

// Fetch implements providers.FetchProvider.
//
// Translation rules:
//
//   - req.SourceRef           -> YouTube URL (required).
//   - req.SegmentStart/.End   -> optional segment bounds.
//     0 for both means "full video" (downloads entire content).
//   - req.DestinationID       -> not used; output lands in a temp dir.
//     The caller is responsible for subsequent upload.
//
// The returned FetchedAsset carries:
//   - Asset: canonical representation with metadata (title, duration,
//     tags, language, etc.) sourced from yt-dlp.
//   - LocalPath: absolute path to the downloaded + cut file.
//   - Bytes: file size in bytes.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	if err := a.checkFetchWired(); err != nil {
		return nil, err
	}
	if req.SourceRef == "" {
		return nil, fmt.Errorf("youtube fetch: SourceRef (URL) is required")
	}

	// Build the YouTubeCutRequest from the canonical FetchRequest.
	//
	// Segment extraction:
	//   - SegmentStart > 0 && SegmentEnd > SegmentStart: download that
	//     exact segment via yt-dlp DownloadSections.
	//   - Both 0: download the full video by setting a sentinel
	//     duration of 86400s (24h). yt-dlp clips to the actual video
	//     duration internally, so this effectively means "full video".
	var startSec, durationSec float64
	if req.SegmentStart > 0 || req.SegmentEnd > 0 {
		startSec = req.SegmentStart.Seconds()
		if req.SegmentEnd > req.SegmentStart {
			durationSec = (req.SegmentEnd - req.SegmentStart).Seconds()
		}
	} else {
		// Full video: sentinel duration so yt-dlp downloads everything.
		durationSec = 86400
	}

	safeName := req.AssetID
	if safeName == "" {
		safeName = fmt.Sprintf("yt_fetch_%d", time.Now().UnixNano())
	}

	cutReq := youtubesrcports.VideoCutRequest{
		URL:        req.SourceRef,
		VideoID:    req.AssetID,
		Start:      startSec,
		Duration:   durationSec,
		OutputName: safeName,
		KeepAudio:  !req.NoAudio, // inverted: NoAudio=true → KeepAudio=false → ffmpeg strips audio
		// Registering a YouTube clip is also the cut boundary. Normalize at
		// this seam so every persisted/uploaded segment has the canonical
		// delivery profile, independent of the source video's format.
		Normalize: true,
		Strategy:  "replace",
	}

	result, err := a.fetcher.DownloadAndCut(ctx, cutReq)
	if err != nil {
		return nil, fmt.Errorf("youtube fetch: %w", err)
	}

	// Resolve file size from disk
	var byteSize int64
	if fi, statErr := os.Stat(result.LocalPath); statErr == nil {
		byteSize = fi.Size()
	}

	// Build the canonical Asset from video metadata.
	// Metadata can be nil when the pipeline returns a cached clip
	// (Drive cache hit — no yt-dlp metadata fetch).
	meta := result.Metadata
	assetName := safeName
	if meta != nil && meta.Title != "" {
		assetName = meta.Title
	}
	assetRecord := &asset.Asset{
		ID:        req.AssetID,
		Name:      assetName,
		Source:    "youtube",
		SourceURL: req.SourceRef,
		MediaType: asset.MediaType("video"),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	assetRecord.SetLocalPath(result.LocalPath)

	if meta != nil {
		assetRecord.SetYouTubeTitle(meta.Title)
		assetRecord.SetYouTubeUploader(meta.Uploader)
		assetRecord.SetYouTubeDescription(meta.Description)
		assetRecord.SetYouTubeLanguage(meta.Language)
		if meta.UploadDate != "" {
			assetRecord.SetYouTubeUploadDate(meta.UploadDate)
		}
		if meta.Duration > 0 {
			assetRecord.Duration = time.Duration(meta.Duration * float64(time.Second))
		}
	}

	return &providers.FetchedAsset{
		Asset:     assetRecord,
		LocalPath: result.LocalPath,
		FetchedAt: time.Now(),
		Bytes:     byteSize,
	}, nil
}

// checkFetchWired returns an error when the adapter has no usable fetcher.
func (a *Adapter) checkFetchWired() error {
	if a.fetcher == nil {
		return fmt.Errorf("youtube fetch: fetcher not wired")
	}
	rv := reflect.ValueOf(a.fetcher)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return fmt.Errorf("youtube fetch: fetcher not wired")
	}
	return nil
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
