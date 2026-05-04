package artlist

import (
	"encoding/json"
	"testing"

	"velox/go-master/pkg/models"
)

func TestArtlistDedupKeyUsesCanonicalRequest(t *testing.T) {
	reqA := RunTagRequest{
		Term:     "  City ",
		Strategy: "",
		DryRun:   false,
	}

	reqB := RunTagRequest{
		Term:     "city",
		Strategy: "verify",
		DryRun:   false,
	}

	normA := NormalizeRunTagRequest(reqA, RunDefaults{
		DefaultRootFolderID: "drive-folder",
	})

	normB := NormalizeRunTagRequest(reqB, RunDefaults{
		DefaultRootFolderID: "drive-folder",
	})

	keyA := RunDedupKey(normA.Term, normA.RootFolderID, normA.Strategy, normA.DryRun)
	keyB := RunDedupKey(normB.Term, normB.RootFolderID, normB.Strategy, normB.DryRun)

	if keyA != keyB {
		t.Fatalf("expected canonical equivalent requests to share dedup key: %s != %s", keyA, keyB)
	}
}

func TestArtlistRunTerminalStatuses(t *testing.T) {
	terminal := []RunStatus{
		RunCompleted,
		RunCompletedDryRun,
		RunFailed,
		RunCancelled,
	}

	for _, status := range terminal {
		if !IsTerminalRunStatus(status) {
			t.Fatalf("expected %s to be terminal", status)
		}
	}

	nonTerminal := []RunStatus{
		RunQueued,
		RunRunning,
	}

	for _, status := range nonTerminal {
		if IsTerminalRunStatus(status) {
			t.Fatalf("expected %s to be non-terminal", status)
		}
	}
}

func TestArtlistJobPayloadRoundTrip(t *testing.T) {
	original := RunTagRequest{
		Term:         "city",
		Limit:        5,
		RootFolderID: "folder123",
		Strategy:     "verify",
		DryRun:       true,
	}

	// Test codec PayloadFromRequest
	codec := &JobCodec{}
	payload := codec.PayloadFromRequest(&original)

	// payload is already map[string]any
	// Test codec RequestFromPayload
	decoded := codec.RequestFromPayload(payload)

	if decoded.Term != original.Term {
		t.Fatalf("term mismatch: got %q want %q", decoded.Term, original.Term)
	}
	if decoded.Limit != original.Limit {
		t.Fatalf("limit mismatch: got %d want %d", decoded.Limit, original.Limit)
	}
	if decoded.RootFolderID != original.RootFolderID {
		t.Fatalf("root folder mismatch")
	}
	if decoded.Strategy != original.Strategy {
		t.Fatalf("strategy mismatch")
	}
	if decoded.DryRun != original.DryRun {
		t.Fatalf("dry run mismatch")
	}
}

func TestArtlistJobResultRoundTrip(t *testing.T) {
	resp := &RunTagResponse{
		OK:          true,
		Term:        "city",
		Status:      string(RunCompleted),
		Found:       2,
		Processed:   1,
		Skipped:     1,
		Failed:      0,
		TagFolderID: "folder123",
		Items: []RunTagItem{
			{
				ClipID:       "clip1",
				Name:         "City skyline",
				Filename:     "city.mp4",
				Status:       "completed",
				DriveLink:    "https://drive.google.com/file/d/abc",
				LocalPath:    "/tmp/city.mp4",
				FileHash:     "hash123",
				DownloadLink: "https://example.com/dl/abc",
			},
		},
	}

	codec := &JobCodec{}
	result := codec.ResultFromResponse(resp)

	// Check result map contains expected values
	if result["term"] != "city" {
		t.Fatalf("term not preserved in result")
	}
	if result["processed"] != 1 {
		t.Fatalf("processed mismatch: got %v want 1", result["processed"])
	}
	if result["found"] != 2 {
		t.Fatalf("found mismatch")
	}

	// Convert back to RunTagResponse
	job := &models.Job{
		ID:     "test-job",
		Type:   "artlist.run",
		Status: models.StatusCompleted,
	}
	jsonPayload, _ := json.Marshal(codec.PayloadFromRequest(&RunTagRequest{Term: "city"}))
	job.Payload = jsonPayload
	job.Result = result

	converted := codec.ResponseFromJob(job)
	if converted.Processed != resp.Processed {
		t.Fatalf("processed mismatch: got %d want %d", converted.Processed, resp.Processed)
	}
	if len(converted.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(converted.Items))
	}
	if converted.Items[0].ClipID != "clip1" {
		t.Fatalf("item clip id lost")
	}
}

func TestNormalizeRunTagRequest(t *testing.T) {
	// Test basic normalization
	req := RunTagRequest{
		Term:     "  Test Term ",
		Limit:    0,
		Strategy: "",
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{
		DefaultLimit: 5,
		MaxLimit:     500,
	})

	if normalized.Term != "Test Term" {
		t.Fatalf("term not trimmed: got %q", normalized.Term)
	}
	if normalized.Limit != 5 {
		t.Fatalf("default limit not applied: got %d", normalized.Limit)
	}
}

func TestNormalizeRunTagRequestWithForceReupload(t *testing.T) {
	// Test ForceReupload conversion
	req := RunTagRequest{
		Term:          "test",
		ForceReupload: true,
		Strategy:      "",
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{})

	if normalized.ForceReupload {
		t.Fatal("ForceReupload should be cleared after normalization")
	}
	if normalized.Strategy != "replace" {
		t.Fatalf("expected strategy to be 'replace', got %q", normalized.Strategy)
	}
}

func TestNormalizeRunTagRequestMaxLimit(t *testing.T) {
	req := RunTagRequest{
		Term:  "test",
		Limit: 1000,
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{
		MaxLimit: 500,
	})

	if normalized.Limit != 500 {
		t.Fatalf("max limit not applied: got %d", normalized.Limit)
	}
}
