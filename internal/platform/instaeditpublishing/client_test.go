package instaeditpublishing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

type fakeInstaEdit struct {
	mu             sync.Mutex
	publishable    bool
	finalStatus    string
	presignCalls   int
	uploadCalls    int
	pollCalls      int
	createBody     map[string]any
	idempotencyKey string
	uploadBody     string
}

func newFakeInstaEdit(t *testing.T, publishable bool, finalStatus string) (*fakeInstaEdit, *httptest.Server) {
	t.Helper()
	fake := &fakeInstaEdit{publishable: publishable, finalStatus: finalStatus}
	var server *httptest.Server

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/publishing/targets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("targets method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.URL.Query().Get("workspace_id"); got != "5" {
			t.Errorf("workspace_id = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"workspace_id": 5,
			"channels": []any{map[string]any{
				"type":                    "channel",
				"platform_account_id":     9,
				"channel_id":              "UC-test",
				"channel_name":            "Test Channel",
				"status":                  "active",
				"account_state":           "valid",
				"is_publishable":          fake.publishable,
				"enabled":                 true,
				"velox_destination_id":    "instaedit_extdst_test",
				"external_destination_id": "extdst_test",
				"last_published_at":       "2026-11-09T12:00:00Z",
				"last_published_title":    "Previous video",
				"last_published_video_id": "vid-previous",
				"last_published_url":      "https://youtu.be/vid-previous",
			}},
		})
	})
	mux.HandleFunc("/api/v1/media/presign", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("presign method = %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode presign: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body["filename"] != "clip.mp4" {
			t.Errorf("filename = %v", body["filename"])
		}
		if body["content_type"] != "video/mp4" {
			t.Errorf("content_type = %v", body["content_type"])
		}
		sum := sha256.Sum256([]byte("video-bytes"))
		if body["sha256"] != hex.EncodeToString(sum[:]) {
			t.Errorf("sha256 = %v", body["sha256"])
		}
		if body["size_bytes"] != float64(len("video-bytes")) {
			t.Errorf("size_bytes = %v", body["size_bytes"])
		}
		fake.mu.Lock()
		fake.presignCalls++
		fake.mu.Unlock()
		writeJSON(t, w, http.StatusOK, map[string]any{
			"asset_id":       "asset-1",
			"upload_url":     server.URL + "/upload",
			"upload_headers": map[string]string{"x-test": "1"},
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("upload method = %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "video/mp4" {
			t.Errorf("upload content type = %q", got)
		}
		if got := r.Header.Get("x-test"); got != "1" {
			t.Errorf("upload x-test = %q", got)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upload: %v", err)
		}
		fake.mu.Lock()
		fake.uploadCalls++
		fake.uploadBody = string(data)
		fake.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/media/asset-1/complete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("complete method = %s", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"id": "asset-1"})
	})
	mux.HandleFunc("/api/v1/posts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("posts method = %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode post: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		fake.mu.Lock()
		fake.createBody = body
		fake.idempotencyKey = r.Header.Get("Idempotency-Key")
		fake.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"id":     77,
			"status": "queued",
			"targets": []any{map[string]any{
				"id":                  88,
				"post_id":             77,
				"platform_account_id": 9,
				"status":              "queued",
			}},
		})
	})
	mux.HandleFunc("/api/v1/post-targets/88", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("post target method = %s", r.Method)
		}
		fake.mu.Lock()
		fake.pollCalls++
		calls := fake.pollCalls
		fake.mu.Unlock()
		status := "queued"
		if calls >= 2 {
			status = fake.finalStatus
		}
		response := map[string]any{
			"id":                  88,
			"post_id":             77,
			"platform_account_id": 9,
			"status":              status,
		}
		if status == "published" {
			response["platform_post_id"] = "vid-123"
			response["remote_post_url"] = "https://www.youtube.com/watch?v=vid-123"
			response["privacy_status"] = "private"
		}
		if status == "failed" {
			response["error_message"] = "boom"
		}
		writeJSON(t, w, http.StatusOK, response)
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return fake, server
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(Config{BaseURL: server.URL, APIKey: "test-key", WorkspaceID: 5})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.PollInterval = time.Millisecond
	client.PublishTimeout = 2 * time.Second
	return client
}

func testVideo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("video-bytes"), 0o600); err != nil {
		t.Fatalf("write video: %v", err)
	}
	return path
}

func TestPublishYouTube(t *testing.T) {
	fake, server := newFakeInstaEdit(t, true, "published")
	client := testClient(t, server)
	result, err := client.PublishYouTube(context.Background(), ports.YouTubeUploadRequest{
		PlatformAccountID: 9,
		VideoPath:         testVideo(t),
		Title:             "Titolo",
		Description:       "Descrizione",
		Tags:              []string{"one", "two"},
		PrivacyStatus:     "private",
		IdempotencyKey:    "job-1",
	})
	if err != nil {
		t.Fatalf("PublishYouTube: %v", err)
	}
	if result.PostID != 77 || result.TargetID != 88 || result.VideoID != "vid-123" {
		t.Errorf("result = %#v", result)
	}
	if result.ChannelID != "UC-test" || result.ChannelName != "Test Channel" {
		t.Errorf("channel = %#v", result)
	}
	if result.VideoURL != "https://www.youtube.com/watch?v=vid-123" {
		t.Errorf("video url = %q", result.VideoURL)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.presignCalls != 1 || fake.uploadCalls != 1 {
		t.Errorf("presign/upload calls = %d/%d", fake.presignCalls, fake.uploadCalls)
	}
	if fake.uploadBody != "video-bytes" {
		t.Errorf("upload body = %q", fake.uploadBody)
	}
	if fake.idempotencyKey != "job-1" {
		t.Errorf("idempotency key = %q", fake.idempotencyKey)
	}
	if fake.pollCalls < 2 {
		t.Errorf("poll calls = %d", fake.pollCalls)
	}
	if fake.createBody["workspace_id"] != float64(5) || fake.createBody["status"] != "queued" {
		t.Errorf("create root = %#v", fake.createBody)
	}
	content, ok := fake.createBody["content"].(map[string]any)
	if !ok {
		t.Fatalf("content = %#v", fake.createBody["content"])
	}
	if content["title"] != "Titolo" || content["caption"] != "Descrizione" {
		t.Errorf("content = %#v", content)
	}
	media, ok := content["media"].([]any)
	if !ok || len(media) != 1 {
		t.Fatalf("media = %#v", content["media"])
	}
	if media[0].(map[string]any)["asset_id"] != "asset-1" {
		t.Errorf("media = %#v", media)
	}
	targets, ok := fake.createBody["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("targets = %#v", fake.createBody["targets"])
	}
	target := targets[0].(map[string]any)
	if target["platform_account_id"] != float64(9) {
		t.Errorf("target = %#v", target)
	}
	settings := target["settings"].(map[string]any)["youtube"].(map[string]any)
	if settings["title"] != "Titolo" || settings["privacy_status"] != "private" {
		t.Errorf("youtube settings = %#v", settings)
	}
}

func TestPublishYouTubeByDestinationID(t *testing.T) {
	fake, server := newFakeInstaEdit(t, true, "published")
	client := testClient(t, server)
	result, err := client.PublishYouTube(context.Background(), ports.YouTubeUploadRequest{
		DestinationID: "instaedit_extdst_test",
		WorkspaceID:   5,
		PublicationID: "main",
		VideoPath:     testVideo(t),
		Title:         "Titolo",
		PrivacyStatus: "unlisted",
	})
	if err != nil {
		t.Fatalf("PublishYouTube: %v", err)
	}
	if result.ChannelID != "UC-test" || result.ChannelName != "Test Channel" {
		t.Errorf("channel = %#v", result)
	}
	if result.DestinationID != "instaedit_extdst_test" || result.WorkspaceID != 5 || result.PublicationID != "main" {
		t.Errorf("destination = %#v", result)
	}
	if result.PlatformAccountID != 9 {
		t.Errorf("platform_account_id = %d, want 9", result.PlatformAccountID)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	targets, ok := fake.createBody["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("targets = %#v", fake.createBody["targets"])
	}
	if got := targets[0].(map[string]any)["platform_account_id"]; got != float64(9) {
		t.Errorf("resolved platform_account_id = %v, want 9", got)
	}
}

func TestPublishYouTubeRejectsUnknownDestination(t *testing.T) {
	fake, server := newFakeInstaEdit(t, true, "published")
	client := testClient(t, server)
	_, err := client.PublishYouTube(context.Background(), ports.YouTubeUploadRequest{
		DestinationID: "instaedit_extdst_missing",
		WorkspaceID:   5,
		VideoPath:     testVideo(t),
		Title:         "Titolo",
	})
	if err == nil || !strings.Contains(err.Error(), "destination_id") {
		t.Fatalf("error = %v, want destination_id validation", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.presignCalls != 0 {
		t.Errorf("presign calls = %d, want 0", fake.presignCalls)
	}
}

func TestPublishYouTubeRejectsForeignWorkspace(t *testing.T) {
	_, server := newFakeInstaEdit(t, true, "published")
	client := testClient(t, server)
	_, err := client.PublishYouTube(context.Background(), ports.YouTubeUploadRequest{
		DestinationID: "instaedit_extdst_test",
		WorkspaceID:   99,
		VideoPath:     testVideo(t),
		Title:         "Titolo",
	})
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("error = %v, want workspace_id validation", err)
	}
}

func TestPublishYouTubeRejectsUnpublishableTarget(t *testing.T) {
	fake, server := newFakeInstaEdit(t, false, "published")
	client := testClient(t, server)
	_, err := client.PublishYouTube(context.Background(), ports.YouTubeUploadRequest{
		PlatformAccountID: 9,
		VideoPath:         testVideo(t),
		Title:             "Titolo",
	})
	if err == nil || !strings.Contains(err.Error(), "not publishable") {
		t.Fatalf("error = %v, want not publishable", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.presignCalls != 0 {
		t.Errorf("presign calls = %d, want 0", fake.presignCalls)
	}
}

func TestPublishYouTubeTargetFailure(t *testing.T) {
	_, server := newFakeInstaEdit(t, true, "failed")
	client := testClient(t, server)
	_, err := client.PublishYouTube(context.Background(), ports.YouTubeUploadRequest{
		PlatformAccountID: 9,
		VideoPath:         testVideo(t),
		Title:             "Titolo",
	})
	if err == nil || !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want failed/boom", err)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("INSTAEDIT_PUBLISHING_BASE_URL", "https://api.example.test/")
	t.Setenv(APIKeyEnv, "key-1")
	t.Setenv(WorkspaceIDEnv, "5")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseURL != "https://api.example.test" || cfg.APIKey != "key-1" || cfg.WorkspaceID != 5 {
		t.Errorf("cfg = %#v", cfg)
	}
	t.Setenv(APIKeyEnv, "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig without API key succeeded")
	}
}
