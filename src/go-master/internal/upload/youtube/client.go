// Package youtube provides YouTube API integration for Agent 5.
package youtube

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/api/youtube/v3"
	"google.golang.org/api/option"
	"golang.org/x/oauth2"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Client gestisce le operazioni YouTube
type Client struct {
	service *youtube.Service
}

// VideoMetadata metadata per upload YouTube
type VideoMetadata struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	CategoryID  string   `json:"categoryId"`
	Privacy     string   `json:"privacy"` // public, unlisted, private
	Language    string   `json:"language"`
}

// DefaultMetadata restituisce metadata di default
func DefaultMetadata() VideoMetadata {
	return VideoMetadata{
		CategoryID: "22", // People & Blogs
		Privacy:    "private",
		Language:   "it",
	}
}

// NewClient crea un nuovo client YouTube
func NewClient(ctx context.Context, tokenSource oauth2.TokenSource) (*Client, error) {
	service, err := youtube.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("failed to create YouTube service: %w", err)
	}

	return &Client{
		service: service,
	}, nil
}

// UploadVideo carica un video su YouTube
func (c *Client) UploadVideo(ctx context.Context, videoPath string, meta VideoMetadata) (string, error) {
	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("failed to open video: %w", err)
	}
	defer file.Close()

	video := &youtube.Video{
		Snippet: &youtube.VideoSnippet{
			Title:       meta.Title,
			Description: meta.Description,
			Tags:        meta.Tags,
			CategoryId:  meta.CategoryID,
			DefaultLanguage: meta.Language,
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus:     meta.Privacy,
			SelfDeclaredMadeForKids: false,
		},
	}

	result, err := c.service.Videos.Insert([]string{"snippet", "status"}, video).
		Media(file).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("YouTube upload failed: %w", err)
	}

	logger.Info("Uploaded to YouTube",
		zap.String("title", meta.Title),
		zap.String("id", result.Id),
		zap.String("privacy", meta.Privacy))

	return result.Id, nil
}

// GetVideoStatus ottiene lo stato di elaborazione
func (c *Client) GetVideoStatus(ctx context.Context, videoID string) (string, error) {
	response, err := c.service.Videos.List([]string{"processingDetails", "status"}).
		Id(videoID).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("failed to get video status: %w", err)
	}

	if len(response.Items) == 0 {
		return "", fmt.Errorf("video not found: %s", videoID)
	}

	return response.Items[0].ProcessingDetails.ProcessingStatus, nil
}

// IsVideoProcessed verifica se il video è stato processato
func (c *Client) IsVideoProcessed(ctx context.Context, videoID string) (bool, error) {
	status, err := c.GetVideoStatus(ctx, videoID)
	if err != nil {
		return false, err
	}

	return status == "succeeded" || status == "", nil
}

// UpdateVideoMetadata aggiorna i metadata di un video
func (c *Client) UpdateVideoMetadata(ctx context.Context, videoID string, meta VideoMetadata) error {
	video := &youtube.Video{
		Id: videoID,
		Snippet: &youtube.VideoSnippet{
			Title:       meta.Title,
			Description: meta.Description,
			Tags:        meta.Tags,
			CategoryId:  meta.CategoryID,
		},
	}

	_, err := c.service.Videos.Update([]string{"snippet"}, video).
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("failed to update video: %w", err)
	}

	logger.Info("Updated YouTube video metadata", zap.String("video_id", videoID))
	return nil
}

// SetVideoPrivacy imposta la privacy di un video
func (c *Client) SetVideoPrivacy(ctx context.Context, videoID, privacy string) error {
	video := &youtube.Video{
		Id: videoID,
		Status: &youtube.VideoStatus{
			PrivacyStatus: privacy,
		},
	}

	_, err := c.service.Videos.Update([]string{"status"}, video).
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("failed to set video privacy: %w", err)
	}

	logger.Info("Updated YouTube video privacy", zap.String("video_id", videoID), zap.String("privacy", privacy))
	return nil
}

// DeleteVideo elimina un video
func (c *Client) DeleteVideo(ctx context.Context, videoID string) error {
	err := c.service.Videos.Delete(videoID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete video: %w", err)
	}

	logger.Info("Deleted YouTube video", zap.String("video_id", videoID))
	return nil
}