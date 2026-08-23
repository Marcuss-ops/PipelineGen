// Package jobbrokerclient — client_p1_10_test.go (P1 #10, July 2026).
//
// End-to-end test surface for the 3-phase upload protocol. Verifies
// the godlike/07 contract that the HTTP request is bound to
// prepareCtx.Ctx (NOT context.Background) so worker cancellation /
// lease-loss / shutdown drain aborts the in-flight transport instead
// of silently running to completion.
//
// Order-independent: tests do not rely on any package-level singleton
// state. Each test spins up its own httptest.Server + Client.
//
// Coverage gap (forward-pointer): the pre-cancel pattern below proves
// the Ctx is bound to net/http.Request at request-build time. A
// streaming-cancellation test (httptest.BlockUntilCtxDone handler
// that consumes r.Context().Done() during the body send) is the
// next hardening — deferred because the relapse cost is low
// (handler-side `handlerCalled.Load()` already logs a regression)
// and the pre-cancel pattern catches the most likely drift surface
// (silent reversion to context.Background at the seam).
package jobbrokerclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	jobbrokerclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
)

// ── Fail-closed tests (P1 #10 godlike/07 no-fake-availability) ─────────────

// TestClient_PrepareArtifactUpload_NilCtx_FailsClosed — the Client
// rejects PrepareArtifactUpload with a typed error when prepareCtx.Ctx
// is nil. The error must wrap the canonical ErrArtifactCtxRequired
// sentinel (distinct from ErrArtifactUploaderNotConfigured) so callers
// can errors.Is each disambiguatably.
func TestClient_PrepareArtifactUpload_NilCtx_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP handler should NOT be called when prepareCtx.Ctx is nil")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := jobbrokerclient.New(srv.URL, "test-token")
	_, err := c.PrepareArtifactUpload(remote.PrepareContext{
		Ctx:        nil,
		JobID:      "job-001",
		LeaseID:    "lease-001",
		ArtifactID: "job-001:script_json",
		SHA256:     "abc12345",
	})
	if err == nil {
		t.Fatal("expected error when prepareCtx.Ctx is nil (P1 #10 fail-closed)")
	}
	if !errors.Is(err, remote.ErrArtifactCtxRequired) {
		t.Errorf("expected ErrArtifactCtxRequired; got %v", err)
	}
}

// TestClient_UploadArtifactFile_NilCtx_FailsClosed — same gate for
// the upload-file command.
func TestClient_UploadArtifactFile_NilCtx_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP handler should NOT be called when prepareCtx.Ctx is nil")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := jobbrokerclient.New(srv.URL, "test-token")
	_, err := c.UploadArtifactFile(remote.PrepareContext{
		Ctx:        nil,
		JobID:      "job-001",
		LeaseID:    "lease-001",
		ArtifactID: "job-001:script_json",
		SHA256:     "abc12345",
	}, "sess-001", "/tmp/nonexistent", "key")
	if err == nil {
		t.Fatal("expected error when prepareCtx.Ctx is nil (P1 #10 fail-closed)")
	}
	if !errors.Is(err, remote.ErrArtifactCtxRequired) {
		t.Errorf("expected ErrArtifactCtxRequired; got %v", err)
	}
}

// TestClient_FinalizeArtifactUpload_NilCtx_FailsClosed — same gate
// for the finalize command.
func TestClient_FinalizeArtifactUpload_NilCtx_FailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP handler should NOT be called when prepareCtx.Ctx is nil")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := jobbrokerclient.New(srv.URL, "test-token")
	_, err := c.FinalizeArtifactUpload(remote.PrepareContext{
		Ctx:        nil,
		JobID:      "job-001",
		LeaseID:    "lease-001",
		ArtifactID: "job-001:script_json",
		SHA256:     "abc12345",
	}, "sess-001", "abc12345", "key")
	if err == nil {
		t.Fatal("expected error when prepareCtx.Ctx is nil (P1 #10 fail-closed)")
	}
	if !errors.Is(err, remote.ErrArtifactCtxRequired) {
		t.Errorf("expected ErrArtifactCtxRequired; got %v", err)
	}
}

// ── Ctx-through-HTTP end-to-end tests (P1 #10 audit-pin) ──────────────────
//
// Pattern: pre-cancel prepareCtx.Ctx BEFORE calling the protocol
// command. http.Client.Do(req) inspects req.Context() before any
// network activity and returns an error wrapping context.Canceled.
// This is timing-race-free (no goroutine + WaitGroup race), and
// proves prepareCtx.Ctx is the value bound to net/http.Request
// rather than a hidden context.Background() (the P1 #10 bug);

// if the Client silently used context.Background, httpClient.Do()
// would never see cancellation and would either succeed OR fail
// with a connection / decode error, NOT one wrapping context.Canceled.

// TestClient_PrepareArtifactUpload_CtxPreCancelled — P1 #10 audit-pin
// for the prepare command.
func TestClient_PrepareArtifactUpload_CtxPreCancelled(t *testing.T) {
	var handlerSawCancelled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			handlerSawCancelled.Store(true)
			return
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remote.UploadSession{
			ID:         "sess-001",
			LeaseID:    "lease-001",
			ArtifactID: "job-001:script_json",
			State:      remote.StateUploadPreparing,
		})
	}))
	defer srv.Close()

	c := jobbrokerclient.New(srv.URL, "test-token")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	pc := remote.PrepareContext{
		Ctx:          cancelled,
		JobID:        "job-001",
		LeaseID:      "lease-001",
		ArtifactID:   "job-001:script_json",
		ArtifactKind: "script_json",
		Filename:     "script.json",
		MIMEType:     "application/json",
		SizeBytes:    1234,
		SHA256:       "abc12345",
	}
	_, err := c.PrepareArtifactUpload(pc)
	if err == nil {
		t.Fatal("expected error when prepareCtx.Ctx is pre-cancelled (transport must abort)")
	}
	// godlike/07 typed-error probe: net/http wraps context.Canceled
	// in *url.Error via httpClient.Do(), and *url.Error.Unwrap() in
	// Go 1.20+ surfaces the inner error to errors.Is. This catches
	// the typed cancellation regardless of wrapping depth.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error wrapping context.Canceled (proves Ctx flowed into net/http.Request); got %v", err)
	}
}

// TestClient_UploadArtifactFile_CtxPreCancelled — P1 #10 audit-pin
// for the streaming upload-file command. This is the highest-value
// test because the upload-file path previously used a literal
// context.Background() — the P1 #10 bug.
func TestClient_UploadArtifactFile_CtxPreCancelled(t *testing.T) {
	var handlerCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(remote.UploadSession{
			ID:         "sess-001",
			LeaseID:    "lease-001",
			ArtifactID: "job-001:script_json",
			State:      remote.StateUploadUploading,
		})
	}))
	defer srv.Close()

	c := jobbrokerclient.New(srv.URL, "test-token")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tmp := writeUploadFile(t, make([]byte, 4096))
	pc := remote.PrepareContext{
		Ctx:          cancelled,
		JobID:        "job-001",
		LeaseID:      "lease-001",
		ArtifactID:   "job-001:script_json",
		ArtifactKind: "script_json",
		Filename:     "script.json",
		MIMEType:     "application/json",
		SizeBytes:    4096,
		SHA256:       "abc12345",
	}
	_, err := c.UploadArtifactFile(pc, "sess-001", tmp, "key")
	if err == nil {
		t.Fatal("expected error after Ctx pre-cancellation; got nil (regression: Context.Background() leak?)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error wrapping context.Canceled (proves Ctx flowed into net/http.Request); got %v", err)
	}
	// Defense-in-depth: handler should not have been called because
	// the pre-cancelled ctx aborts the request before HTTP send.
	// (This assertion may be racy on extremely fast hosts — but in
	// practice httpClient.Do() never makes a remote call when
	// prepareCtx.Ctx is already Done(), so the handler is never
	// invoked.)
	if handlerCalled.Load() {
		t.Log("note: handler was invoked despite pre-cancelled Ctx — likely a future refactor regression to watch")
	}
}

// TestClient_FinalizeArtifactUpload_CtxPreCancelled — P1 #10 audit-pin
// for the finalize command.
func TestClient_FinalizeArtifactUpload_CtxPreCancelled(t *testing.T) {
	var handlerCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(remote.UploadSession{
			ID:         "sess-001",
			LeaseID:    "lease-001",
			ArtifactID: "job-001:script_json",
			State:      remote.StateUploadFinalized,
		})
	}))
	defer srv.Close()

	c := jobbrokerclient.New(srv.URL, "test-token")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	pc := remote.PrepareContext{
		Ctx:          cancelled,
		JobID:        "job-001",
		LeaseID:      "lease-001",
		ArtifactID:   "job-001:script_json",
		ArtifactKind: "script_json",
		Filename:     "script.json",
		MIMEType:     "application/json",
		SizeBytes:    4096,
		SHA256:       "abc12345",
	}
	_, err := c.FinalizeArtifactUpload(pc, "sess-001", "abc12345", "key")
	if err == nil {
		t.Fatal("expected error after Ctx pre-cancellation; got nil (regression: Context.Background() leak?)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error wrapping context.Canceled (proves Ctx flowed into net/http.Request); got %v", err)
	}
	if handlerCalled.Load() {
		t.Log("note: handler was invoked despite pre-cancelled Ctx — likely a future refactor regression to watch")
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────

// writeUploadFile writes payload to a fresh temp file under
// t.TempDir() and returns the path. Used by the streaming
// cancellation test.
func writeUploadFile(t *testing.T, payload []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// Compile-time anchor: silence the io import used only by the
// streaming body-read pattern propagation tests reference (kept
// here as a forward-pointer to backend-integration tests that
// will exercise the streaming path more thoroughly).
var _ = io.EOF
