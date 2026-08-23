package stockplan

import (
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"math"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

var (
	ErrYouTubeURLInvalid  = errors.New("invalid YouTube URL")
	ErrYouTubeURLNotVideo = errors.New("YouTube URL is not a video")
)

type YouTubeVideo struct {
	ID  string `json:"youtube_video_id"`
	URL string `json:"source_url"`
}

func ParseYouTubeURL(raw string) (YouTubeVideo, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return YouTubeVideo{}, fmt.Errorf("%w: %q", ErrYouTubeURLInvalid, raw)
	}
	h := strings.ToLower(u.Hostname())
	id := ""
	if h == "youtu.be" {
		id = strings.Trim(u.Path, "/")
	} else if h == "youtube.com" || h == "www.youtube.com" || strings.HasSuffix(h, ".youtube.com") {
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else {
			for _, p := range []string{"/shorts/", "/live/", "/embed/"} {
				if strings.HasPrefix(u.Path, p) {
					id = strings.Trim(strings.TrimPrefix(u.Path, p), "/")
					break
				}
			}
		}
		if id == "" {
			return YouTubeVideo{}, fmt.Errorf("%w: %s", ErrYouTubeURLNotVideo, u.Path)
		}
	} else {
		return YouTubeVideo{}, fmt.Errorf("%w: host %s", ErrYouTubeURLInvalid, h)
	}
	if len(id) < 6 || len(id) > 64 {
		return YouTubeVideo{}, fmt.Errorf("%w: video id", ErrYouTubeURLInvalid)
	}
	for _, r := range id {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return YouTubeVideo{}, fmt.Errorf("%w: video id", ErrYouTubeURLInvalid)
		}
	}
	return YouTubeVideo{ID: id, URL: "https://www.youtube.com/watch?v=" + id}, nil
}

type TranscriptCue struct {
	StartMs, EndMs int64
	Text           string
}
type HighlightCandidate struct {
	StartMs         int64    `json:"start_ms"`
	EndMs           int64    `json:"end_ms"`
	DurationMs      int64    `json:"duration_ms"`
	Text            string   `json:"transcript"`
	Keywords        []string `json:"keywords,omitempty"`
	SpeechDensity   float64  `json:"speech_density"`
	QuerySimilarity float64  `json:"query_similarity"`
	RelevanceScore  float64  `json:"relevance_score"`
	SelectionReason string   `json:"reason"`
}
type TranscriptSegmenter interface {
	Segment([]TranscriptCue, int64, int64) []HighlightCandidate
}
type transcriptSegmenter struct{}

func NewTranscriptSegmenter() TranscriptSegmenter { return transcriptSegmenter{} }
func (transcriptSegmenter) Segment(in []TranscriptCue, target, overlap int64) []HighlightCandidate {
	if target <= 0 || overlap < 0 || overlap >= target {
		return nil
	}
	c := append([]TranscriptCue(nil), in...)
	sort.SliceStable(c, func(i, j int) bool { return c[i].StartMs < c[j].StartMs })
	if len(c) == 0 {
		return nil
	}
	var out []HighlightCandidate
	for start, last := c[0].StartMs, c[len(c)-1].EndMs; start < last; start = start + target - overlap {
		end := start + target
		var text []string
		for _, x := range c {
			if x.EndMs > start && x.StartMs < end {
				text = append(text, strings.TrimSpace(x.Text))
			}
		}
		if s := strings.TrimSpace(strings.Join(text, " ")); s != "" {
			out = append(out, HighlightCandidate{StartMs: start, EndMs: end, DurationMs: target, Text: s})
		}
	}
	return out
}

type HighlightWeights struct{ Query, Subject, Language, Density, Diversity, Quality float64 }

func DefaultHighlightWeights() HighlightWeights {
	return HighlightWeights{.35, .20, .15, .10, .10, .10}
}

type HighlightSelector interface {
	Select([]HighlightCandidate, string, string, int, int64) []HighlightCandidate
}
type highlightSelector struct{ w HighlightWeights }

func NewHighlightSelector(w HighlightWeights) HighlightSelector {
	if w == (HighlightWeights{}) {
		w = DefaultHighlightWeights()
	}
	return &highlightSelector{w: w}
}
func (s *highlightSelector) Select(in []HighlightCandidate, query, subject string, limit int, gap int64) []HighlightCandidate {
	if limit <= 0 {
		return nil
	}
	q, sub := tokens(query), tokens(subject)
	a := append([]HighlightCandidate(nil), in...)
	for i := range a {
		c := &a[i]
		c.DurationMs = c.EndMs - c.StartMs
		if c.DurationMs <= 0 {
			continue
		}
		words := tokens(c.Text)
		c.SpeechDensity = clamp(float64(len(words)) / float64(max64(c.DurationMs/1000, 1)*3))
		c.QuerySimilarity = overlap(words, q)
		ss := overlap(words, sub)
		c.RelevanceScore = clamp(s.w.Query*c.QuerySimilarity + s.w.Subject*ss + s.w.Language*complete(c.Text) + s.w.Density*c.SpeechDensity + s.w.Quality*quality(*c))
		c.SelectionReason = fmt.Sprintf("query=%.2f subject=%.2f density=%.2f", c.QuerySimilarity, ss, c.SpeechDensity)
	}
	sort.SliceStable(a, func(i, j int) bool {
		if a[i].RelevanceScore == a[j].RelevanceScore {
			return a[i].StartMs < a[j].StartMs
		}
		return a[i].RelevanceScore > a[j].RelevanceScore
	})
	var out []HighlightCandidate
	for _, c := range a {
		if c.DurationMs <= 0 {
			continue
		}
		close := false
		for _, p := range out {
			if c.StartMs < p.EndMs+gap && p.StartMs < c.EndMs+gap {
				close = true
				break
			}
		}
		if !close {
			out = append(out, c)
		}
		if len(out) == limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

type HighlightRegistry struct{ selectors map[string]HighlightSelector }

func NewHighlightRegistry() *HighlightRegistry {
	r := &HighlightRegistry{map[string]HighlightSelector{}}
	_ = r.Register("youtube", NewHighlightSelector(DefaultHighlightWeights()))
	return r
}
func (r *HighlightRegistry) Register(provider string, s HighlightSelector) error {
	if r == nil || strings.TrimSpace(provider) == "" || s == nil {
		return errors.New("highlight registry: provider and selector are required")
	}
	r.selectors[strings.ToLower(provider)] = s
	return nil
}
func (r *HighlightRegistry) Resolve(provider string) (HighlightSelector, bool) {
	if r == nil {
		return nil, false
	}
	s, ok := r.selectors[strings.ToLower(strings.TrimSpace(provider))]
	return s, ok
}

type YouTubeStockRequest struct {
	Subject        string   `json:"subject"`
	YouTubeURLs    []string `json:"youtube_urls"`
	Query          string   `json:"query"`
	ClipsPerVideo  int      `json:"clips_per_video"`
	ClipDurationMs int64    `json:"clip_duration_ms"`
}

func (r YouTubeStockRequest) Validate() error {
	if len(r.YouTubeURLs) == 0 || r.ClipsPerVideo <= 0 || r.ClipDurationMs <= 0 {
		return errors.New("youtube stock request: invalid required fields")
	}
	for _, u := range r.YouTubeURLs {
		if _, e := ParseYouTubeURL(u); e != nil {
			return e
		}
	}
	return nil
}

type SelectedSegment struct {
	YouTubeVideoID  string  `json:"youtube_video_id"`
	SourceURL       string  `json:"source_url"`
	StartMs         int64   `json:"start_ms"`
	EndMs           int64   `json:"end_ms"`
	DurationMs      int64   `json:"duration_ms"`
	Transcript      string  `json:"transcript"`
	RelevanceScore  float64 `json:"relevance_score"`
	SelectionReason string  `json:"selection_reason"`
	SelectionBasis  string  `json:"selection_basis"`
	VisualVerified  bool    `json:"visual_verified"`
	CacheKey        string  `json:"cache_key"`
	LocalPath       string  `json:"local_path,omitempty"`
	AssetID         string  `json:"asset_id,omitempty"`
	LegacyFileMD5   string  `json:"legacy_file_md5,omitempty"`
	DriveLink       string  `json:"drive_link,omitempty"`
	QdrantPointID   string  `json:"qdrant_point_id,omitempty"`
	Status          string  `json:"status"`
}

func (s SelectedSegment) Validate() error {
	if s.YouTubeVideoID == "" || s.SourceURL == "" || s.StartMs < 0 || s.EndMs <= s.StartMs || s.DurationMs != s.EndMs-s.StartMs {
		return errors.New("selected segment: invalid identity, interval, or duration")
	}
	if math.IsNaN(s.RelevanceScore) || math.IsInf(s.RelevanceScore, 0) || s.RelevanceScore < 0 || s.RelevanceScore > 1 || s.SelectionBasis != "transcript" || s.VisualVerified {
		return errors.New("selected segment: invalid transcript-first selection contract")
	}
	if s.SelectionReason == "" || s.CacheKey == "" {
		return errors.New("selected segment: selection reason and cache key are required")
	}
	return nil
}

type YouTubeStockResult struct {
	VideosAnalyzed   int               `json:"videos_analyzed"`
	SelectedSegments []SelectedSegment `json:"selected_segments"`
}
type PartialDownloadPlan struct {
	VideoID        string `json:"youtube_video_id"`
	StartMs        int64  `json:"start_ms"`
	EndMs          int64  `json:"end_ms"`
	DurationMs     int64  `json:"duration_ms"`
	ProfileVersion string `json:"profile_version"`
}

func (p PartialDownloadPlan) Validate() error {
	if p.VideoID == "" || p.StartMs < 0 || p.EndMs <= p.StartMs || p.DurationMs != p.EndMs-p.StartMs {
		return errors.New("partial download plan: invalid interval or duration")
	}
	return nil
}
func (p PartialDownloadPlan) YTDLPSection() string {
	return fmt.Sprintf("*%.3f-%.3f", float64(p.StartMs)/1000, float64(p.EndMs)/1000)
}
func (p PartialDownloadPlan) CacheKey() string {
	h := digest.SHA256Bytes([]byte(fmt.Sprintf("youtube:%s:%d:%d:%s", p.VideoID, p.StartMs, p.EndMs, p.ProfileVersion)))
	return h
}
func tokens(s string) map[string]struct{} {
	o := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) > 2 {
			o[w] = struct{}{}
		}
	}
	return o
}
func overlap(a, b map[string]struct{}) float64 {
	if len(b) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if _, ok := b[w]; ok {
			n++
		}
	}
	return float64(n) / float64(len(b))
}
func complete(s string) float64 {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	if strings.HasSuffix(strings.TrimSpace(s), ".") || strings.HasSuffix(strings.TrimSpace(s), "!") || strings.HasSuffix(strings.TrimSpace(s), "?") {
		return 1
	}
	return .5
}
func quality(c HighlightCandidate) float64 {
	if c.DurationMs >= 1000 {
		return 1
	}
	return 0
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
