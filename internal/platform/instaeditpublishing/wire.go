package instaeditpublishing

import (
	"fmt"
	"strings"
)

type targetsResponse struct {
	WorkspaceID int64           `json:"workspace_id"`
	Channels    []channelTarget `json:"channels"`
}

type channelTarget struct {
	Type                  string `json:"type"`
	PlatformAccountID     int64  `json:"platform_account_id"`
	ChannelID             string `json:"channel_id"`
	ChannelName           string `json:"channel_name"`
	Status                string `json:"status"`
	AccountState          string `json:"account_state"`
	IsPublishable         *bool  `json:"is_publishable"`
	Enabled               *bool  `json:"enabled"`
	DestinationID         string `json:"destination_id,omitempty"`
	VeloxDestinationID    string `json:"velox_destination_id,omitempty"`
	ExternalDestinationID string `json:"external_destination_id,omitempty"`
	LastPublishedAt       string `json:"last_published_at,omitempty"`
	LastPublishedTitle    string `json:"last_published_title,omitempty"`
	LastPublishedVideoID  string `json:"last_published_video_id,omitempty"`
	LastPublishedURL      string `json:"last_published_url,omitempty"`
}

func (t *channelTarget) matchesDestination(destinationID string) bool {
	for _, candidate := range []string{t.VeloxDestinationID, t.ExternalDestinationID, t.DestinationID} {
		if candidate != "" && strings.EqualFold(strings.TrimSpace(candidate), destinationID) {
			return true
		}
	}
	return false
}

func (t *channelTarget) veloxDestinationID() string {
	for _, candidate := range []string{t.VeloxDestinationID, t.ExternalDestinationID, t.DestinationID} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func (t *channelTarget) label(destinationID string, platformAccountID int64) string {
	name := strings.TrimSpace(t.ChannelName)
	if name == "" {
		name = fmt.Sprintf("platform_account_id %d", t.PlatformAccountID)
	}
	if destinationID = strings.TrimSpace(destinationID); destinationID != "" {
		return fmt.Sprintf("%q (destination_id=%s)", name, destinationID)
	}
	if platformAccountID <= 0 {
		platformAccountID = t.PlatformAccountID
	}
	return fmt.Sprintf("%q (platform_account_id=%d)", name, platformAccountID)
}

type presignRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256,omitempty"`
}

type presignResponse struct {
	AssetID       string            `json:"asset_id"`
	UploadURL     string            `json:"upload_url"`
	UploadHeaders map[string]string `json:"upload_headers"`
	Asset         *struct {
		ID string `json:"id"`
	} `json:"asset,omitempty"`
}

type createPostRequest struct {
	WorkspaceID int64              `json:"workspace_id"`
	Status      string             `json:"status"`
	Content     createPostContent  `json:"content"`
	Targets     []createPostTarget `json:"targets"`
}

type createPostContent struct {
	Title   string            `json:"title"`
	Caption string            `json:"caption,omitempty"`
	Media   []createPostMedia `json:"media"`
}

type createPostMedia struct {
	AssetID string `json:"asset_id"`
}

type createPostTarget struct {
	PlatformAccountID int64              `json:"platform_account_id"`
	Settings          createPostSettings `json:"settings"`
}

type createPostSettings struct {
	YouTube youtubeSettings `json:"youtube"`
}

type youtubeSettings struct {
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	PrivacyStatus string   `json:"privacy_status"`
	MadeForKids   bool     `json:"made_for_kids"`
	Tags          []string `json:"tags,omitempty"`
}

type createPostResponse struct {
	ID      int64        `json:"id"`
	Status  string       `json:"status"`
	Targets []postTarget `json:"targets"`
	Post    *struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"post,omitempty"`
}

func (r *createPostResponse) postID() int64 {
	if r == nil {
		return 0
	}
	if r.ID != 0 {
		return r.ID
	}
	if r.Post != nil {
		return r.Post.ID
	}
	return 0
}

func (r *createPostResponse) postStatus() string {
	if r == nil {
		return ""
	}
	if r.Status != "" {
		return r.Status
	}
	if r.Post != nil {
		return r.Post.Status
	}
	return ""
}

type postTarget struct {
	ID                int64  `json:"id"`
	PostID            int64  `json:"post_id"`
	PlatformAccountID int64  `json:"platform_account_id"`
	Status            string `json:"status"`
	PlatformPostID    string `json:"platform_post_id"`
	RemotePostID      string `json:"remote_post_id"`
	RemotePostURL     string `json:"remote_post_url"`
	ExternalID        string `json:"external_id"`
	ExternalPostID    string `json:"external_post_id"`
	YouTubeVideoID    string `json:"youtube_video_id"`
	PrivacyStatus     string `json:"privacy_status"`
	ErrorMessage      string `json:"error_message"`
	LastError         string `json:"last_error"`
	LastErrorCode     string `json:"last_error_code"`
}

func (t *postTarget) videoID() string {
	for _, value := range []string{t.PlatformPostID, t.YouTubeVideoID, t.RemotePostID, t.ExternalID, t.ExternalPostID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (t *postTarget) videoURL() string {
	if value := strings.TrimSpace(t.RemotePostURL); value != "" {
		return value
	}
	if videoID := t.videoID(); videoID != "" {
		return "https://youtu.be/" + videoID
	}
	return ""
}

func (t *postTarget) errorText() string {
	for _, value := range []string{t.ErrorMessage, t.LastError, t.LastErrorCode} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "no error detail returned"
}
