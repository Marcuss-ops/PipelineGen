package renderinggen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

func TestClientSubmitCreated(t *testing.T) {
	var gotBody json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"job-1"}`))
	}))
	defer srv.Close()

	client := New(srv.URL)
	if err := client.Submit(context.Background(), scriptgen.RenderQueueJob{ID: "job-1", JobType: "overlay.render"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("decode submitted body: %v", err)
	}
	if got, _ := body["job_type"].(string); got != "overlay.render" {
		t.Fatalf("wire job_type = %q, want exactly overlay.render", got)
	}
}

func TestClientSubmitConflictIsErrJobExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "job job-1 already exists", http.StatusConflict)
	}))
	defer srv.Close()

	client := New(srv.URL)
	err := client.Submit(context.Background(), scriptgen.RenderQueueJob{ID: "job-1"})
	if !errors.Is(err, scriptgen.ErrJobExists) {
		t.Fatalf("expected ErrJobExists, got %v", err)
	}
}

func TestToScriptArtifactMapsCopyCertification(t *testing.T) {
	in := &queueclient.Artifact{
		ID:                 "art-1",
		Kind:               "video",
		StorageKey:         "overlay/2026/08/job-1/overlay.mp4",
		ArtifactURL:        "https://store/overlay.mp4",
		ArtifactHash:       "abc",
		ContentType:        "video/mp4",
		SizeBytes:          28382193,
		Width:              1920,
		Height:             1080,
		FPSNum:             30,
		FPSDen:             1,
		FrameCount:         546,
		DurationUS:         18200000,
		ProfileID:          "velox-h264-copy-v1",
		CopyEligible:       true,
		Codec:              "h264",
		CodecProfile:       "high",
		ClosedGOP:          true,
		FirstFrameKeyframe: true,
	}
	got := toScriptArtifact(in)
	if got == nil {
		t.Fatal("nil artifact")
	}
	if got.ID != in.ID || got.SHA256 != in.ArtifactHash || got.ProfileID != in.ProfileID ||
		got.CopyEligible != in.CopyEligible || got.ClosedGOP != in.ClosedGOP ||
		got.FirstFrameKeyframe != in.FirstFrameKeyframe || got.Codec != in.Codec ||
		got.CodecProfile != in.CodecProfile || got.Width != in.Width ||
		got.Height != in.Height || got.FPSNum != in.FPSNum || got.FPSDen != in.FPSDen ||
		got.StorageKey != in.StorageKey || got.DurationUS != in.DurationUS ||
		got.FrameCount != in.FrameCount || got.SizeBytes != in.SizeBytes {
		t.Fatalf("artifact mapping lost fields: %+v", got)
	}
}

func TestToScriptArtifactMapsAnalyticsFields(t *testing.T) {
	in := &queueclient.Artifact{
		ArtifactHash: "abc",
		Metrics:      map[string]float64{"render_ms": 900.9, "encode_ms": 300.1, "publish_ms": 12},
		DriveFileID:  "drive-file-1",
		DriveLink:    "https://drive.google.com/file/d/drive-file-1/view",
	}
	got := toScriptArtifact(in)
	if got == nil {
		t.Fatal("nil artifact")
	}
	if got.RenderMS != 900 || got.EncodeMS != 300 {
		t.Fatalf("render/encode = %d/%d, want 900/300 (ms, rounded down)", got.RenderMS, got.EncodeMS)
	}
	if got.DriveFileID != "drive-file-1" || got.DriveLink != "https://drive.google.com/file/d/drive-file-1/view" {
		t.Fatalf("drive identity lost: %+v", got)
	}

	// Absent metrics map → zeros, never a panic.
	if none := toScriptArtifact(&queueclient.Artifact{}); none.RenderMS != 0 || none.EncodeMS != 0 || none.DriveFileID != "" {
		t.Fatalf("empty artifact must map to zero analytics: %+v", none)
	}
}

func TestToScriptArtifactMapsRenderingGenPhaseTimings(t *testing.T) {
	in := &queueclient.Artifact{
		ArtifactHash: "abc",
		Metrics: map[string]float64{
			"render_ms":             900,
			"encode_ms":             300,
			"materialize_us":        420000,
			"overlay_compile_us":    15000,
			"probe_us":              25000,
			"sha256_us":             8000,
			"objectstore_upload_us": 31000,
			"drive_publish_ms":      240,
		},
	}
	got := toScriptArtifact(in)
	if got == nil {
		t.Fatal("nil artifact")
	}
	if got.RenderMS != 900 || got.EncodeMS != 300 {
		t.Fatalf("render/encode = %d/%d, want 900/300", got.RenderMS, got.EncodeMS)
	}
	if got.MaterializeMS != 420 || got.PlanMS != 15 || got.ProbeMS != 25 || got.HashMS != 8 || got.UploadMS != 31 || got.DrivePublishMS != 240 {
		t.Fatalf("phase timings = %+v, want materialize=420 plan=15 probe=25 hash=8 upload=31 drive_publish=240", got)
	}

	// Millisecond keys win over microsecond keys when both are present.
	both := toScriptArtifact(&queueclient.Artifact{Metrics: map[string]float64{"materialize_us": 500000, "materialize_ms": 999}})
	if both.MaterializeMS != 999 {
		t.Fatalf("ms key must win over us key: got %d", both.MaterializeMS)
	}

	// Missing phase keys stay zero (a missing measurement is never faked).
	none := toScriptArtifact(&queueclient.Artifact{Metrics: map[string]float64{"render_ms": 10}})
	if none.MaterializeMS != 0 || none.PlanMS != 0 || none.ProbeMS != 0 || none.HashMS != 0 || none.UploadMS != 0 || none.DrivePublishMS != 0 {
		t.Fatalf("absent phases must map to zero: %+v", none)
	}
}

func TestClientGetDecodesArtifact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/job-1" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"job-1","state":"completed","artifact":{"id":"art-1","copy_eligible":true}}`))
	}))
	defer srv.Close()

	client := New(srv.URL)
	job, err := client.Get(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if job.State != "completed" || job.Artifact == nil || job.Artifact.ID != "art-1" || !job.Artifact.CopyEligible {
		t.Fatalf("unexpected job: %+v", job)
	}
}
