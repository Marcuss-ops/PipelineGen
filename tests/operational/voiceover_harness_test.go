// Package operational — voiceover_harness_test.go
//
// 7 TDD contract tests for voiceover_harness.go. All tests are pure-stdlib
// (no internal/* imports). Curl tests use httptest.Server so they run
// hermetically without a live PipelineGen instance.
//
// Skip rules: tests that require VELOX_ADMIN_TOKEN set skip when unset
// (mirrors the existing per-FASE wrapper convention). The token-rotation
// test uses a temp file so it runs in any environment.
package operational

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── 1. Skip when no token source is configured ────────────────────────

func TestNewVoiceoverHarness_NoToken_Skips(t *testing.T) {
	// Snapshot env, force both to empty, defer restore.
	origToken, hadToken := os.LookupEnv("VELOX_ADMIN_TOKEN")
	origFile, hadFile := os.LookupEnv("TOKEN_FILE")
	t.Cleanup(func() {
		if hadToken {
			_ = os.Setenv("VELOX_ADMIN_TOKEN", origToken)
		} else {
			_ = os.Unsetenv("VELOX_ADMIN_TOKEN")
		}
		if hadFile {
			_ = os.Setenv("TOKEN_FILE", origFile)
		} else {
			_ = os.Unsetenv("TOKEN_FILE")
		}
	})
	_ = os.Unsetenv("VELOX_ADMIN_TOKEN")
	_ = os.Unsetenv("TOKEN_FILE")

	// t.Skipf does NOT panic — it marks the test as skipped and halts
	// execution. So if the harness returns from this call without
	// skipping, the contract is broken. The sub-test scope contains
	// the "must not reach" assertion.
	t.Run("subtest", func(st *testing.T) {
		h, err := NewVoiceoverHarness(st, HarnessOptions{FASE: "TEST-SKIP"})
		if err != nil {
			st.Fatalf("expected t.Skipf, not a typed error; got err=%v h=%+v", err, h)
		}
		if h != nil {
			st.Fatalf("expected t.Skipf to halt the sub-test; NewVoiceoverHarness returned %+v", h)
		}
	})
}

// ── 2. Token resolved from explicit option ────────────────────────────

func TestNewVoiceoverHarness_ExplicitToken(t *testing.T) {
	// Force a clean env to prove the explicit option wins.
	_ = os.Unsetenv("VELOX_ADMIN_TOKEN")
	_ = os.Unsetenv("TOKEN_FILE")
	t.Setenv("VELOX_ADMIN_TOKEN", "")
	t.Setenv("TOKEN_FILE", "")

	h, err := NewVoiceoverHarness(t, HarnessOptions{
		FASE:  "TEST-EXPLICIT",
		Token: "explicit-token-abc123",
	})
	if err != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", err)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}
	if h.token != "explicit-token-abc123" {
		t.Fatalf("expected token=explicit-token-abc123, got %q", h.token)
	}
	if h.report.FASE != "TEST-EXPLICIT" {
		t.Fatalf("expected FASE=TEST-EXPLICIT, got %q", h.report.FASE)
	}
}

// ── 3. Token resolved from TOKEN_FILE ─────────────────────────────────

func TestNewVoiceoverHarness_TokenFromFile(t *testing.T) {
	// Force env var empty so we exercise the file path.
	t.Setenv("VELOX_ADMIN_TOKEN", "")

	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "pipelinegen.env")
	if err := os.WriteFile(tokenFile, []byte(
		"# PipelineGen env file (test fixture)\n"+
			"VELOX_PORT=8080\n"+
			"VELOX_ADMIN_TOKEN=file-token-xyz789\n"+
			"OTHER_VAR=ignored\n",
	), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("TOKEN_FILE", tokenFile)

	h, err := NewVoiceoverHarness(t, HarnessOptions{FASE: "TEST-FILE"})
	if err != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", err)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}
	if h.token != "file-token-xyz789" {
		t.Fatalf("expected token=file-token-xyz789, got %q", h.token)
	}
	if h.tokenFile != tokenFile {
		t.Fatalf("expected tokenFile=%q, got %q", tokenFile, h.tokenFile)
	}
}

// ── 4. Curl: 200 returns body ─────────────────────────────────────────

func TestCurl_200ReturnsBody(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "test-token-200")

	var sawAuth atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer test-token-200" {
			sawAuth.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok","id":"abc-123"}`))
	}))
	defer srv.Close()

	h, herr := NewVoiceoverHarness(t, HarnessOptions{
		FASE:    "TEST-CURL-200",
		APIBase: srv.URL,
	})
	if herr != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", herr)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}

	code, body, err := h.Curl(context.Background(), "GET", "/api/test", nil)
	if err != nil {
		t.Fatalf("Curl returned err: %v", err)
	}
	if code != 200 {
		t.Fatalf("expected code=200, got %d (body=%s)", code, string(body))
	}
	if !sawAuth.Load() {
		t.Fatal("server did not receive the expected Authorization header")
	}
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("body missing expected payload: %s", string(body))
	}
}

// ── 5. Curl: 401 triggers token reload from TOKEN_FILE ────────────────

func TestCurl_401TriggersTokenReload(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "") // env-var path disabled → file path active

	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "rotate.env")
	if err := os.WriteFile(tokenFile, []byte("VELOX_ADMIN_TOKEN=stale-token-v1\n"), 0o600); err != nil {
		t.Fatalf("write initial token file: %v", err)
	}
	t.Setenv("TOKEN_FILE", tokenFile)

	var requestCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		auth := r.Header.Get("Authorization")
		if n == 1 {
			// First call: stale token → 401
			if auth != "Bearer stale-token-v1" {
				t.Errorf("first call: expected stale token, got %q", auth)
			}
			w.WriteHeader(401)
			return
		}
		// Second+ call: rotated token → 200
		if auth != "Bearer fresh-token-v2" {
			t.Errorf("retry #%d: expected rotated token, got %q", n, auth)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	h, herr := NewVoiceoverHarness(t, HarnessOptions{
		FASE:        "TEST-ROTATE",
		APIBase:     srv.URL,
		MaxRetries:  3,
		HTTPTimeout: 2 * time.Second,
	})
	if herr != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", herr)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}

	// Simulate operator rotation: rewrite the file mid-test.
	// The Curl below will see 401 on attempt #1, then reload + retry
	// with the new value, succeeding on attempt #2.
	if err := os.WriteFile(tokenFile, []byte("VELOX_ADMIN_TOKEN=fresh-token-v2\n"), 0o600); err != nil {
		t.Fatalf("rewrite token file: %v", err)
	}

	code, body, err := h.Curl(context.Background(), "GET", "/api/test", nil)
	if err != nil {
		t.Fatalf("Curl returned err: %v", err)
	}
	if code != 200 {
		t.Fatalf("expected code=200 after token reload, got %d (body=%s, requests=%d)",
			code, string(body), requestCount.Load())
	}
	if requestCount.Load() != 2 {
		t.Fatalf("expected exactly 2 server requests (1 stale + 1 fresh), got %d", requestCount.Load())
	}
	if h.token != "fresh-token-v2" {
		t.Fatalf("expected in-memory token=fresh-token-v2 after reload, got %q", h.token)
	}
}

// ── 6. Assert: pass does not fail; fail calls t.Fatal ─────────────────

func TestAssert_PassAndFail(t *testing.T) {
	// The harness Assert method calls t.Fatal on mismatch, which calls
	// runtime.Goexit and terminates the test process. To verify this
	// behavior without letting it kill the parent test, we use the
	// canonical subprocess pattern: re-exec the same test binary with
	// BE_CRASH=1, which triggers the fatal path in the child. The
	// parent verifies the child exited with non-zero status.
	if os.Getenv("BE_CRASH") == "1" {
		t.Setenv("VELOX_ADMIN_TOKEN", "test-assert")
		h, err := NewVoiceoverHarness(t, HarnessOptions{FASE: "TEST-ASSERT"})
		if err != nil {
			t.Fatalf("NewVoiceoverHarness returned err: %v", err)
		}
		if h == nil {
			t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
		}
		h.Assert("happy_eq", "42", "42")
		h.Assert("mismatch", "expected", "actual")
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestAssert_PassAndFail$", "-test.v")
	cmd.Env = append(os.Environ(), "BE_CRASH=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected harness.Assert mismatch to fail the child process, but it passed (output: %s)", string(out))
	}
	// Verify the child output contains the expected mismatch message.
	if !strings.Contains(string(out), "mismatch") {
		t.Fatalf("expected child output to contain 'mismatch', got: %s", string(out))
	}
}

// ── 7. WriteReport: JSON shape + auto-Outcome ──────────────────────────

func TestWriteReport_JSONShape(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "test-report")
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report.json")

	h, err := NewVoiceoverHarness(t, HarnessOptions{
		FASE:       "TEST-REPORT",
		ReportPath: reportPath,
	})
	if err != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", err)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}
	h.RecordJobID("parent", "parent-123")
	h.RecordJobID("child_it_it", "child-456")
	h.Assert("status_eq", "200", "200")
	h.Assert("body_has_id", "true", "true")

	if err := h.WriteReport(); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	// Re-read the JSON and validate the shape.
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report file: %v", err)
	}
	var got VoiceoverReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal report: %v (raw: %s)", err, string(data))
	}
	if got.FASE != "TEST-REPORT" {
		t.Fatalf("FASE: expected TEST-REPORT, got %q", got.FASE)
	}
	if got.Outcome != "pass" {
		t.Fatalf("Outcome: expected pass, got %q", got.Outcome)
	}
	if got.JobIDs["parent"] != "parent-123" {
		t.Fatalf("JobIDs[parent]: expected parent-123, got %q", got.JobIDs["parent"])
	}
	if got.JobIDs["child_it_it"] != "child-456" {
		t.Fatalf("JobIDs[child_it_it]: expected child-456, got %q", got.JobIDs["child_it_it"])
	}
	if len(got.Assertions) != 2 {
		t.Fatalf("Assertions: expected 2, got %d", len(got.Assertions))
	}
	if got.StartedAt == "" || got.FinishedAt == "" {
		t.Fatalf("timestamps missing: started=%q finished=%q", got.StartedAt, got.FinishedAt)
	}
	// File mode must be 0600 (token-leak prevention: even though the report
	// doesn't carry the token, future migrations might add it; the mode
	// is set proactively).
	st, err := os.Stat(reportPath)
	if err != nil {
		t.Fatalf("stat report: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("report file mode: expected 0600, got %04o", perm)
	}
}

// ── 6. Curl: connection-refused surfaces a transport error ──────────

func TestCurl_ConnectionRefused_ReturnsError(t *testing.T) {
	// Skipped under -short because verifying the Curl transport-error
	// path requires racing the OS to bind an unused port (TOCTOU). The
	// underlying behaviour (http.Client.Do returning a wrapped error
	// for transport failures) is a Go-stdlib guarantee, not a harness
	// contract — the value of this test is documentation, not regression
	// detection. Operators running the full test suite (`go test ./...`
	// without -short) get coverage.
	if testing.Short() {
		t.Skip("Curl connection-refused test requires live port binding; skipped under -short")
	}
	t.Setenv("VELOX_ADMIN_TOKEN", "test-token-connref")

	// Bind + immediately close to grab a port nothing is listening on.
	// TOCTOU race: another process could rebind the port between
	// srv.Close() and h.Curl(). Documented as a known limitation; the
	// test value is "the harness surfaces a transport error", not
	// "the test reliably hits a refused port".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close()

	h, herr := NewVoiceoverHarness(t, HarnessOptions{
		FASE:        "TEST-CONNREF",
		APIBase:     addr,
		HTTPTimeout: 500 * time.Millisecond,
	})
	if herr != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", herr)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}

	code, _, err := h.Curl(context.Background(), "GET", "/api/test", nil)
	if err == nil {
		t.Fatalf("expected transport error on connection refused; got code=%d err=nil", code)
	}
	if !strings.Contains(err.Error(), "voiceover harness: Curl") {
		t.Fatalf("expected wrapped error to mention the harness layer, got: %v", err)
	}
}

// ── 7. Curl: context cancellation aborts the request ─────────────────

func TestCurl_ContextCancellation_ReturnsError(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "test-token-ctx")

	// Server that blocks until the client disconnects; the harness's
	// context cancellation should unblock the call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	h, herr := NewVoiceoverHarness(t, HarnessOptions{
		FASE:        "TEST-CTX",
		APIBase:     srv.URL,
		HTTPTimeout: 5 * time.Second, // longer than the ctx cancel
	})
	if herr != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", herr)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the request aborts before write

	_, _, err := h.Curl(ctx, "GET", "/api/test", nil)
	if err == nil {
		t.Fatal("expected context-cancellation error; got nil")
	}
}

// ── 8. Probe*: ErrSqliteBinaryMissing is the typed-sentinel signal ───

func TestProbeJobs_ReturnsErrSqliteBinaryMissing_WhenBinaryAbsent(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "test-probe-skip")

	h, herr := NewVoiceoverHarness(t, HarnessOptions{
		FASE:    "TEST-PROBE-SENTINEL",
		APIBase: "http://127.0.0.1:1", // dummy; never reached
		DBPath:  "/nonexistent/path/media.db.sqlite",
	})
	if herr != nil {
		t.Fatalf("NewVoiceoverHarness returned err: %v", herr)
	}
	if h == nil {
		t.Fatal("NewVoiceoverHarness returned nil harness (skipped unexpectedly)")
	}
	// Force the binary-resolved path to "" to simulate "not in PATH".
	// The harness resolves at construct time; for this test we override
	// post-construct because looking up PATH mid-test is unreliable.
	h.sqliteBin = ""

	rows, err := h.ProbeJobs("any-id")
	if !errors.Is(err, ErrSqliteBinaryMissing) {
		t.Fatalf("expected ErrSqliteBinaryMissing, got err=%v rows=%v", err, rows)
	}
	if rows != nil {
		t.Fatalf("expected rows=nil on sentinel, got %v", rows)
	}

	// Same contract for the other 3 probes.
	if _, err := h.ProbeVoiceovers("any-id"); !errors.Is(err, ErrSqliteBinaryMissing) {
		t.Fatalf("ProbeVoiceovers: expected ErrSqliteBinaryMissing, got %v", err)
	}
	if _, err := h.ProbeMediaAssets("any-id"); !errors.Is(err, ErrSqliteBinaryMissing) {
		t.Fatalf("ProbeMediaAssets: expected ErrSqliteBinaryMissing, got %v", err)
	}
	if _, err := h.ProbeOutboxEvents("any-id"); !errors.Is(err, ErrSqliteBinaryMissing) {
		t.Fatalf("ProbeOutboxEvents: expected ErrSqliteBinaryMissing, got %v", err)
	}
}

// ── 9. NewVoiceoverHarness: negative MaxRetries returns typed error ──

// Note: a prior version of this test wrapped the harness in t.Run
// and called st.Fatal on a return — the structure made the parent
// test fail in BOTH the correct and incorrect contract states, so
// it was always red in CI. The current contract returns a typed
// error instead of calling t.Fatalf, which lets the test assert the
// error directly without the sub-test cascade issue.

func TestNewVoiceoverHarness_NegativeMaxRetries_ReturnsError(t *testing.T) {
	t.Setenv("VELOX_ADMIN_TOKEN", "test-negative-retry")

	h, err := NewVoiceoverHarness(t, HarnessOptions{
		FASE:       "TEST-NEG-RETRY",
		MaxRetries: -1,
	})
	if err == nil {
		t.Fatalf("expected typed error on negative MaxRetries, got h=%+v err=nil", h)
	}
	if h != nil {
		t.Fatalf("expected h=nil on contract violation, got %+v", h)
	}
	if !strings.Contains(err.Error(), "MaxRetries must be >= 0") {
		t.Fatalf("expected error to mention MaxRetries bound, got: %v", err)
	}
}
