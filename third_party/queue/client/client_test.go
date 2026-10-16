package client

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeQueue returns an httptest server implementing the subset of the queue
// HTTP API the client speaks.
func fakeQueue(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var job Job
		_ = json.NewDecoder(r.Body).Decode(&job)
		switch job.ID {
		case "dup":
			http.Error(w, "already exists", http.StatusConflict)
		case "boom":
			http.Error(w, "bad input", http.StatusBadRequest)
		case "internal":
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": job.ID})
		}
	})

	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "missing" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		job := Job{
			ID:       id,
			State:    StateCompleted,
			Attempts: 2,
			Artifact: &Artifact{
				ID:           "art-1",
				ArtifactURL:  "https://store/overlay.mp4",
				ArtifactHash: "abc",
				ProfileID:    CertifiedProfileVeloxH2641080p30V1,
				CopyEligible: true,
				ClosedGOP:    true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
	})

	mux.HandleFunc("GET /jobs/depth", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Stats{Pending: 3, Running: 1, Completed: 5, Depth: 4})
	})

	mux.HandleFunc("POST /jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		switch id {
		case "missing":
			http.Error(w, "not found", http.StatusNotFound)
		case "done":
			http.Error(w, "job is in state \"completed\", cannot cancel terminal job", http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	return httptest.NewServer(mux)
}

func TestCancelSuccessAndIdempotency(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Cancel(context.Background(), "job-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// The queue treats a repeated cancel as a no-op (204), which must surface
	// as nil so producers can retry cancels idempotently.
	if err := c.Cancel(context.Background(), "job-1"); err != nil {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestCancelNotFound(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	err := c.Cancel(context.Background(), "missing")
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCancelTerminalConflict(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Cancel(context.Background(), "done"); err == nil {
		t.Fatal("expected conflict on completed job")
	}
}

func TestValidateJobMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		job  Job
		want bool
	}{
		{name: "empty defaults", job: Job{}},
		{name: "store only", job: Job{PublicationPolicy: PublicationObjectStoreOnly}},
		{name: "drive", job: Job{PublicationPolicy: PublicationObjectStoreAndDrive}},
		{name: "unknown policy", job: Job{PublicationPolicy: "publish_everywhere"}, want: true},
		{name: "negative delay", job: Job{DaemonAdmissionWaitMS: -1}, want: true},
		{name: "infinite delay", job: Job{DaemonAdmissionWaitMS: math.Inf(1)}, want: true},
		{name: "nan delay", job: Job{DaemonAdmissionWaitMS: math.NaN()}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateJobMetadata(test.job)
			if (err != nil) != test.want {
				t.Fatalf("ValidateJobMetadata(%+v) err=%v, want error=%t", test.job, err, test.want)
			}
		})
	}
}

func TestSubmitSuccess(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Submit(context.Background(), Job{ID: "job-1", Schema: JobSchemaV1, Version: JobSchemaVersionV1, RenderPlan: []byte(`{"n":1}`), Assets: []AssetRef{}}); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func TestSubmitDuplicate(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	err := c.Submit(context.Background(), Job{ID: "dup", Schema: JobSchemaV1, Version: JobSchemaVersionV1, RenderPlan: []byte(`{"n":1}`), Assets: []AssetRef{}})
	if err == nil || !errors.Is(err, ErrJobExists) {
		t.Fatalf("want ErrJobExists, got %v", err)
	}
}

func TestSubmitError(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Submit(context.Background(), Job{ID: "boom", Schema: JobSchemaV1, Version: JobSchemaVersionV1, RenderPlan: []byte(`{"n":1}`), Assets: []AssetRef{}}); err == nil {
		t.Fatal("expected error on 400")
	}
}

func TestSubmitServerErrorIsNotJobExists(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	err := c.Submit(context.Background(), Job{ID: "internal", Schema: JobSchemaV1, Version: JobSchemaVersionV1, RenderPlan: []byte(`{"n":1}`), Assets: []AssetRef{}})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	// A storage failure must never masquerade as an idempotent duplicate:
	// producers treat ErrJobExists as success and would silently drop the job.
	if errors.Is(err, ErrJobExists) {
		t.Fatalf("500 must not map to ErrJobExists, got %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("want HTTP status in error, got %v", err)
	}
}

func TestGetArtifact(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	job, err := c.Get(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if job.State != StateCompleted || job.Artifact == nil {
		t.Fatalf("unexpected job: %+v", job)
	}
	if !job.Artifact.CopyEligible || !job.Artifact.ClosedGOP || job.Artifact.ProfileID != CertifiedProfileVeloxH2641080p30V1 {
		t.Fatalf("artifact certification not round-tripped: %+v", job.Artifact)
	}
}

func TestGetNotFound(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Get(context.Background(), "missing")
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDepthAndHealth(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	stats, err := c.Depth(context.Background())
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if stats.Pending != 3 || stats.Completed != 5 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestWaitCompletes(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job, err := c.Wait(ctx, "job-1", time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != StateCompleted || job.Artifact == nil {
		t.Fatalf("wait returned non-terminal job: %+v", job)
	}
}

func TestWaitContextCancel(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Wait(ctx, "missing", time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("want context cancellation, got %v", err)
	}
}

// TestValidateJobIDAcceptsAuthoredTextAndRejectsUnsafeComponents pins the
// production failure this rule must not repeat: an overlay job id derived from
// an Italian phrase ("...la comunità local...") was rejected as an unsupported
// character at workspace materialization, failing a render job whose plan had
// already been compiled and paid for. An id is opaque text that becomes ONE
// filename component, so non-ASCII letters are legal while separators, control
// characters, symbols and traversal components stay banned.
func TestValidateJobIDAcceptsAuthoredTextAndRejectsUnsafeComponents(t *testing.T) {
	valid := []string{
		"job-1",
		"run_abc:item:000:masked-upward-reveal__x__phrase__01",
		"isometric-3d-fold__l-evento-scosse-profondamente-la-comunità-local__phrase__01",
		"Ünïcödé-123",
		"日本語-01",
		"e\u0301-1", // decomposed accent: mark admitted with its base letter
	}
	for _, id := range valid {
		if err := ValidateJobID(id); err != nil {
			t.Errorf("ValidateJobID(%q) = %v, want nil", id, err)
		}
	}
	invalid := []string{
		"", ".", "..",
		"a/b", "a\\b", "a b", "a\nb", "a\tb", "a\x00b",
		"a😀b", // symbol, not a letter
		string([]byte{0xff, 0xfe}),
		strings.Repeat("a", 257),
	}
	for _, id := range invalid {
		if err := ValidateJobID(id); err == nil {
			t.Errorf("ValidateJobID(%q) = nil, want a rejection", id)
		}
	}
}
