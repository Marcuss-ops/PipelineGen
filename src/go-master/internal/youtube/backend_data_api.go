package youtube

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

type DataAPIBackend struct {
	service *youtube.Service
}

func NewDataAPIBackend(ctx context.Context, apiKey string) (*DataAPIBackend, error) {
	service, err := youtube.NewService(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create youtube service: %w", err)
	}
	return &DataAPIBackend{service: service}, nil
}

func NewDataAPIBackendWithOAuth(ctx context.Context, clientID, clientSecret, tokenFile string) (*DataAPIBackend, error) {
	// Questo verrebbe usato se volessimo usare OAuth invece di API Key
	// Per ora implementiamo la struttura base
	return nil, fmt.Errorf("oauth not implemented in backend_data_api yet")
}

func (b *DataAPIBackend) GetVideo(ctx context.Context, videoID string) (*VideoInfo, error) {
	call := b.service.Videos.List([]string{"snippet", "contentDetails", "statistics"}).Id(videoID)
	resp, err := call.Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("video not found: %s", videoID)
	}

	item := resp.Items[0]
	duration, _ := time.ParseDuration(item.ContentDetails.Duration) // Semplificato, serve parsing ISO8601

	return &VideoInfo{
		ID:          item.Id,
		Title:       item.Snippet.Title,
		Description: item.Snippet.Description,
		ChannelID:   item.Snippet.ChannelId,
		Channel:     item.Snippet.ChannelTitle,
		UploadDate:  parseTime(item.Snippet.PublishedAt),
		Views:       int64(item.Statistics.ViewCount),
		Likes:       int64(item.Statistics.LikeCount),
		Duration:    duration,
	}, nil
}

func (b *DataAPIBackend) GetChannelUploadsPlaylistID(ctx context.Context, channelID string) (string, error) {
	call := b.service.Channels.List([]string{"contentDetails"}).Id(channelID)
	resp, err := call.Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if len(resp.Items) == 0 {
		return "", fmt.Errorf("channel not found: %s", channelID)
	}
	return resp.Items[0].ContentDetails.RelatedPlaylists.Uploads, nil
}

func (b *DataAPIBackend) GetPlaylistItems(ctx context.Context, playlistID string, limit int64) ([]SearchResult, error) {
	call := b.service.PlaylistItems.List([]string{"snippet", "contentDetails"}).
		PlaylistId(playlistID).
		MaxResults(limit)

	resp, err := call.Context(ctx).Do()
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, item := range resp.Items {
		results = append(results, SearchResult{
			ID:    item.ContentDetails.VideoId,
			Title: item.Snippet.Title,
			// Altri campi...
		})
	}
	return results, nil
}

// Stub per soddisfare l'interfaccia Client
func (b *DataAPIBackend) Download(ctx context.Context, req *DownloadRequest) (*DownloadResult, error) {
	return nil, fmt.Errorf("download not supported by Data API backend")
}
func (b *DataAPIBackend) DownloadAudio(ctx context.Context, req *AudioDownloadRequest) (*AudioDownloadResult, error) {
	return nil, fmt.Errorf("download not supported by Data API backend")
}
func (b *DataAPIBackend) Search(ctx context.Context, query string, opts *SearchOptions) ([]SearchResult, error) {
	return nil, nil
}
func (b *DataAPIBackend) GetChannelVideos(ctx context.Context, channelURL string, opts *ChannelOptions) ([]SearchResult, error) {
	return nil, nil
}
func (b *DataAPIBackend) GetTrending(ctx context.Context, region string, limit int) ([]SearchResult, error) {
	return nil, nil
}
func (b *DataAPIBackend) GetSubtitles(ctx context.Context, videoID string, lang string) (*SubtitleInfo, error) {
	return nil, nil
}
func (b *DataAPIBackend) GetTranscript(ctx context.Context, url string, lang string) (string, error) {
	return "", nil
}
func (b *DataAPIBackend) CheckAvailable(ctx context.Context) error {
	return nil
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
