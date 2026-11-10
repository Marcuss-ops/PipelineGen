package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

type uploadStubPublisher struct {
	got    youtubeports.YouTubeUploadRequest
	result *youtubeports.YouTubeUploadResult
	err    error
	calls  int
}

func (s *uploadStubPublisher) PublishYouTube(_ context.Context, req youtubeports.YouTubeUploadRequest) (*youtubeports.YouTubeUploadResult, error) {
	s.calls++
	s.got = req
	return s.result, s.err
}

func uploadJob(t *testing.T, payload map[string]any) *kerneljob.Job {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &kerneljob.Job{ID: "job-upload-1", Type: "youtube.upload", Payload: raw}
}

func TestUploadJobHandlerPublishes(t *testing.T) {
	stub := &uploadStubPublisher{result: &youtubeports.YouTubeUploadResult{
		PostID: 77, TargetID: 88, VideoID: "vid-123", Status: "published", PrivacyStatus: "private",
		ChannelID: "UC-test", ChannelName: "Test Channel",
	}}
	handler := NewUploadJobHandler(stub, zap.NewNop())
	result, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"platform_account_id": 9,
		"video_path":          "/tmp/clip.mp4",
		"title":               "  Titolo  ",
		"description":         " Descrizione ",
		"tags":                []string{"one", " One ", "two"},
		"privacy_status":      "public",
		"made_for_kids":       true,
	}), &jobtools.JobTools{})
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("publisher calls = %d, want 1", stub.calls)
	}
	if stub.got.IdempotencyKey != "job-upload-1" {
		t.Errorf("idempotency key = %q", stub.got.IdempotencyKey)
	}
	if stub.got.Title != "Titolo" || stub.got.Description != "Descrizione" {
		t.Errorf("trimmed strings = %q / %q", stub.got.Title, stub.got.Description)
	}
	if got, want := strings.Join(stub.got.Tags, ","), "one,two"; got != want {
		t.Errorf("tags = %q, want %q", got, want)
	}
	if stub.got.PrivacyStatus != "public" || !stub.got.MadeForKids {
		t.Errorf("privacy/madeForKids = %q/%v", stub.got.PrivacyStatus, stub.got.MadeForKids)
	}
	if result["video_id"] != "vid-123" || result["post_id"] != int64(77) || result["target_id"] != int64(88) {
		t.Errorf("result = %#v", result)
	}
	if result["video_url"] != "https://youtu.be/vid-123" {
		t.Errorf("video_url = %v", result["video_url"])
	}
}

func TestUploadJobHandlerDefaultsToPrivate(t *testing.T) {
	stub := &uploadStubPublisher{result: &youtubeports.YouTubeUploadResult{Status: "published"}}
	handler := NewUploadJobHandler(stub, nil)
	_, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"platform_account_id": 9,
		"video_url":           "https://example.com/clip.mp4",
		"title":               "Titolo",
	}), nil)
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if stub.got.PrivacyStatus != "private" {
		t.Errorf("privacy = %q, want private", stub.got.PrivacyStatus)
	}
}

func TestUploadJobHandlerRejectsMissingTarget(t *testing.T) {
	stub := &uploadStubPublisher{}
	handler := NewUploadJobHandler(stub, nil)
	_, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"video_path": "/tmp/clip.mp4",
		"title":      "Titolo",
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "platform_account_id") {
		t.Fatalf("error = %v, want platform_account_id validation", err)
	}
	if stub.calls != 0 {
		t.Errorf("publisher calls = %d, want 0", stub.calls)
	}
}

func TestUploadJobHandlerRejectsInvalidPrivacy(t *testing.T) {
	stub := &uploadStubPublisher{}
	handler := NewUploadJobHandler(stub, nil)
	_, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"platform_account_id": 9,
		"video_path":          "/tmp/clip.mp4",
		"title":               "Titolo",
		"privacy_status":      "secret",
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "privacy_status") {
		t.Fatalf("error = %v, want privacy_status validation", err)
	}
	if stub.calls != 0 {
		t.Errorf("publisher calls = %d, want 0", stub.calls)
	}
}

func TestUploadJobHandlerAcceptsPublishingTargetAndPublication(t *testing.T) {
	stub := &uploadStubPublisher{result: &youtubeports.YouTubeUploadResult{
		PostID: 77, TargetID: 88, VideoID: "vid-123", Status: "pending",
		PrivacyStatus: "unlisted", WorkspaceID: 5,
		DestinationID: "instaedit_extdst_test", PublicationID: "main", PlatformAccountID: 9,
		ChannelID: "UC-test", ChannelName: "Test Channel",
	}}
	handler := NewUploadJobHandler(stub, nil)
	result, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"video_path": "/tmp/clip.mp4",
		"title":      "Legacy title",
		"publishing_target": map[string]any{
			"workspace_id":   5,
			"type":           "channel",
			"destination_id": "instaedit_extdst_test",
		},
		"publications": []any{map[string]any{
			"publication_id": "main",
			"output_ref":     map[string]any{"variant_id": "final"},
			"metadata": map[string]any{
				"title":         "Titolo",
				"description":   "Descrizione",
				"tags":          []string{"uno", "due"},
				"privacy":       "unlisted",
				"made_for_kids": true,
			},
		}},
	}), &jobtools.JobTools{})
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if stub.got.PlatformAccountID != 0 {
		t.Errorf("platform_account_id = %d, want 0 (resolved from destination)", stub.got.PlatformAccountID)
	}
	if stub.got.DestinationID != "instaedit_extdst_test" || stub.got.WorkspaceID != 5 {
		t.Errorf("target = %#v", stub.got)
	}
	if stub.got.PublicationID != "main" {
		t.Errorf("publication_id = %q", stub.got.PublicationID)
	}
	if stub.got.Title != "Titolo" || stub.got.Description != "Descrizione" {
		t.Errorf("metadata = %q / %q", stub.got.Title, stub.got.Description)
	}
	if got, want := strings.Join(stub.got.Tags, ","), "uno,due"; got != want {
		t.Errorf("tags = %q, want %q", got, want)
	}
	if stub.got.PrivacyStatus != "unlisted" || !stub.got.MadeForKids {
		t.Errorf("privacy/madeForKids = %q/%v", stub.got.PrivacyStatus, stub.got.MadeForKids)
	}
	if result["destination_id"] != "instaedit_extdst_test" || result["publication_id"] != "main" || result["workspace_id"] != int64(5) {
		t.Errorf("result = %#v", result)
	}
	if result["platform_account_id"] != int64(9) {
		t.Errorf("platform_account_id = %v, want 9", result["platform_account_id"])
	}
}

func TestUploadJobHandlerRejectsMultiplePublications(t *testing.T) {
	stub := &uploadStubPublisher{}
	handler := NewUploadJobHandler(stub, nil)
	_, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"video_path": "/tmp/clip.mp4",
		"title":      "Titolo",
		"publishing_target": map[string]any{
			"workspace_id":   5,
			"type":           "channel",
			"destination_id": "instaedit_extdst_test",
		},
		"publications": []any{
			map[string]any{"publication_id": "main", "metadata": map[string]any{"title": "Titolo"}},
			map[string]any{"publication_id": "second", "metadata": map[string]any{"title": "Titolo"}},
		},
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "one publication") {
		t.Fatalf("error = %v, want one publication validation", err)
	}
	if stub.calls != 0 {
		t.Errorf("publisher calls = %d, want 0", stub.calls)
	}
}

func TestUploadJobHandlerRejectsGroupTarget(t *testing.T) {
	stub := &uploadStubPublisher{}
	handler := NewUploadJobHandler(stub, nil)
	_, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"video_path": "/tmp/clip.mp4",
		"title":      "Titolo",
		"publishing_target": map[string]any{
			"workspace_id": 5,
			"type":         "group",
			"group_id":     12,
		},
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "type must be channel") {
		t.Fatalf("error = %v, want channel type validation", err)
	}
	if stub.calls != 0 {
		t.Errorf("publisher calls = %d, want 0", stub.calls)
	}
}

func TestUploadJobHandlerPropagatesPublisherError(t *testing.T) {
	stub := &uploadStubPublisher{err: errors.New("target failed")}
	handler := NewUploadJobHandler(stub, nil)
	_, err := handler.HandleJob(context.Background(), uploadJob(t, map[string]any{
		"platform_account_id": 9,
		"video_path":          "/tmp/clip.mp4",
		"title":               "Titolo",
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "target failed") {
		t.Fatalf("error = %v, want publisher error", err)
	}
}
