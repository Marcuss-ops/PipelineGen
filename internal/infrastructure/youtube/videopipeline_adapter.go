package youtube

import (
	"context"

	youtubeapp "github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
)

// VideoPipelineAdapter wraps videomuscles.Pipeline and converts between
// application-layer DTOs (VideoCutRequest/VideoCutResult) and the concrete
// infrastructure types (videomuscles.YouTubeCutRequest/YouTubeCutResult).
type VideoPipelineAdapter struct {
	inner *videomuscles.Pipeline
}

// NewVideoPipelineAdapter creates a new adapter.
func NewVideoPipelineAdapter(p *videomuscles.Pipeline) *VideoPipelineAdapter {
	return &VideoPipelineAdapter{inner: p}
}

// DownloadAndCutYouTubeVideo implements youtube.VideoPipeline.
func (a *VideoPipelineAdapter) DownloadAndCutYouTubeVideo(ctx context.Context, req youtubeapp.VideoCutRequest) (*youtubeapp.VideoCutResult, error) {
	infraReq := videomuscles.YouTubeCutRequest{
		URL:               req.URL,
		VideoID:           req.VideoID,
		Start:             req.Start,
		Duration:          req.Duration,
		OutputName:        req.OutputName,
		ForceKeyframes:    req.ForceKeyframes,
		KeepAudio:         req.KeepAudio,
		Normalize:         req.Normalize,
		Strategy:          req.Strategy,
		OutputDir:         req.OutputDir,
		PreDownloadedPath: req.PreDownloadedPath,
	}

	infraResult, err := a.inner.DownloadAndCutYouTubeVideo(ctx, infraReq)
	if err != nil {
		return nil, err
	}

	var meta *youtubeapp.YouTubeMetadataPort
	if infraResult != nil && infraResult.Metadata != nil {
		ym := infraResult.Metadata
		meta = &youtubeapp.YouTubeMetadataPort{
			ID:           ym.ID,
			Title:        ym.Title,
			Description:  ym.Description,
			Language:     ym.Language,
			Uploader:     ym.Uploader,
			UploadDate:   ym.UploadDate,
			ViewCount:    ym.ViewCount,
			Duration:     ym.Duration,
			ThumbnailURL: ym.ThumbnailURL,
			Categories:   ym.Categories,
			Tags:         ym.Tags,
		}
		for _, ch := range ym.Chapters {
			meta.Chapters = append(meta.Chapters, youtubeapp.VideoChapterPort{
				Title:     ch.Title,
				StartTime: ch.StartTime,
				EndTime:   ch.EndTime,
			})
		}
	}

	var localPath string
	if infraResult != nil {
		localPath = infraResult.LocalPath
	}

	return &youtubeapp.VideoCutResult{
		LocalPath: localPath,
		Metadata:  meta,
	}, nil
}
