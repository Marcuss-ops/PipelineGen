package artlist

import (
	"encoding/json"
	"testing"

	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

func TestArtlistDedupKeyUsesCanonicalRequest(t *testing.T) {
	reqA := RunTagRequest{
		Term:     "  City ",
		Strategy: "",
		DryRun:   false,
		Limit:    8,
	}

	reqB := RunTagRequest{
		Term:     "city",
		Strategy: "verify",
		DryRun:   false,
		Limit:    8,
	}

	normA := NormalizeRunTagRequest(reqA, RunDefaults{
		DefaultRootFolderID: "drive-folder",
	})

	normB := NormalizeRunTagRequest(reqB, RunDefaults{
		DefaultRootFolderID: "drive-folder",
	})

	keyA := RunDedupKey(normA.Term, normA.RootFolderID, normA.Strategy, normA.DryRun, normA.Limit)
	keyB := RunDedupKey(normB.Term, normB.RootFolderID, normB.Strategy, normB.DryRun, normB.Limit)

	if keyA != keyB {
		t.Fatalf("expected canonical equivalent requests to share dedup key: %s != %s", keyA, keyB)
	}
}

// TestArtlistDedupKey_DifferentLimitProducesDifferentKey pins the
// Fase 5 / Commit 3 follow-up invariant: the dedup key MUST vary with
// the limit parameter. A replay of the same term + root folder +
// strategy + dryRun with a DIFFERENT limit is a DIFFERENT run, not a
// same-run replay; omitting limit from the key would collapse
// distinct runs into one ActiveKey and surface misleading dedup
// (godlike/07 fail-closed: never silently merge distinct operator
// requests).
func TestArtlistDedupKey_DifferentLimitProducesDifferentKey(t *testing.T) {
	key8 := RunDedupKey("city", "drive-folder", "verify", false, 8)
	key16 := RunDedupKey("city", "drive-folder", "verify", false, 16)
	key1 := RunDedupKey("city", "drive-folder", "verify", false, 1)

	if key8 == key16 {
		t.Fatalf("expected limit=8 and limit=16 to produce different dedup keys (a different limit is a different run); both produced %q", key8)
	}
	if key8 == key1 {
		t.Fatalf("expected limit=8 and limit=1 to produce different dedup keys; both produced %q", key8)
	}
	if key16 == key1 {
		t.Fatalf("expected limit=16 and limit=1 to produce different dedup keys; both produced %q", key16)
	}
}

func TestArtlistJobResultRoundTrip(t *testing.T) {
	resp := &RunTagResponse{
		OK:          true,
		Term:        "city",
		Status:      "completed",
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
	job := &domainjob.Job{
		ID:     "test-job",
		Type:   "artlist.run",
		Status: domainjob.StatusSucceeded,
	}
	jsonPayload, _ := json.Marshal(codec.PayloadFromRequest(&RunTagRequest{Term: "city"}))
	job.Payload = jsonPayload
	job.Result = mustRawMessage(result)

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

func mustRawMessage(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
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
		t.Fatalf("term not normalized correctly: got %q", normalized.Term)
	}
	if normalized.Limit != 5 {
		t.Fatalf("default limit not applied: got %d", normalized.Limit)
	}
}

func TestNormalizeSearchTermLimitsToFourWords(t *testing.T) {
	if got := normalizeSearchTerm("  blue ocean sunset "); got != "blue ocean sunset" {
		t.Fatalf("expected three words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("single"); got != "single" {
		t.Fatalf("expected single word unchanged, got %q", got)
	}
	if got := normalizeSearchTerm("one two three four five"); got != "one two three four" {
		t.Fatalf("expected four words max, got %q", got)
	}
}

func TestNormalizeRunTagRequestRootFallback(t *testing.T) {
	req := RunTagRequest{
		Term: "test",
	}

	normalized := NormalizeRunTagRequest(req, RunDefaults{
		DefaultRootFolderID: "artlist-root",
	})

	if normalized.RootFolderID != "artlist-root" {
		t.Fatalf("root folder fallback not applied: got %q", normalized.RootFolderID)
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

func TestJobCodecPayloadFromRequestNilSafe(t *testing.T) {
	codec := &JobCodec{}

	payload := codec.PayloadFromRequest(nil)

	if payload == nil {
		t.Fatal("expected empty payload map, got nil")
	}

	if len(payload) != 0 {
		t.Fatalf("expected empty payload map, got %v", payload)
	}
}

func TestJobCodecPayloadFromRequestTrimsCanonicalFields(t *testing.T) {
	codec := &JobCodec{}

	payload := codec.PayloadFromRequest(&RunTagRequest{
		Term:         "  boxing highlights  ",
		RootFolderID: "  root123  ",
		Strategy:     "  verify  ",
		Limit:        3,
	})

	if payload["term"] != "boxing highlights" {
		t.Fatalf("expected trimmed term, got %q", payload["term"])
	}

	if payload["root_folder_id"] != "root123" {
		t.Fatalf("expected trimmed root_folder_id, got %q", payload["root_folder_id"])
	}

	if payload["strategy"] != "verify" {
		t.Fatalf("expected trimmed strategy, got %q", payload["strategy"])
	}
}
