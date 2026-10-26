package remotejob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// staleMasterStub answers PREPARE with the given prepare body and FINALIZE
// with the given status/body, which is all the stale-identity classification
// needs.
func staleMasterStub(t *testing.T, prepareBody string, finalizeStatus int, finalizeBody string) (*httptest.Server, *int) {
	t.Helper()
	finalizes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/pre":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(prepareBody))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/finalize"):
			finalizes++
			w.WriteHeader(finalizeStatus)
			_, _ = w.Write([]byte(finalizeBody))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return server, &finalizes
}

// TestPrepareJobRefusesAnAlreadyCancelledPreparation: an idempotent PREPARE
// replay can hand back an identity the Master has already canceled. A canceled
// preparation must never take a FINALIZE, so the client classifies it as a
// stale identity carrying the job id the caller must archive.
func TestPrepareJobRefusesAnAlreadyCancelledPreparation(t *testing.T) {
	server, finalizes := staleMasterStub(t, `{"job_id":"master-cancelled","status":"CANCELED"}`, http.StatusAccepted, ``)
	defer server.Close()
	client := New(server.URL, "test-token")

	result, err := client.PrepareJob(context.Background(), map[string]any{"job_type": "scene.composite.v1"})
	if err == nil {
		t.Fatal("a canceled preparation must not report success")
	}
	if !errors.Is(err, ErrRemoteStaleJob) {
		t.Fatalf("PrepareJob error = %v, want ErrRemoteStaleJob", err)
	}
	var stale *StaleJobError
	if !errors.As(err, &stale) || stale.JobID != "master-cancelled" || stale.Status != "CANCELED" {
		t.Fatalf("stale error = %#v, want the canceled identity it names", stale)
	}
	if result.JobID != "master-cancelled" {
		t.Fatalf("result job id = %q, want the archived identity", result.JobID)
	}
	if *finalizes != 0 {
		t.Fatalf("FINALIZE calls = %d, want 0 — a canceled preparation is never finalized", *finalizes)
	}
}

// TestPrepareKeepsTheIDOnlyProjection: the id-only Prepare callers keep their
// contract and surface the same typed refusal.
func TestPrepareKeepsTheIDOnlyProjection(t *testing.T) {
	server, _ := staleMasterStub(t, `{"job_id":"master-cancelled","status":"CANCELLED"}`, http.StatusAccepted, ``)
	defer server.Close()
	client := New(server.URL, "test-token")

	jobID, err := client.Prepare(context.Background(), map[string]any{"job_type": "scene.composite.v1"})
	if !errors.Is(err, ErrRemoteStaleJob) {
		t.Fatalf("Prepare error = %v, want ErrRemoteStaleJob", err)
	}
	if jobID != "master-cancelled" {
		t.Fatalf("Prepare job id = %q, want the stale identity", jobID)
	}
}

// TestPrepareJobHandsBackAnInFlightIdentity: an idempotent replay that returns
// a LEASED, still-running job is not stale — it is a render that already
// exists. No error means the caller may decide to resume it; only terminal
// statuses are refused.
func TestPrepareJobHandsBackAnInFlightIdentity(t *testing.T) {
	server, finalizes := staleMasterStub(t, `{"job_id":"master-running","status":"RUNNING"}`, http.StatusAccepted, ``)
	defer server.Close()
	client := New(server.URL, "test-token")

	result, err := client.PrepareJob(context.Background(), map[string]any{"job_type": "scene.composite.v1"})
	if err != nil {
		t.Fatalf("PrepareJob: %v", err)
	}
	if result.JobID != "master-running" || result.Status != "RUNNING" {
		t.Fatalf("result = %+v, want the in-flight identity", result)
	}
	if *finalizes != 0 {
		t.Fatalf("FINALIZE calls = %d, want 0", *finalizes)
	}
}

// TestFinalizeClassifiesALeaseConflictAsStale: the observed Garlasco failure
// mode (`job_already_leased` during FINALIZE) is a stale identity, not a
// transient fault. Replaying FINALIZE on that id can never succeed, so the
// error is typed and the caller replaces the identity instead of retrying.
func TestFinalizeClassifiesALeaseConflictAsStale(t *testing.T) {
	server, finalizes := staleMasterStub(t, `{"job_id":"master-job-1","status":"PREPARING"}`, http.StatusConflict, `job_already_leased`)
	defer server.Close()
	client := New(server.URL, "test-token")

	if _, err := client.PrepareJob(context.Background(), map[string]any{"job_type": "scene.composite.v1"}); err != nil {
		t.Fatalf("PrepareJob: %v", err)
	}
	err := client.Finalize(context.Background(), "master-job-1", map[string]any{"runtime_assets": []any{}})
	if err == nil {
		t.Fatal("a lease-conflict FINALIZE must not report success")
	}
	if !errors.Is(err, ErrRemoteStaleJob) {
		t.Fatalf("Finalize error = %v, want ErrRemoteStaleJob", err)
	}
	var stale *StaleJobError
	if !errors.As(err, &stale) || stale.JobID != "master-job-1" {
		t.Fatalf("stale error = %#v, want the refused identity", stale)
	}
	if !strings.Contains(err.Error(), "job_already_leased") {
		t.Fatalf("error = %v, want the Master's refusal preserved in the cause", err)
	}
	if *finalizes != 1 {
		t.Fatalf("FINALIZE calls = %d, want 1", *finalizes)
	}
}

// TestFinalizeDoesNotClassifyAnOrdinaryFailureAsStale: a transport or server
// fault keeps the ordinary error contract — the caller may retry the SAME
// identity, which is exactly what the stale classification must not forbid.
func TestFinalizeDoesNotClassifyAnOrdinaryFailureAsStale(t *testing.T) {
	server, _ := staleMasterStub(t, `{"job_id":"master-job-1","status":"PREPARING"}`, http.StatusInternalServerError, `disk on fire`)
	defer server.Close()
	client := New(server.URL, "test-token")

	if _, err := client.PrepareJob(context.Background(), map[string]any{"job_type": "scene.composite.v1"}); err != nil {
		t.Fatalf("PrepareJob: %v", err)
	}
	err := client.Finalize(context.Background(), "master-job-1", map[string]any{"runtime_assets": []any{}})
	if err == nil {
		t.Fatal("a failed FINALIZE must not report success")
	}
	if errors.Is(err, ErrRemoteStaleJob) {
		t.Fatalf("Finalize error = %v, must not classify an ordinary failure as stale", err)
	}
}

// TestStaleResponsePhrasesAreMatchedWhole guards the classifier against false
// positives on unrelated messages (for example "released" contains the letters
// of "leased" but describes a healthy resource).
func TestStaleResponsePhrasesAreMatchedWhole(t *testing.T) {
	if staleJobResponse("upload released after release dance") {
		t.Fatal("a released-resource message must not classify as a stale lease")
	}
	if !staleJobResponse("HTTP 409: job_already_leased") {
		t.Fatal("job_already_leased must classify as stale")
	}
	if !staleJobResponse("the job was already cancelled") {
		t.Fatal("already cancelled must classify as stale")
	}
}
