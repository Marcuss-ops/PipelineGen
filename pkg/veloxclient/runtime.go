// runtime.go — the Runtime SDK surface on top of the raw submit/poll pair.
//
// Why this file exists: an autonomous remote agent must discover material,
// inspect a candidate, materialize it (clips/stock), verify the artifact and
// register what it produced — all against the Master's HTTP surface. Every
// path below lives in routes.go so no caller hardcodes a URL, and every method
// funnels through the same transport (auth header, retry policy, credential
// redaction) instead of re-implementing headers by hand.
//
// The methods are deliberately thin: they marshal a request, call the shared
// transport, and decode the documented response. Endpoints whose response is a
// broad/dynamic envelope (media search, register-batch, clip metadata) return
// json.RawMessage so a server-side additive field cannot break the client; the
// typed wrappers exist only where the wire shape is a stable contract.
package veloxclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ── Discovery: GET /api/clips/search (live YouTube) ─────────────────────────

// TopicSearchQuery is the GET /api/clips/search input. Sort and PublishedAfter
// are forwarded verbatim; PublishedAfter is an RFC3339 date (e.g.
// "2025-01-01T00:00:00Z") or "" for no filter. Limit clamps server-side to
// [1,50] (default 10).
type TopicSearchQuery struct {
	Q              string
	Limit          int
	Sort           string
	PublishedAfter string
}

// TopicSearchResponse mirrors the canonical topic-search envelope.
type TopicSearchResponse struct {
	OK      bool                `json:"ok"`
	Query   string              `json:"query"`
	Limit   int                 `json:"limit"`
	Count   int                 `json:"count"`
	Source  string              `json:"source"`
	Results []TopicSearchResult `json:"results"`
}

// TopicSearchResult is one ranked YouTube candidate. DirectLink is the URL to
// feed into GET /api/clips/info and POST /api/clips/process.
type TopicSearchResult struct {
	VideoID            string `json:"video_id"`
	Title              string `json:"title"`
	ChannelName        string `json:"channel_name"`
	ThumbnailURL       string `json:"thumbnail_url"`
	ViewCount          int64  `json:"view_count"`
	UploadDate         string `json:"upload_date"`
	Duration           int    `json:"duration"`
	SimilarityScore    int    `json:"similarity_score"`
	FormatMatchPercent int    `json:"format_match_percent"`
	DirectLink         string `json:"direct_link"`
}

// SearchClipsByTopic performs LIVE YouTube discovery. Distinct from
// MediaSearch, which reads the Master's already-registered catalog.
func (c *Client) SearchClipsByTopic(ctx context.Context, q TopicSearchQuery) (*TopicSearchResponse, error) {
	if strings.TrimSpace(q.Q) == "" {
		return nil, fmt.Errorf("veloxclient: SearchClipsByTopic requires a non-empty Q")
	}
	params := url.Values{}
	params.Set("q", q.Q)
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Sort != "" {
		params.Set("sort", q.Sort)
	}
	if q.PublishedAfter != "" {
		params.Set("published_after", q.PublishedAfter)
	}
	var out TopicSearchResponse
	if err := c.getJSON(ctx, RouteClipsTopicSearch+"?"+params.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Metadata: GET /api/clips/info ───────────────────────────────────────────

// ClipMetadata is a lenient projection of the YouTube metadata blob returned
// by GET /api/clips/info. Raw always carries the full body so a caller that
// needs a field this struct does not model (chapters, thumbnails, ...) can
// decode it without a client release.
type ClipMetadata struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Duration  float64         `json:"duration"`
	Uploader  string          `json:"uploader"`
	ViewCount int64           `json:"view_count"`
	Thumbnail string          `json:"thumbnail"`
	Tags      []string        `json:"tags"`
	Raw       json.RawMessage `json:"-"`
}

// ClipInfo fetches full metadata for a single YouTube URL without downloading
// it. Accepts both watch?v= and youtu.be URLs.
func (c *Client) ClipInfo(ctx context.Context, videoURL string) (*ClipMetadata, error) {
	if strings.TrimSpace(videoURL) == "" {
		return nil, fmt.Errorf("veloxclient: ClipInfo requires a non-empty url")
	}
	path := RouteClipsInfo + "?url=" + url.QueryEscape(videoURL)
	raw, err := c.getRaw(ctx, path)
	if err != nil {
		return nil, err
	}
	var meta ClipMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("veloxclient: decode clip info: %w (body=%s)", err, truncate(raw, 256))
	}
	meta.Raw = append(json.RawMessage(nil), raw...)
	return &meta, nil
}

// ── Catalog: POST /api/media/search ─────────────────────────────────────────

// MediaSearch posts a unified media-search request and returns the raw
// envelope. The request shape (query / mode / universe / filters) is owned by
// the server; callers pass the canonical map so the client does not freeze a
// server-side schema it would then have to track.
func (c *Client) MediaSearch(ctx context.Context, payload any) (json.RawMessage, error) {
	return c.PostJSONRaw(ctx, RouteMediaSearch, payload)
}

// ── Stock: POST /api/stock-pipeline/{run,search-and-run} ────────────────────

// StockRunRequest is the documented POST /api/stock-pipeline/run body. Only the
// fields an autonomous agent sets are typed; the server rejects unknown fields
// (UNKNOWN_FIELD), so this mirrors the wire contract rather than widening it.
type StockRunRequest struct {
	SearchQueries []string `json:"search_queries,omitempty"`
	DirectURLs    []string `json:"direct_urls,omitempty"`
	DriveURLs     []string `json:"drive_urls,omitempty"`
	// Legacy per-clip specs are intentionally not modelled: the agent drives
	// the search path, and the four-part duration contract below is the
	// supported way to bound a run.
	TotalMinutes                   int    `json:"total_minutes,omitempty"`
	TargetTotalDurationSeconds     int    `json:"target_total_duration_seconds,omitempty"`
	TargetDurationPerSourceSeconds int    `json:"target_duration_per_source_seconds,omitempty"`
	ClipsPerSource                 int    `json:"clips_per_source,omitempty"`
	ClipDurationSeconds            int    `json:"clip_duration_seconds,omitempty"`
	MaxVideos                      int    `json:"max_videos,omitempty"`
	Subfolder                      string `json:"subfolder,omitempty"`
	FolderName                     string `json:"folder_name,omitempty"`
	DriveFolderID                  string `json:"drive_folder_id,omitempty"`
	Async                          bool   `json:"async,omitempty"`
	Persist                        bool   `json:"persist,omitempty"`
}

// StockQuery is one entry of the search-and-run `queries` array.
type StockQuery struct {
	Q     string `json:"q"`
	Limit int    `json:"limit,omitempty"`
}

// StockSearchAndRunRequest is the POST /api/stock-pipeline/search-and-run body.
// NOTE the `queries` field name: sending `search_queries` here is the legacy
// /run shape and the server fails closed with 400.
type StockSearchAndRunRequest struct {
	Queries                        []StockQuery `json:"queries"`
	TotalMinutes                   int          `json:"total_minutes,omitempty"`
	TargetTotalDurationSeconds     int          `json:"target_total_duration_seconds,omitempty"`
	TargetDurationPerSourceSeconds int          `json:"target_duration_per_source_seconds,omitempty"`
	ClipsPerSource                 int          `json:"clips_per_source,omitempty"`
	ClipDurationSeconds            int          `json:"clip_duration_seconds,omitempty"`
	MaxVideos                      int          `json:"max_videos,omitempty"`
	Subfolder                      string       `json:"subfolder,omitempty"`
	FolderName                     string       `json:"folder_name,omitempty"`
	DriveFolderID                  string       `json:"drive_folder_id,omitempty"`
	Async                          bool         `json:"async,omitempty"`
	Persist                        bool         `json:"persist,omitempty"`
}

// StockRunResponse is the endpoint ACKNOWLEDGEMENT (not the job state): Status
// is QUEUED (async), completed (inline) or error. Poll JobID via WaitJob for
// the real outcome.
type StockRunResponse struct {
	JobID        string `json:"job_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	Status       string `json:"status"`
	Deduplicated bool   `json:"deduplicated"`
	Error        string `json:"error,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

// StockPipelineRun posts a /run request (search_queries / direct_urls /
// drive_urls).
func (c *Client) StockPipelineRun(ctx context.Context, req StockRunRequest) (*StockRunResponse, error) {
	var out StockRunResponse
	if err := c.postJSONInto(ctx, RouteStockPipelineRun, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StockPipelineSearchAndRun posts a /search-and-run request (queries).
func (c *Client) StockPipelineSearchAndRun(ctx context.Context, req StockSearchAndRunRequest) (*StockRunResponse, error) {
	if len(req.Queries) == 0 {
		return nil, fmt.Errorf("veloxclient: StockPipelineSearchAndRun requires at least one query")
	}
	var out StockRunResponse
	if err := c.postJSONInto(ctx, RouteStockPipelineSearchAndRun, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Self-import: register / upload material the remote produced ─────────────

// RegisterBatch registers assets whose bytes are already durable (on Drive) by
// wire shape only. This is the canonical way a remote records material it
// produced itself; it returns the raw envelope so the caller can read the
// per-item results the server reports.
func (c *Client) RegisterBatch(ctx context.Context, payload any) (json.RawMessage, error) {
	return c.PostJSONRaw(ctx, RouteMediaRegisterBatch, payload)
}

// RegisterFromYouTube registers catalog rows for YouTube videos the remote
// already uploaded.
func (c *Client) RegisterFromYouTube(ctx context.Context, payload any) (json.RawMessage, error) {
	return c.PostJSONRaw(ctx, RouteMediaRegisterFromYouTube, payload)
}

// VideoUploadMeta is the multipart form metadata for
// POST /api/media/clips/upload-video. Tags are sent as a JSON array (the
// server also accepts a comma-separated fallback).
type VideoUploadMeta struct {
	Filename    string
	Name        string
	Description string
	Tags        []string
	Source      string
	Category    string
	Group       string
	FolderID    string
}

// UploadVideoClip uploads a video file plus metadata as multipart/form-data and
// returns the raw JSON response (clip_id, drive_link, local_path, ...). This is
// how a remote adds a file it acquired on its own disk to the catalog without
// ever touching the Master's database.
func (c *Client) UploadVideoClip(ctx context.Context, meta VideoUploadMeta, file io.Reader) (json.RawMessage, error) {
	if file == nil {
		return nil, fmt.Errorf("veloxclient: UploadVideoClip requires a file reader")
	}
	if strings.TrimSpace(meta.Filename) == "" {
		return nil, fmt.Errorf("veloxclient: UploadVideoClip requires meta.Filename")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", meta.Filename)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: build multipart file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("veloxclient: stream file into multipart: %w", err)
	}

	fields := map[string]string{
		"name":        meta.Name,
		"description": meta.Description,
		"source":      meta.Source,
		"category":    meta.Category,
		"group":       meta.Group,
		"folder_id":   meta.FolderID,
	}
	if len(meta.Tags) > 0 {
		tagsJSON, err := json.Marshal(meta.Tags)
		if err != nil {
			return nil, fmt.Errorf("veloxclient: marshal tags: %w", err)
		}
		fields["tags"] = string(tagsJSON)
	}
	for k, v := range fields {
		if v == "" {
			continue
		}
		if err := writer.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("veloxclient: write multipart field %s: %w", k, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("veloxclient: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+RouteMediaUploadVideo, &body)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: build upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServer, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read upload body: %v", ErrServer, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return raw, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: status=%d", ErrUnauthorized, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: status=%d", ErrNotFound, resp.StatusCode)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return nil, fmt.Errorf("%w: status=%d body=%s", ErrBadRequest, resp.StatusCode, truncate(raw, 256))
	default:
		return nil, fmt.Errorf("%w: status=%d body=%s", ErrServer, resp.StatusCode, truncate(raw, 256))
	}
}

// ── Download: POST /api/media/clips/{source}/clips/{id}/download ────────────

// DownloadClip streams a clip's artifact into w and returns the response
// Content-Type. The endpoint is POST-only; PostToWriter is the canonical path.
func (c *Client) DownloadClip(ctx context.Context, source, clipID string, w io.Writer) (string, error) {
	if source == "" || clipID == "" {
		return "", fmt.Errorf("veloxclient: DownloadClip requires source and clipID")
	}
	return c.PostToWriter(ctx, RouteClipsDownload(source, clipID), nil, w)
}

// ── transport helpers ───────────────────────────────────────────────────────

// getRaw performs an authenticated GET and returns the raw body.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("veloxclient: empty path")
	}
	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	resp, retryable, err := c.doRequest(ctx, http.MethodGet, url, nil, "")
	if err != nil {
		if retryable {
			return nil, ErrServer
		}
		return nil, err
	}
	return resp.body, nil
}

// getJSON performs an authenticated GET and decodes the body into out.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	raw, err := c.getRaw(ctx, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("veloxclient: decode response: %w (body=%s)", err, truncate(raw, 256))
	}
	return nil
}

// PostJSONRaw is the exported form of PostJSON: POST a JSON payload and return
// the raw response body (for envelopes whose shape the server owns).
func (c *Client) PostJSONRaw(ctx context.Context, path string, payload any) (json.RawMessage, error) {
	raw, err := c.PostJSON(ctx, path, payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// postJSONInto POSTs payload and decodes the response into out.
func (c *Client) postJSONInto(ctx context.Context, path string, payload any, out any) error {
	raw, err := c.PostJSON(ctx, path, payload)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("veloxclient: decode response: %w (body=%s)", err, truncate(raw, 256))
	}
	return nil
}
