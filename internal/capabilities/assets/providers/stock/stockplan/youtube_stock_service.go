package stockplan

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strings"
	"sync"
	"time"

	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

var (
	ErrTranscriptUnavailable = errors.New("TRANSCRIPT_UNAVAILABLE")
	ErrYouTubeMetadata       = errors.New("YOUTUBE_METADATA_UNAVAILABLE")
)

type MetadataProvider interface {
	GetVideoInfo(context.Context, string) (*youtubeports.DownloaderMetadata, error)
}

type Transcript struct {
	Cues     []TranscriptCue
	Language string
	Source   string
	Hash     string
}

type TranscriptProvider interface {
	AcquireStockTranscript(context.Context, string, int64) (*Transcript, error)
}

type Extractor interface {
	Extract(context.Context, *youtubedto.ExtractRequest) (*youtubedto.ExtractResponse, error)
}

// StockService owns the transcript-first YouTube stock use case. The final
// clip work is delegated to the existing YouTube extractor, preserving its
// atomic media_assets/outbox writer and canonical Drive publisher.
type StockService struct {
	metadata   MetadataProvider
	transcript TranscriptProvider
	extractor  Extractor
	selector   HighlightSelector
	segmenter  TranscriptSegmenter
	folderID   string
	mu         sync.Mutex
	metaCache  map[string]*youtubeports.DownloaderMetadata
	textCache  map[string]*Transcript
}

func NewYouTubeStockService(metadata MetadataProvider, transcript TranscriptProvider, extractor Extractor, folderID string) (*StockService, error) {
	if metadata == nil || transcript == nil || extractor == nil {
		return nil, errors.New("youtube stock: metadata, transcript and extractor ports are required")
	}
	return &StockService{
		metadata: metadata, transcript: transcript, extractor: extractor,
		selector:  NewHighlightSelector(DefaultHighlightWeights()),
		segmenter: NewTranscriptSegmenter(), folderID: folderID,
		metaCache: make(map[string]*youtubeports.DownloaderMetadata),
		textCache: make(map[string]*Transcript),
	}, nil
}

// Plan performs metadata → transcript → selection without downloading or
// persisting media. It is safe for callers that need to rank candidates
// across providers before materialization.
func (s *StockService) Plan(ctx context.Context, req YouTubeStockRequest) (*YouTubeStockResult, error) {
	if s == nil {
		return nil, errors.New("youtube stock: service is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	out := &YouTubeStockResult{}
	languageByVideo := make(map[string]string)
	titleByVideo := make(map[string]string)
	seenURLs := make(map[string]struct{}, len(req.YouTubeURLs))
	for _, rawURL := range req.YouTubeURLs {
		video, parseErr := ParseYouTubeURL(rawURL)
		if parseErr != nil {
			return nil, parseErr
		}
		if _, seen := seenURLs[video.ID]; seen {
			continue
		}
		seenURLs[video.ID] = struct{}{}
		err := error(nil)
		if err != nil {
			return nil, err
		}
		meta, err := s.getMetadata(ctx, video)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrYouTubeMetadata, err)
		}
		durationMs := int64(meta.Duration * 1000)
		if durationMs <= 0 {
			return nil, fmt.Errorf("%w: duration for %s is not positive", ErrYouTubeMetadata, video.ID)
		}
		if meta.Title == "" {
			return nil, fmt.Errorf("%w: title for %s is empty", ErrYouTubeMetadata, video.ID)
		}
		if meta.LiveStatus == "is_live" || meta.LiveStatus == "is_upcoming" {
			return nil, fmt.Errorf("%w: live stream %s is not supported", ErrYouTubeMetadata, video.ID)
		}
		transcript, err := s.getTranscript(ctx, video, durationMs)
		if err != nil {
			return nil, err
		}
		candidates := s.segmenter.Segment(transcript.Cues, req.ClipDurationMs, 0)
		for i := range candidates {
			if candidates[i].EndMs > durationMs {
				candidates[i].EndMs = durationMs
				candidates[i].DurationMs = candidates[i].EndMs - candidates[i].StartMs
			}
		}
		fullCandidates := candidates[:0]
		for _, candidate := range candidates {
			if candidate.DurationMs == req.ClipDurationMs {
				fullCandidates = append(fullCandidates, candidate)
			}
		}
		selected := s.selector.Select(fullCandidates, req.Query, req.Subject, req.ClipsPerVideo, 5000)
		if len(selected) != req.ClipsPerVideo {
			return nil, fmt.Errorf("youtube stock: only %d highlights selected for %s", len(selected), video.ID)
		}
		languageByVideo[video.ID] = transcript.Language
		titleByVideo[video.ID] = meta.Title
		for _, c := range selected {
			seg := SelectedSegment{
				YouTubeVideoID: video.ID, SourceURL: video.URL, StartMs: c.StartMs, EndMs: c.EndMs,
				DurationMs: c.DurationMs, Transcript: c.Text, RelevanceScore: c.RelevanceScore,
				SelectionReason: c.SelectionReason, SelectionBasis: "transcript", VisualVerified: false,
				CacheKey: (PartialDownloadPlan{VideoID: video.ID, StartMs: c.StartMs, EndMs: c.EndMs, DurationMs: c.DurationMs, ProfileVersion: "youtube-stock-v1"}).CacheKey(),
				Status:   "SEGMENTS_PLANNED",
			}
			if err := seg.Validate(); err != nil {
				return nil, err
			}
			out.SelectedSegments = append(out.SelectedSegments, seg)
		}
		out.VideosAnalyzed++
	}

	sort.SliceStable(out.SelectedSegments, func(i, j int) bool {
		if out.SelectedSegments[i].YouTubeVideoID == out.SelectedSegments[j].YouTubeVideoID {
			return out.SelectedSegments[i].StartMs < out.SelectedSegments[j].StartMs
		}
		return out.SelectedSegments[i].YouTubeVideoID < out.SelectedSegments[j].YouTubeVideoID
	})
	return out, nil
}

// Materialize downloads and persists already-selected segments through the
// canonical YouTube extractor. It does not re-run metadata, transcript, or
// highlight selection.
func (s *StockService) Materialize(ctx context.Context, req YouTubeStockRequest, planned *YouTubeStockResult) (*YouTubeStockResult, error) {
	if s == nil {
		return nil, errors.New("youtube stock: service is nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if planned == nil {
		return nil, errors.New("youtube stock: planned result is nil")
	}
	out := &YouTubeStockResult{
		VideosAnalyzed:   planned.VideosAnalyzed,
		SelectedSegments: append([]SelectedSegment(nil), planned.SelectedSegments...),
	}
	if len(out.SelectedSegments) == 0 {
		return out, nil
	}
	languageByVideo := make(map[string]string)
	titleByVideo := make(map[string]string)
	seenURLs := make(map[string]struct{}, len(req.YouTubeURLs))
	for _, rawURL := range req.YouTubeURLs {
		video, parseErr := ParseYouTubeURL(rawURL)
		if parseErr != nil {
			return nil, parseErr
		}
		if _, seen := seenURLs[video.ID]; seen {
			continue
		}
		seenURLs[video.ID] = struct{}{}
		err := error(nil)
		if err != nil {
			return nil, err
		}
		meta, err := s.getMetadata(ctx, video)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrYouTubeMetadata, err)
		}
		transcript, err := s.getTranscript(ctx, video, int64(meta.Duration*1000))
		if err != nil {
			return nil, err
		}
		languageByVideo[video.ID] = transcript.Language
		titleByVideo[video.ID] = meta.Title
	}
	return s.materializePlanned(ctx, req, out, languageByVideo, titleByVideo)
}

// Run preserves the legacy standalone behavior by composing Plan and
// Materialize.
func (s *StockService) Run(ctx context.Context, req YouTubeStockRequest) (*YouTubeStockResult, error) {
	planned, err := s.Plan(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.materializePlanned(ctx, req, planned, nil, nil)
}

func (s *StockService) materializePlanned(ctx context.Context, req YouTubeStockRequest, out *YouTubeStockResult, languageByVideo, titleByVideo map[string]string) (*YouTubeStockResult, error) {
	if languageByVideo == nil {
		languageByVideo = make(map[string]string)
	}
	if titleByVideo == nil {
		titleByVideo = make(map[string]string)
	}
	byVideo := make(map[string][]SelectedSegment)
	for _, seg := range out.SelectedSegments {
		byVideo[seg.YouTubeVideoID] = append(byVideo[seg.YouTubeVideoID], seg)
	}
	videoIDs := make([]string, 0, len(byVideo))
	for videoID := range byVideo {
		videoIDs = append(videoIDs, videoID)
	}
	sort.Strings(videoIDs)
	for _, videoID := range videoIDs {
		segments := byVideo[videoID]
		uncached := segments[:0]
		for _, segment := range segments {
			if segment.Status == "SEGMENTS_PLANNED" || strings.TrimSpace(segment.AssetID) == "" || strings.TrimSpace(segment.DriveLink) == "" {
				uncached = append(uncached, segment)
			}
		}
		if len(uncached) == 0 {
			continue
		}
		segments = uncached
		// A planned segment carrying canonical persistence fields is a durable
		// cache hit, even when its status was normalized by a previous job.
		// The filter above intentionally accepts only genuinely unmaterialized
		// segments, so retries and cross-job replays do not call the extractor.
		extractReq := &youtubedto.ExtractRequest{
			URL:         "https://www.youtube.com/watch?v=" + videoID,
			Strategy:    youtubedto.StrategyYouTubeStockPartial,
			Destination: &youtubedto.DestinationRequest{FolderID: s.folderID, Group: req.Subject, CreateSubfolder: true},
		}
		for i, seg := range segments {
			extractReq.Segments = append(extractReq.Segments, youtubedto.Segment{
				Start: clockString(seg.StartMs), End: clockString(seg.EndMs), Name: fmt.Sprintf("%s-%02d", videoID, i+1),
				SourceTitle: titleByVideo[videoID], Summary: seg.SelectionReason,
				Texts: []youtubedto.LocalizedClipText{{LanguageCode: languageByVideo[videoID], Transcript: seg.Transcript, SourceType: "youtube_subtitle", IsOriginal: true}},
			})
		}
		resp, err := s.extractor.Extract(ctx, extractReq)
		if err != nil {
			return nil, fmt.Errorf("youtube stock: extract %s: %w", videoID, err)
		}
		if resp == nil || !resp.OK {
			if resp != nil && resp.Error != "" {
				return nil, fmt.Errorf("youtube stock: extract %s: %s", videoID, resp.Error)
			}
			return nil, fmt.Errorf("youtube stock: extract %s failed", videoID)
		}
		if len(resp.Items) != len(segments) {
			return nil, fmt.Errorf("youtube stock: extract %s returned %d items, want %d", videoID, len(resp.Items), len(segments))
		}
		for i := range segments {
			segments[i].LocalPath = resp.Items[i].LocalPath
			segments[i].DriveLink = resp.Items[i].DriveLink
			segments[i].Status = resp.Items[i].Status
			segments[i].AssetID = resp.Items[i].ID
			segments[i].LegacyFileMD5 = resp.Items[i].LegacyFileMD5
		}
		for i := range out.SelectedSegments {
			if out.SelectedSegments[i].YouTubeVideoID != videoID {
				continue
			}
			for _, done := range segments {
				if out.SelectedSegments[i].StartMs == done.StartMs && out.SelectedSegments[i].EndMs == done.EndMs {
					out.SelectedSegments[i] = done
					break
				}
			}
		}
	}
	return out, nil
}

func (s *StockService) getMetadata(ctx context.Context, video YouTubeVideo) (*youtubeports.DownloaderMetadata, error) {
	s.mu.Lock()
	if cached := s.metaCache[video.ID]; cached != nil {
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()
	meta, err := s.metadata.GetVideoInfo(ctx, video.URL)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.metaCache[video.ID] = meta
	s.mu.Unlock()
	return meta, nil
}

func (s *StockService) getTranscript(ctx context.Context, video YouTubeVideo, durationMs int64) (*Transcript, error) {
	s.mu.Lock()
	if cached := s.textCache[video.ID]; cached != nil {
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()
	transcript, err := s.transcript.AcquireStockTranscript(ctx, video.ID, durationMs)
	if err != nil {
		return nil, err
	}
	if transcript == nil || len(transcript.Cues) == 0 || transcript.Hash == "" {
		return nil, fmt.Errorf("%w: %s", ErrTranscriptUnavailable, video.ID)
	}
	s.mu.Lock()
	s.textCache[video.ID] = transcript
	s.mu.Unlock()
	return transcript, nil
}

func clockString(ms int64) string { return time.UnixMilli(ms).UTC().Format("15:04:05") }

func TranscriptFromBundle(bundle *detail.ResolvedTextBundle) *Transcript {
	if bundle == nil || bundle.IsEmpty() {
		return nil
	}
	sum := digest.SHA256Bytes([]byte(bundle.PlainText))
	return &Transcript{
		Cues: bundleToCues(bundle.Cues), Language: bundle.LanguageCode,
		Source: string(bundle.SourceType), Hash: sum,
	}
}

func bundleToCues(in []detail.TimedCue) []TranscriptCue {
	out := make([]TranscriptCue, 0, len(in))
	for _, cue := range in {
		out = append(out, TranscriptCue{StartMs: cue.StartMs, EndMs: cue.EndMs, Text: cue.Text})
	}
	return out
}
