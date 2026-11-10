// Package instaeditpublishing is the InstaEdit adapter for direct YouTube
// publishing. It implements the youtube.upload port with the same sequence
// used by the InstaEdit web client:
//
//	GET  /api/v1/publishing/targets
//	POST /api/v1/media/presign
//	PUT  {presigned upload_url}
//	POST /api/v1/media/{asset_id}/complete
//	POST /api/v1/posts
//	GET  /api/v1/post-targets/{target_id}
//
// The API key is workspace-scoped and must carry read, accounts, media, write
// and publish permissions. The adapter never logs the key.
package instaeditpublishing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

const (
	defaultPollInterval   = 3 * time.Second
	defaultPublishTimeout = 20 * time.Minute
	apiCallTimeout        = 30 * time.Second
	maxResponseBody       = 8 << 20
)

// HTTPError is a non-2xx response from InstaEdit.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("InstaEdit returned HTTP %d: %s", e.StatusCode, message)
}

// Client publishes to InstaEdit. PollInterval and PublishTimeout are exported
// so tests and operators can tune the wait without changing the wire contract.
type Client struct {
	cfg            Config
	http           *http.Client
	PollInterval   time.Duration
	PublishTimeout time.Duration
}

// NewClient validates the configuration and constructs an HTTP client without
// a global timeout: video uploads can legitimately take longer than a normal
// JSON call, and every operation is bounded by its request context.
func NewClient(cfg Config) (*Client, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return nil, fmt.Errorf("InstaEdit publishing base URL and API key are required")
	}
	if cfg.WorkspaceID <= 0 {
		return nil, fmt.Errorf("InstaEdit publishing workspace id is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("invalid InstaEdit publishing base URL")
	}
	return &Client{
		cfg:            cfg,
		http:           &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 10, IdleConnTimeout: 90 * time.Second}},
		PollInterval:   defaultPollInterval,
		PublishTimeout: defaultPublishTimeout,
	}, nil
}

// PublishYouTube uploads the video and waits for the InstaEdit post target to
// reach a terminal state. It returns the YouTube video id when InstaEdit
// publishes the target.
func (c *Client) PublishYouTube(ctx context.Context, req ports.YouTubeUploadRequest) (*ports.YouTubeUploadResult, error) {
	if c == nil {
		return nil, fmt.Errorf("InstaEdit publishing client is nil")
	}
	if err := validatePublishRequest(req); err != nil {
		return nil, err
	}
	channel, err := c.resolveTarget(ctx, req.PlatformAccountID, req.DestinationID, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	videoPath, cleanup, err := c.materializeVideo(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	video, err := inspectVideo(videoPath)
	if err != nil {
		return nil, err
	}
	asset, err := c.presign(ctx, video)
	if err != nil {
		return nil, err
	}
	if err := c.upload(ctx, asset, video); err != nil {
		return nil, err
	}
	if err := c.complete(ctx, asset.AssetID); err != nil {
		return nil, err
	}
	post, err := c.createPost(ctx, req, asset.AssetID, channel.PlatformAccountID)
	if err != nil {
		return nil, err
	}
	postID := post.postID()
	if postID == 0 {
		return nil, fmt.Errorf("InstaEdit created a post but returned no post id")
	}
	// The web client creates immediate posts with status=queued and never
	// calls /publish. Keep /publish as a defensive fallback only for an
	// unexpected draft response.
	if strings.EqualFold(post.postStatus(), "draft") {
		if err := c.publishPost(ctx, postID); err != nil {
			return nil, err
		}
	}
	targetID := targetIDFor(post.Targets, channel.PlatformAccountID)
	if targetID == 0 {
		return nil, fmt.Errorf("InstaEdit created post %d but returned no target for platform_account_id %d", postID, channel.PlatformAccountID)
	}
	target, err := c.waitForTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	privacy := target.PrivacyStatus
	if privacy == "" {
		privacy = normalizePrivacy(req.PrivacyStatus)
	}
	videoID := target.videoID()
	result := &ports.YouTubeUploadResult{
		PostID:            postID,
		TargetID:          targetID,
		VideoID:           videoID,
		VideoURL:          target.videoURL(),
		Status:            target.Status,
		PrivacyStatus:     privacy,
		WorkspaceID:       c.cfg.WorkspaceID,
		DestinationID:     channel.veloxDestinationID(),
		PublicationID:     strings.TrimSpace(req.PublicationID),
		PlatformAccountID: channel.PlatformAccountID,
		ChannelID:         channel.ChannelID,
		ChannelName:       channel.ChannelName,
	}
	if result.VideoURL == "" && videoID != "" {
		result.VideoURL = "https://youtu.be/" + videoID
	}
	return result, nil
}

func validatePublishRequest(req ports.YouTubeUploadRequest) error {
	if req.PlatformAccountID < 0 {
		return fmt.Errorf("platform_account_id must be a positive integer")
	}
	if req.PlatformAccountID == 0 && strings.TrimSpace(req.DestinationID) == "" {
		return fmt.Errorf("platform_account_id or destination_id is required")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return fmt.Errorf("title is required")
	}
	if len([]rune(title)) > 100 {
		return fmt.Errorf("title must be at most 100 characters")
	}
	if len([]rune(req.Description)) > 5000 {
		return fmt.Errorf("description must be at most 5000 characters")
	}
	if normalizePrivacy(req.PrivacyStatus) == "" {
		return fmt.Errorf("privacy_status must be one of private, unlisted, public")
	}
	if strings.TrimSpace(req.VideoPath) == "" && strings.TrimSpace(req.VideoURL) == "" {
		return fmt.Errorf("one of video_path or video_url is required")
	}
	if strings.TrimSpace(req.VideoPath) == "" {
		parsed, err := url.Parse(strings.TrimSpace(req.VideoURL))
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return fmt.Errorf("video_url must be an absolute http(s) URL")
		}
	}
	tagChars := 0
	for _, tag := range req.Tags {
		tagChars += len([]rune(strings.TrimSpace(tag)))
	}
	if tagChars > 500 {
		return fmt.Errorf("tags must contain at most 500 characters in total")
	}
	return nil
}

func normalizePrivacy(value string) string {
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

// resolveTarget reads the workspace catalog and selects exactly one channel.
// platformAccountID and destinationID are alternative selectors: when both are
// supplied the catalog row must match both. destinationID is matched against
// velox_destination_id, external_destination_id and destination_id because the
// catalog exposes the opaque id under different names over time.
func (c *Client) resolveTarget(ctx context.Context, platformAccountID int64, destinationID string, workspaceID int64) (*channelTarget, error) {
	destinationID = strings.TrimSpace(destinationID)
	if workspaceID <= 0 {
		workspaceID = c.cfg.WorkspaceID
	}
	if workspaceID != c.cfg.WorkspaceID {
		return nil, fmt.Errorf("publishing workspace_id %d does not match the configured workspace %d", workspaceID, c.cfg.WorkspaceID)
	}
	path := fmt.Sprintf("/api/v1/publishing/targets?workspace_id=%d", workspaceID)
	data, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("read InstaEdit publishing targets: %w", err)
	}
	var response targetsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode InstaEdit publishing targets: %w", err)
	}
	var match *channelTarget
	for i := range response.Channels {
		channel := &response.Channels[i]
		if platformAccountID > 0 && channel.PlatformAccountID != platformAccountID {
			continue
		}
		if destinationID != "" && !channel.matchesDestination(destinationID) {
			continue
		}
		match = channel
		break
	}
	if match == nil {
		if destinationID != "" {
			return nil, fmt.Errorf("destination_id %q is not a channel in InstaEdit workspace %d", destinationID, workspaceID)
		}
		return nil, fmt.Errorf("platform_account_id %d is not a channel in InstaEdit workspace %d", platformAccountID, workspaceID)
	}
	label := match.label(destinationID, platformAccountID)
	if !strings.EqualFold(match.Status, "active") {
		return nil, fmt.Errorf("channel %s is not active (status=%q)", label, match.Status)
	}
	if match.IsPublishable != nil && !*match.IsPublishable {
		return nil, fmt.Errorf("channel %s is not publishable", label)
	}
	if match.Enabled != nil && !*match.Enabled {
		return nil, fmt.Errorf("channel %s is disabled", label)
	}
	return match, nil
}

func (c *Client) presign(ctx context.Context, video videoFile) (*presignResponse, error) {
	request := presignRequest{
		Filename:    video.Filename,
		ContentType: video.ContentType,
		SizeBytes:   video.Size,
		SHA256:      video.SHA256,
	}
	var response presignResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/media/presign", request, &response, ""); err != nil {
		return nil, fmt.Errorf("presign InstaEdit media: %w", err)
	}
	if response.AssetID == "" && response.Asset != nil {
		response.AssetID = response.Asset.ID
	}
	if response.AssetID == "" {
		return nil, fmt.Errorf("InstaEdit presign returned no asset_id")
	}
	if strings.TrimSpace(response.UploadURL) == "" {
		return nil, fmt.Errorf("InstaEdit presign returned no upload_url")
	}
	return &response, nil
}

func (c *Client) upload(ctx context.Context, asset *presignResponse, video videoFile) error {
	uploadURL, err := c.absoluteURL(asset.UploadURL)
	if err != nil {
		return fmt.Errorf("resolve InstaEdit upload URL: %w", err)
	}
	file, err := os.Open(video.Path)
	if err != nil {
		return fmt.Errorf("open video for InstaEdit upload: %w", err)
	}
	defer file.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, file)
	if err != nil {
		return fmt.Errorf("build InstaEdit upload request: %w", err)
	}
	for key, value := range asset.UploadHeaders {
		req.Header.Set(key, value)
	}
	if !hasHeader(asset.UploadHeaders, "Content-Type") {
		req.Header.Set("Content-Type", video.ContentType)
	}
	req.ContentLength = video.Size

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("upload video to InstaEdit storage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		return &HTTPError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	return nil
}

func (c *Client) complete(ctx context.Context, assetID string) error {
	path := "/api/v1/media/" + url.PathEscape(assetID) + "/complete"
	if _, err := c.do(ctx, http.MethodPost, path, nil, ""); err != nil {
		return fmt.Errorf("complete InstaEdit media asset %s: %w", assetID, err)
	}
	return nil
}

func (c *Client) createPost(ctx context.Context, req ports.YouTubeUploadRequest, assetID string, platformAccountID int64) (*createPostResponse, error) {
	description := strings.TrimSpace(req.Description)
	settings := youtubeSettings{
		Title:         strings.TrimSpace(req.Title),
		Description:   description,
		PrivacyStatus: normalizePrivacy(req.PrivacyStatus),
		MadeForKids:   req.MadeForKids,
		Tags:          cleanTags(req.Tags),
	}
	payload := createPostRequest{
		WorkspaceID: c.cfg.WorkspaceID,
		Status:      "queued",
		Content: createPostContent{
			Title:   strings.TrimSpace(req.Title),
			Caption: description,
			Media:   []createPostMedia{{AssetID: assetID}},
		},
		Targets: []createPostTarget{{
			PlatformAccountID: platformAccountID,
			Settings:          createPostSettings{YouTube: settings},
		}},
	}
	var response createPostResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/posts", payload, &response, req.IdempotencyKey); err != nil {
		return nil, fmt.Errorf("create InstaEdit post: %w", err)
	}
	return &response, nil
}

func (c *Client) publishPost(ctx context.Context, postID int64) error {
	path := fmt.Sprintf("/api/v1/posts/%d/publish", postID)
	if _, err := c.do(ctx, http.MethodPost, path, nil, ""); err != nil {
		return fmt.Errorf("publish InstaEdit post %d: %w", postID, err)
	}
	return nil
}

func (c *Client) waitForTarget(ctx context.Context, targetID int64) (*postTarget, error) {
	interval := c.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timeout := c.PublishTimeout
	if timeout <= 0 {
		timeout = defaultPublishTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last *postTarget
	var lastErr error
	for {
		target, err := c.getPostTarget(waitCtx, targetID)
		if err == nil && target != nil {
			last = target
			switch strings.ToLower(target.Status) {
			case "published", "completed", "success", "succeeded":
				return target, nil
			case "failed", "dlq", "dead_letter", "blocked_auth", "cancelled", "canceled", "partially_published":
				return nil, fmt.Errorf("InstaEdit post target %d finished as %s: %s", targetID, target.Status, target.errorText())
			}
		} else if err != nil {
			lastErr = err
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden) {
				return nil, err
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if last != nil {
				return nil, fmt.Errorf("timed out waiting for InstaEdit post target %d (last status=%q, last error=%v): %w", targetID, last.Status, lastErr, waitCtx.Err())
			}
			if lastErr != nil {
				return nil, fmt.Errorf("timed out waiting for InstaEdit post target %d: %v: %w", targetID, lastErr, waitCtx.Err())
			}
			return nil, fmt.Errorf("timed out waiting for InstaEdit post target %d: %w", targetID, waitCtx.Err())
		case <-timer.C:
		}
	}
}

func (c *Client) getPostTarget(ctx context.Context, targetID int64) (*postTarget, error) {
	path := fmt.Sprintf("/api/v1/post-targets/%d", targetID)
	data, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("read InstaEdit post target %d: %w", targetID, err)
	}
	var wrapper struct {
		Target     *postTarget `json:"target"`
		PostTarget *postTarget `json:"post_target"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil {
		if wrapper.Target != nil {
			return wrapper.Target, nil
		}
		if wrapper.PostTarget != nil {
			return wrapper.PostTarget, nil
		}
	}
	var target postTarget
	if err := json.Unmarshal(data, &target); err != nil {
		return nil, fmt.Errorf("decode InstaEdit post target %d: %w", targetID, err)
	}
	return &target, nil
}

func (c *Client) materializeVideo(ctx context.Context, req ports.YouTubeUploadRequest) (string, func(), error) {
	if local := strings.TrimSpace(req.VideoPath); local != "" {
		info, err := os.Stat(local)
		if err != nil {
			return "", func() {}, fmt.Errorf("video_path is not readable: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", func() {}, fmt.Errorf("video_path is not a regular file")
		}
		return local, func() {}, nil
	}

	videoURL := strings.TrimSpace(req.VideoURL)
	parsed, err := url.Parse(videoURL)
	if err != nil {
		return "", func() {}, fmt.Errorf("parse video_url: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return "", func() {}, fmt.Errorf("build video_url request: %w", err)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", func() {}, fmt.Errorf("download video_url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		return "", func() {}, fmt.Errorf("download video_url: InstaEdit source returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	extension := strings.ToLower(filepath.Ext(parsed.Path))
	if extension != ".mp4" && extension != ".mov" && extension != ".m4v" {
		extension = ".mp4"
	}
	tmp, err := os.CreateTemp("", "pipelinegen-youtube-upload-*"+extension)
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary video: %w", err)
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("download video_url body: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temporary video: %w", err)
	}
	return tmp.Name(), cleanup, nil
}

func (c *Client) absoluteURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		return parsed.String(), nil
	}
	base, err := url.Parse(c.cfg.BaseURL + "/")
	if err != nil {
		return "", err
	}
	return base.ResolveReference(parsed).String(), nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any, idempotencyKey string) error {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode InstaEdit request: %w", err)
		}
	}
	data, err := c.do(ctx, method, path, body, idempotencyKey)
	if err != nil {
		return err
	}
	if output != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode InstaEdit response: %w", err)
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, idempotencyKey string) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(callCtx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call InstaEdit: %w", err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if readErr != nil {
		return nil, fmt.Errorf("read InstaEdit response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(data))}
	}
	return data, nil
}

type videoFile struct {
	Path        string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
}

func inspectVideo(filePath string) (videoFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return videoFile{}, fmt.Errorf("open video: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return videoFile{}, fmt.Errorf("stat video: %w", err)
	}
	if !info.Mode().IsRegular() {
		return videoFile{}, fmt.Errorf("video path is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return videoFile{}, fmt.Errorf("hash video: %w", err)
	}
	filename := filepath.Base(filePath)
	contentType, err := videoContentType(filename)
	if err != nil {
		return videoFile{}, err
	}
	return videoFile{
		Path:        filePath,
		Filename:    filename,
		ContentType: contentType,
		Size:        info.Size(),
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func videoContentType(filename string) (string, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4":
		return "video/mp4", nil
	case ".mov":
		return "video/quicktime", nil
	case ".m4v":
		return "video/x-m4v", nil
	default:
		return "", fmt.Errorf("unsupported video extension %q (expected .mp4, .mov or .m4v)", filepath.Ext(filename))
	}
}

func hasHeader(headers map[string]string, wanted string) bool {
	for key := range headers {
		if strings.EqualFold(key, wanted) {
			return true
		}
	}
	return false
}

func targetIDFor(targets []postTarget, platformAccountID int64) int64 {
	for _, target := range targets {
		if target.PlatformAccountID == platformAccountID && target.ID != 0 {
			return target.ID
		}
	}
	if len(targets) == 1 {
		return targets[0].ID
	}
	return 0
}

func cleanTags(tags []string) []string {
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
