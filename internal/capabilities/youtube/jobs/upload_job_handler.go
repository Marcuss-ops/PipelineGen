package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	jobyoutube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// UploadPublishingTarget mirrors the Velox publishing_target envelope: the
// workspace scope plus the opaque InstaEdit channel destination.
type UploadPublishingTarget struct {
	WorkspaceID   int64  `json:"workspace_id"`
	Type          string `json:"type"`
	DestinationID string `json:"destination_id,omitempty"`
	GroupID       int64  `json:"group_id,omitempty"`
	Platform      string `json:"platform,omitempty"`
}

// UploadPublicationOutputRef selects the rendered output variant.
type UploadPublicationOutputRef struct {
	VariantID    string `json:"variant_id,omitempty"`
	ArtifactRole string `json:"artifact_role,omitempty"`
}

// UploadPublicationMetadata carries the provider-independent YouTube metadata.
type UploadPublicationMetadata struct {
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Privacy     string   `json:"privacy,omitempty"`
	MadeForKids *bool    `json:"made_for_kids,omitempty"`
}

// UploadPublication is one publication of the rendered video. This job
// publishes a single video to a single channel, so at most one publication is
// accepted.
type UploadPublication struct {
	PublicationID string                      `json:"publication_id"`
	OutputRef     *UploadPublicationOutputRef `json:"output_ref,omitempty"`
	Metadata      UploadPublicationMetadata   `json:"metadata,omitempty"`
}

// UploadJobPayload is the wire payload of a youtube.upload job.
//
// The canonical destination is publishing_target.destination_id, an opaque
// InstaEdit channel destination (velox_destination_id from the catalog).
// platform_account_id remains supported as a legacy shortcut.
type UploadJobPayload struct {
	PlatformAccountID int64                   `json:"platform_account_id,omitempty"`
	PublishingTarget  *UploadPublishingTarget `json:"publishing_target,omitempty"`
	Publications      []UploadPublication     `json:"publications,omitempty"`
	VideoPath         string                  `json:"video_path,omitempty"`
	VideoURL          string                  `json:"video_url,omitempty"`
	Title             string                  `json:"title,omitempty"`
	Description       string                  `json:"description,omitempty"`
	Tags              []string                `json:"tags,omitempty"`
	PrivacyStatus     string                  `json:"privacy_status,omitempty"`
	MadeForKids       bool                    `json:"made_for_kids,omitempty"`
}

// UploadJobHandler publishes a rendered video through InstaEdit.
type UploadJobHandler struct {
	publisher youtubeports.YouTubeUploadPublisher
	log       *zap.Logger
}

func NewUploadJobHandler(publisher youtubeports.YouTubeUploadPublisher, log *zap.Logger) *UploadJobHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &UploadJobHandler{publisher: publisher, log: log}
}

func (h *UploadJobHandler) HandleJob(ctx context.Context, j *kerneljob.Job, tools *jobtools.JobTools) (map[string]any, error) {
	if h == nil || h.publisher == nil {
		return nil, fmt.Errorf("youtube.upload: publisher is not configured")
	}
	if j == nil {
		return nil, fmt.Errorf("youtube.upload: job is nil")
	}
	var payload UploadJobPayload
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("youtube.upload: decode payload: %w", err)
	}
	req, err := payload.uploadRequest(j.ID)
	if err != nil {
		return nil, fmt.Errorf("youtube.upload: %w", err)
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(5, "validating InstaEdit channel and video")
	}

	result, err := h.publisher.PublishYouTube(ctx, req)
	if err != nil {
		h.log.Warn("youtube.upload failed",
			zap.String("job_id", j.ID),
			zap.Int64("platform_account_id", req.PlatformAccountID),
			zap.String("destination_id", req.DestinationID),
			zap.Error(err))
		return nil, fmt.Errorf("youtube.upload: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("youtube.upload: publisher returned no result")
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "YouTube video published")
	}
	videoURL := result.VideoURL
	if videoURL == "" && result.VideoID != "" {
		videoURL = "https://youtu.be/" + result.VideoID
	}
	platformAccountID := result.PlatformAccountID
	if platformAccountID <= 0 {
		platformAccountID = req.PlatformAccountID
	}
	h.log.Info("youtube.upload completed",
		zap.String("job_id", j.ID),
		zap.Int64("post_id", result.PostID),
		zap.Int64("target_id", result.TargetID),
		zap.String("video_id", result.VideoID),
		zap.String("status", result.Status),
		zap.String("destination_id", result.DestinationID))
	return map[string]any{
		"ok":                  true,
		"job_type":            jobyoutube.TypeUpload,
		"post_id":             result.PostID,
		"target_id":           result.TargetID,
		"video_id":            result.VideoID,
		"video_url":           videoURL,
		"status":              result.Status,
		"privacy_status":      result.PrivacyStatus,
		"workspace_id":        result.WorkspaceID,
		"destination_id":      result.DestinationID,
		"publication_id":      result.PublicationID,
		"channel_id":          result.ChannelID,
		"channel_name":        result.ChannelName,
		"platform_account_id": platformAccountID,
	}, nil
}

// uploadRequest validates the payload and merges the canonical
// publications[].metadata with the legacy top-level fields. The canonical
// fields win when both are present.
func (p UploadJobPayload) uploadRequest(jobID string) (youtubeports.YouTubeUploadRequest, error) {
	req := youtubeports.YouTubeUploadRequest{
		PlatformAccountID: p.PlatformAccountID,
		VideoPath:         strings.TrimSpace(p.VideoPath),
		VideoURL:          strings.TrimSpace(p.VideoURL),
		Title:             strings.TrimSpace(p.Title),
		Description:       strings.TrimSpace(p.Description),
		Tags:              cleanUploadTags(p.Tags),
		PrivacyStatus:     p.PrivacyStatus,
		MadeForKids:       p.MadeForKids,
		IdempotencyKey:    jobID,
	}

	if target := p.PublishingTarget; target != nil {
		targetType := strings.ToLower(strings.TrimSpace(target.Type))
		if targetType != "channel" {
			return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("publishing_target.type must be channel (got %q)", target.Type)
		}
		if target.GroupID != 0 {
			return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("publishing_target.group_id is not supported by youtube.upload")
		}
		if target.WorkspaceID <= 0 {
			return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("publishing_target.workspace_id must be a positive integer")
		}
		destinationID := strings.TrimSpace(target.DestinationID)
		if destinationID == "" {
			return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("publishing_target.destination_id is required for a channel target")
		}
		req.WorkspaceID = target.WorkspaceID
		req.DestinationID = destinationID
	}
	if req.PlatformAccountID < 0 {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("platform_account_id must be a positive integer")
	}
	if req.PlatformAccountID == 0 && req.DestinationID == "" {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("publishing_target.destination_id or platform_account_id is required")
	}

	if len(p.Publications) > 1 {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("only one publication per youtube.upload job is supported")
	}
	if len(p.Publications) == 1 {
		publication := p.Publications[0]
		req.PublicationID = strings.TrimSpace(publication.PublicationID)
		meta := publication.Metadata
		if title := strings.TrimSpace(meta.Title); title != "" {
			req.Title = title
		}
		if description := strings.TrimSpace(meta.Description); description != "" {
			req.Description = description
		}
		if len(meta.Tags) > 0 {
			req.Tags = cleanUploadTags(meta.Tags)
		}
		if privacy := strings.TrimSpace(meta.Privacy); privacy != "" {
			req.PrivacyStatus = privacy
		}
		if meta.MadeForKids != nil {
			req.MadeForKids = *meta.MadeForKids
		}
	}

	if req.Title == "" {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("title is required")
	}
	if len([]rune(req.Title)) > 100 {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("title must be at most 100 characters")
	}
	if len([]rune(req.Description)) > 5000 {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("description must be at most 5000 characters")
	}
	if req.VideoPath == "" && req.VideoURL == "" {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("one of video_path or video_url is required")
	}
	req.PrivacyStatus = normalizeUploadPrivacy(req.PrivacyStatus)
	if req.PrivacyStatus == "" {
		return youtubeports.YouTubeUploadRequest{}, fmt.Errorf("privacy_status/privacy must be one of private, unlisted, public")
	}
	return req, nil
}

func normalizeUploadPrivacy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "private":
		return "private"
	case "unlisted":
		return "unlisted"
	case "public":
		return "public"
	default:
		return ""
	}
}

func cleanUploadTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	return out
}
