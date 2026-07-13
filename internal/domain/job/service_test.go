package job

import (
	"testing"
)

// ── EnqueueRequest fields ───────────────────────────────────────────────

func TestEnqueueRequest_AllFields(t *testing.T) {
	req := &EnqueueRequest{
		Type:          "media.artlist",
		Payload:       map[string]string{"key": "val"},
		CorrelationID: "corr-123",
		MaxRetries:    3,
		Priority:      1,
		Project:       "my-project",
		ActiveKey:     "ak-456",
		VideoName:     "test-video.mp4",
	}
	if req.Type != "media.artlist" {
		t.Error("Type field mismatch")
	}
	if req.Project != "my-project" {
		t.Error("Project field mismatch")
	}
	if req.ActiveKey != "ak-456" {
		t.Error("ActiveKey field mismatch")
	}
	if req.VideoName != "test-video.mp4" {
		t.Error("VideoName field mismatch")
	}
	if req.Priority != 1 {
		t.Error("Priority field mismatch")
	}
	if req.CorrelationID != "corr-123" {
		t.Error("CorrelationID field mismatch")
	}
}
