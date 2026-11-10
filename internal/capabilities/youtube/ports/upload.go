package ports

import "context"

// YouTubeUploadRequest is the application-layer request for publishing one
// video to a connected YouTube channel through InstaEdit.
//
// The destination can be addressed either by the InstaEdit
// platform_account_id or by the opaque channel destination id returned by the
// InstaEdit catalog (velox_destination_id / external_destination_id). When both
// are supplied they must identify the same channel.
//
// Exactly one of VideoPath or VideoURL must be provided. VideoPath is resolved
// on the worker host; VideoURL is downloaded by the publishing adapter before
// the presigned upload.
type YouTubeUploadRequest struct {
	PlatformAccountID int64
	DestinationID     string
	WorkspaceID       int64
	PublicationID     string
	VideoPath         string
	VideoURL          string
	Title             string
	Description       string
	Tags              []string
	PrivacyStatus     string
	MadeForKids       bool
	IdempotencyKey    string
}

// YouTubeUploadResult is the terminal projection returned to the job broker.
type YouTubeUploadResult struct {
	PostID            int64  `json:"post_id"`
	TargetID          int64  `json:"target_id"`
	VideoID           string `json:"video_id,omitempty"`
	VideoURL          string `json:"video_url,omitempty"`
	Status            string `json:"status"`
	PrivacyStatus     string `json:"privacy_status,omitempty"`
	WorkspaceID       int64  `json:"workspace_id,omitempty"`
	DestinationID     string `json:"destination_id,omitempty"`
	PublicationID     string `json:"publication_id,omitempty"`
	PlatformAccountID int64  `json:"platform_account_id,omitempty"`
	ChannelID         string `json:"channel_id,omitempty"`
	ChannelName       string `json:"channel_name,omitempty"`
}

// YouTubeUploadPublisher is the narrow port consumed by the youtube.upload job
// handler. The InstaEdit adapter implements it.
type YouTubeUploadPublisher interface {
	PublishYouTube(ctx context.Context, req YouTubeUploadRequest) (*YouTubeUploadResult, error)
}
