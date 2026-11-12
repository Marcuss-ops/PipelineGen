package remotejob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// runningMasterStub answers every GET with the same job document, which is all
// the poll classification needs.
func runningMasterStub(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/jobs/") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
}

// TestPollRunningWithPermanentPreparationFailure is the fourth mandatory
// regression: a Master that reports RUNNING while carrying an irreversible
// preparation failure must not be polled forever.
//
// The classification is deliberately narrow, so the same test also pins the
// opposite direction: a healthy RUNNING job and a recoverable error keep
// returning ErrRemotePending.
func TestPollRunningWithPermanentPreparationFailure(t *testing.T) {
	const permanent = `{
		"job_id":"job-permanent",
		"status":"RUNNING",
		"error_code":"WINDOW_EXTRADATA_IDENTITY_MISMATCH",
		"error":"window preparation rejected: WINDOW_EXTRADATA_IDENTITY_MISMATCH"
	}`
	server := runningMasterStub(t, permanent)
	defer server.Close()
	client := New(server.URL, "test-token")

	result, err := client.Poll(context.Background(), "job-permanent")
	if err == nil {
		t.Fatal("a permanent preparation failure must not report success")
	}
	if errors.Is(err, ErrRemotePending) {
		t.Fatalf("Poll error = %v, must not keep the wait pending", err)
	}
	if !errors.Is(err, ErrRemoteInvalidPlan) {
		t.Fatalf("Poll error = %v, want ErrRemoteInvalidPlan so the run fails on the plan", err)
	}
	if errors.Is(err, ErrRemoteStaleJob) {
		t.Fatalf("Poll error = %v must not be a stale identity: rotating it resubmits the same plan", err)
	}
	var invalid *InvalidPlanError
	if !errors.As(err, &invalid) || invalid.JobID != "job-permanent" || invalid.Code != "WINDOW_EXTRADATA_IDENTITY_MISMATCH" {
		t.Fatalf("invalid-plan error = %#v, want the rejected identity and its code", invalid)
	}
	if result.JobID != "job-permanent" {
		t.Fatalf("result job id = %q, want the classified identity preserved", result.JobID)
	}

	// Attach must return immediately instead of burning the whole budget.
	started := time.Now()
	if _, attachErr := client.Attach(context.Background(), "job-permanent", 30*time.Second); !errors.Is(attachErr, ErrRemoteInvalidPlan) {
		t.Fatalf("Attach error = %v, want the terminal classification", attachErr)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Attach waited %s on a permanently failed job, want an immediate return", elapsed)
	}
}

// TestPollRunningKeepsTransientAndHealthyJobsPending is the inverse contract:
// an unrecognised or explicitly transient failure stays observable, and a
// render that is merely progressing is never terminal.
func TestPollRunningKeepsTransientAndHealthyJobsPending(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "healthy running job",
			body: `{"job_id":"job-ok","status":"RUNNING","dispatch_status":"running"}`,
		},
		{
			name: "transient job error",
			body: `{"job_id":"job-transient","status":"RUNNING","runtime_assets_pending":true,
				"error":"worker timed out while fetching runtime assets"}`,
		},
		{
			name: "unrecognised error stays observable",
			body: `{"job_id":"job-unknown","status":"RUNNING","runtime_assets_pending":true,
				"error":"something went wrong on the worker"}`,
		},
		{
			// The allowlist is explicit: a code this build has never seen is not
			// proof of a permanent defect.
			name: "unrecognised structured code stays observable",
			body: `{"job_id":"job-code","status":"RUNNING","runtime_assets_pending":true,
				"error_code":"SOME_NEW_CODE","error":"preparation failed: SOME_NEW_CODE"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := runningMasterStub(t, testCase.body)
			defer server.Close()
			client := New(server.URL, "test-token")
			_, err := client.Poll(context.Background(), "job-running")
			if !errors.Is(err, ErrRemotePending) {
				t.Fatalf("Poll error = %v, want ErrRemotePending", err)
			}
			if errors.Is(err, ErrRemoteStaleJob) {
				t.Fatalf("Poll error = %v, must not classify as stale", err)
			}
		})
	}
}
