package videocreate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestPayload_StrictDecode pins the §3 boundary: infrastructure facts
// in the payload fail CLOSED (DisallowUnknownFields), and the typed
// Validate rejects incomplete/out-of-vocabulary requests. Both failures
// are TERMINAL (ErrInvalidPayload) — retrying cannot help.
func TestPayload_StrictDecode(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"infrastructure field", `{"topic":"t","duration_seconds":60,"media_sources":["youtube"],"worker_ip":"10.0.0.1"}`},
		{"renderinggen url", `{"topic":"t","duration_seconds":60,"media_sources":["youtube"],"renderinggen_url":"http://x"}`},
		{"filesystem path", `{"topic":"t","duration_seconds":60,"media_sources":["youtube"],"output_path":"/tmp/x"}`},
		{"missing topic", `{"duration_seconds":60,"media_sources":["youtube"]}`},
		{"zero duration", `{"topic":"t","duration_seconds":0,"media_sources":["youtube"]}`},
		{"unknown source", `{"topic":"t","duration_seconds":60,"media_sources":["vimeo"]}`},
	}
	for _, c := range cases {
		if _, err := decodePayload(json.RawMessage(c.raw)); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("%s: err = %v, want ErrInvalidPayload", c.name, err)
		}
	}
	valid := `{"topic":"t","duration_seconds":60,"media_sources":["youtube","stock"],"voiceover":true,"overlays":true}`
	if _, err := decodePayload(json.RawMessage(valid)); err != nil {
		t.Errorf("valid payload rejected: %v", err)
	}
}

func TestHandler_FailsBeforeWorkWhenRequiredChildHandlerMissing(t *testing.T) {
	children := newFakeChildren()
	children.missingHandlers[appjobs.TypeAssemblyFinalize] = true
	deps, _, _ := newTestDeps(t, children, fakeProbe{})
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	j := testJob("job_missing_assembly", "missing-assembly")
	_, err = handler(context.Background(), j, nil)
	if !errors.Is(err, ErrChildHandlerUnavailable) {
		t.Fatalf("handler error = %v, want ErrChildHandlerUnavailable", err)
	}
	if got := children.enqueueCount(); got != 0 {
		t.Fatalf("enqueued %d children despite missing required assembly handler", got)
	}
	rows, err := deps.Steps.ListByJob(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("ListByJob: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("persisted %d workflow steps before child availability preflight", len(rows))
	}
}

// TestHandler_E2E_FakeBacked is the repo-reachable form of the §25/§26
// acceptance run: submit → whole chain → typed result with media_url /
// sha256 / child ledger / completed stages / thumbnail context, plus
// the canonical artifact manifest. The REPLAY half asserts the §26
// "Replay → nessun duplicato" criterion: a second delivery of the same
// job creates zero new children and returns the SAME published identity.
func TestHandler_E2E_FakeBacked(t *testing.T) {
	children := newFakeChildren()
	deps, _, publisher := newTestDeps(t, children, fakeProbe{})
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	j := testJob("job_e2e", "e2e-video-create-001")

	res, err := handler(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("e2e run: %v", err)
	}

	// §18 result contract.
	finalVideo, _ := res["final_video"].(map[string]any)
	if finalVideo == nil {
		t.Fatalf("no final_video in %v", res)
	}
	for _, field := range []string{"asset_id", "media_url", "drive_file_id", "sha256", "size_bytes", "duration_ms"} {
		if finalVideo[field] == nil || finalVideo[field] == "" || finalVideo[field] == int64(0) {
			t.Errorf("final_video.%s = %v, want non-empty", field, finalVideo[field])
		}
	}
	if videoID, _ := res["video_id"].(string); videoID == "" {
		t.Errorf("video_id missing: %v", res)
	}
	switch durationMS := res["duration_ms"].(type) {
	case int64:
		if durationMS <= 0 {
			t.Errorf("duration_ms = %v, want > 0", durationMS)
		}
	case float64:
		if durationMS <= 0 {
			t.Errorf("duration_ms = %v, want > 0", durationMS)
		}
	default:
		t.Errorf("duration_ms = %v (%T), want numeric", res["duration_ms"], res["duration_ms"])
	}

	// §8/§17 child ledger.
	childrenLedger, _ := res["children"].(map[string]any)
	if childrenLedger == nil {
		t.Fatalf("no children ledger: %v", res)
	}
	if len(childrenLedger["youtube"].([]any)) == 0 || len(childrenLedger["stock"].([]any)) == 0 {
		t.Errorf("children ledger incomplete: %v", childrenLedger)
	}
	if childrenLedger["render"] == nil || childrenLedger["script"] == "" {
		t.Errorf("children ledger missing render/script: %v", childrenLedger)
	}

	// §19-B thumbnail context (option B: the 51 owns the cover).
	thumb, _ := res["thumbnail_context"].(map[string]any)
	if thumb == nil || thumb["suggested_prompt"] == "" {
		t.Errorf("thumbnail_context missing: %v", res)
	}

	// §18 artifact manifest: the canonical __artifact_manifest hand-off
	// must validate and carry the ALREADY-PUBLISHED remote identities.
	manifest, err := job.Decode(map[string]any(res))
	if err != nil {
		t.Fatalf("artifact manifest: %v", err)
	}
	if manifest == nil || len(manifest.Artifacts) != 1 {
		t.Fatalf("manifest = %#v, want 1 artifact", manifest)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest.Validate: %v", err)
	}
	entry := manifest.Artifacts[0]
	if entry.Kind != job.ArtifactKindFinalVideo || entry.RemoteFileID != "drive_file_final" {
		t.Errorf("manifest entry = %#v", entry)
	}

	// ── Replay (§26 "nessun duplicato") ─────────────────────────────
	countAfterFirst := children.enqueueCount()
	res2, err := handler(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := children.enqueueCount(); got != countAfterFirst {
		t.Errorf("replay created new children: %d -> %d", countAfterFirst, got)
	}
	if publisher.calls != 1 {
		t.Errorf("replay re-published: publisher calls = %d, want 1", publisher.calls)
	}
	finalVideo2, _ := res2["final_video"].(map[string]any)
	if finalVideo2["media_url"] != finalVideo["media_url"] || finalVideo2["sha256"] != finalVideo["sha256"] {
		t.Errorf("replay diverged: %v vs %v", finalVideo, finalVideo2)
	}
}

// TestNewHandler_FailsClosedWithoutDeps pins godlike/05: an incomplete
// dependency bundle must not produce a handler.
func TestNewHandler_FailsClosedWithoutDeps(t *testing.T) {
	if _, err := NewHandler(Deps{}); err == nil {
		t.Fatal("NewHandler(Deps{}) succeeded, want missing-dependency error")
	}
}

// TestVideoCreateResultContract pins the typed result validation used by
// the codec round-trip and the handler (a result without media_url or
// sha256 must never be constructible into a SUCCEEDED job).
func TestVideoCreateResultContract(t *testing.T) {
	bad := appjobs.VideoCreateResult{VideoID: "v"}
	if err := bad.Validate(); err == nil {
		t.Fatal("empty result must validate-fail")
	}
}
