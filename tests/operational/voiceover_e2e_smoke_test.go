// Package operational — voiceover_e2e_smoke_test.go
//
// FASE 7 E2E smoke test for the voiceover pipeline per the
// VO-OPERATIONAL-READINESS plan (2026-07-08).
//
// 6 verification steps:
//  1. POST /api/media/voiceover/generate with short Italian text
//  2. Poll the job until SUCCEEDED (or 3min timeout)
//  3. Verify voiceovers DB row with status=completed, drive_file_id non-empty
//  4. Verify media_assets projection row with source_job_id match
//  5. Verify outbox_events row created for asset.index.requested
//  6. Write JSON report for offline forensics
//
// Skip rules:
//   - go test -short (no live HTTP probes)
//   - VELOX_ADMIN_TOKEN unset (no auth)
//   - SMOKE_DRIVE_FOLDER_ID unset (needed for destination.kind=explicit)
//
// DB probe fallback: when sqlite3 is not on PATH, DB probes are skipped
// gracefully (errors.Is check) — the HTTP-side assertions ARE the primary
// signal; DB probes are additive observability.
//
// Pure-stdlib (no internal/* imports) — compiles cleanly with the
// 6 pre-existing build issues.
//
// Run:
//
//	VELOX_ADMIN_TOKEN=<token> SMOKE_DRIVE_FOLDER_ID=<id> go test -v -run TestVoiceoverE2ESmoke -timeout 5m ./tests/operational/...
//	make smoke-voiceover
package operational

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestVoiceoverE2ESmoke is the FASE 7 E2E gate for the voiceover pipeline.
func TestVoiceoverE2ESmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live voiceover E2E smoke in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover E2E smoke")
	}
	folderID := os.Getenv("SMOKE_DRIVE_FOLDER_ID")
	if folderID == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover E2E needs a real Drive folder_id for destination.kind=explicit")
	}

	h, err := NewVoiceoverHarness(t, HarnessOptions{
		FASE:   "E2E-SMOKE",
		DBPath: os.Getenv("SMOKE_DB"),
	})
	if err != nil {
		t.Fatalf("NewVoiceoverHarness: %v", err)
	}
	if h == nil {
		t.Skip("harness skipped (no live server)")
	}
	defer func() {
		if cerr := h.WriteReport(); cerr != nil {
			t.Logf("WriteReport: %v", cerr)
		}
	}()

	ctx := context.Background()

	// ── Step 1: POST /api/media/voiceover/generate ─────────────────
	// Send a single-item Italian voiceover request with Skip strategy
	// (DedupeByHash — safe to re-run without creating duplicate files).
	// destination.kind=explicit mirrors the canonical voiceover_b1_smoke.sh
	// pattern — uses SMOKE_DRIVE_FOLDER_ID so the test runs against a
	// real, operator-provisioned Drive folder.
	payload := map[string]any{
		"request_id": fmt.Sprintf("e2e-smoke-%d", time.Now().Unix()),
		"items": []map[string]any{
			{
				"text":     "Questa è una prova end-to-end del sistema di generazione vocale PipelineGen.",
				"language": "it-IT",
				"voice":    "DiegoNeural",
				"filename": "e2e-smoke-test",
			},
		},
		"destination": map[string]any{
			"kind":      "explicit",
			"folder_id": folderID,
		},
		"options": map[string]any{
			"strategy":    "skip",
			"parallelism": 1,
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	code, body, err := h.Curl(ctx, "POST", "/api/media/voiceover/generate", payloadBytes)
	if err != nil {
		t.Fatalf("POST /generate: %v", err)
	}
	h.AssertHTTPStatus("generate_dispatch", []string{"202"}, code)

	// Extract job_id from the 202 Accepted response.
	var genResp struct {
		OK    bool   `json:"ok"`
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &genResp); err != nil {
		t.Fatalf("unmarshal generate response: %v (body: %s)", err, string(body))
	}
	if genResp.JobID == "" {
		t.Fatalf("generate response missing job_id: %s", string(body))
	}
	h.RecordJobID("parent", genResp.JobID)
	t.Logf("voiceover.generate dispatched: job_id=%s", genResp.JobID)

	// ── Step 2: Poll until SUCCEEDED (3min budget) ────────────────
	deadline := time.Now().Add(3 * time.Minute)
	pollInterval := 3 * time.Second
	var terminalStatus string
	var terminalBody []byte

	for time.Now().Before(deadline) {
		code, body, err := h.Curl(ctx, "GET", "/api/jobs/"+genResp.JobID+"/full", nil)
		if err != nil {
			t.Logf("poll /jobs/%s/full: %v (retrying in %v)", genResp.JobID, err, pollInterval)
			time.Sleep(pollInterval)
			continue
		}
		if code != 200 {
			t.Logf("poll /jobs/%s/full returned %d (retrying in %v)", genResp.JobID, code, pollInterval)
			time.Sleep(pollInterval)
			continue
		}

		var jobResp struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &jobResp); err != nil {
			t.Logf("unmarshal job response: %v (retrying)", err)
			time.Sleep(pollInterval)
			continue
		}

		terminalBody = body
		if jobResp.Status == "SUCCEEDED" || jobResp.Status == "FAILED" || jobResp.Status == "DEAD_LETTERED" {
			terminalStatus = jobResp.Status
			break
		}
		t.Logf("job %s status=%s (polling every %v)", genResp.JobID, jobResp.Status, pollInterval)
		time.Sleep(pollInterval)
	}

	if terminalStatus == "" {
		t.Fatalf("job %s did not reach a terminal state within 3min (last body: %s)", genResp.JobID, string(terminalBody))
	}
	h.Assert("job_terminal_status", "SUCCEEDED", terminalStatus)
	t.Logf("voiceover job %s: %s", genResp.JobID, terminalStatus)

	// ── Step 3: Verify voiceovers DB row ──────────────────────────
	// Gracefully skip DB probes when sqlite3 is not on PATH — the
	// HTTP-side assertions are the primary signal; DB probes are
	// additive observability. The harness returns ErrSqliteBinaryMissing
	// as a typed sentinel that callers can distinguish from query errors.
	voRows, voErr := h.ProbeVoiceovers(genResp.JobID)
	if errors.Is(voErr, ErrSqliteBinaryMissing) {
		t.Logf("sqlite3 not on PATH; skipping DB probes (HTTP assertions already passed)")
	} else if voErr != nil {
		t.Fatalf("ProbeVoiceovers: %v", voErr)
	} else {
		h.Assertf("probe_voiceovers_rows", ">=1", "%d", len(voRows))
		for _, row := range voRows {
			t.Logf("voiceover row: %s", row)
		}
		// Verify at least one row has status=completed and non-empty drive_file_id.
		found := false
		for _, row := range voRows {
			cols := strings.Split(row, "|")
			if len(cols) >= 5 {
				status := strings.TrimSpace(cols[2])
				driveFileID := strings.TrimSpace(cols[1])
				if (status == "completed" || status == "succeeded") && driveFileID != "" {
					found = true
					h.Assert("voiceover_drive_file", "non-empty", driveFileID)
					break
				}
			}
		}
		if !found {
			h.Assert("voiceover_completed_row", "found", "not-found")
		}
	}

	// ── Step 4: Verify media_assets projection ────────────────────
	maRows, maErr := h.ProbeMediaAssets(genResp.JobID)
	if errors.Is(maErr, ErrSqliteBinaryMissing) {
		t.Logf("sqlite3 not on PATH; skipping media_assets probe")
	} else if maErr != nil {
		t.Fatalf("ProbeMediaAssets: %v", maErr)
	} else {
		h.Assertf("probe_media_assets_rows", ">=1", "%d", len(maRows))
		for _, row := range maRows {
			t.Logf("media_asset row: %s", row)
		}
	}

	// ── Step 5: Verify outbox events ──────────────────────────────
	obRows, obErr := h.ProbeOutboxEvents(genResp.JobID)
	if errors.Is(obErr, ErrSqliteBinaryMissing) {
		t.Logf("sqlite3 not on PATH; skipping outbox_events probe")
	} else if obErr != nil {
		t.Fatalf("ProbeOutboxEvents: %v", obErr)
	} else {
		h.Assertf("probe_outbox_rows", ">=1", "%d", len(obRows))
		for _, row := range obRows {
			t.Logf("outbox row: %s", row)
		}
	}

	// ── Step 6: Report written automatically via defer ────────────
	t.Logf("voiceover E2E smoke complete — report: %s", h.reportPath)
}
