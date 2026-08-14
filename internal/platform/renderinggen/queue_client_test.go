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
	if err := client.Submit(context.Background(), scriptgen.RenderQueueJob{ID: "job-1"}); err != nil {
		t.Fatalf("submit: %v", err)
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
		URL:                "https://store/overlay.mp4",
		SHA256:             "abc",
		MimeType:           "video/mp4",
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
	if got.ID != in.ID || got.SHA256 != in.SHA256 || got.ProfileID != in.ProfileID ||
		got.CopyEligible != in.CopyEligible || got.ClosedGOP != in.ClosedGOP ||
		got.FirstFrameKeyframe != in.FirstFrameKeyframe || got.Codec != in.Codec ||
		got.CodecProfile != in.CodecProfile || got.Width != in.Width ||
		got.Height != in.Height || got.FPSNum != in.FPSNum || got.FPSDen != in.FPSDen ||
		got.StorageKey != in.StorageKey || got.DurationUS != in.DurationUS ||
		got.FrameCount != in.FrameCount || got.SizeBytes != in.SizeBytes {
		t.Fatalf("artifact mapping lost fields: %+v", got)
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
