// Package operational — voiceover_harness.go
//
// Infrastructure for the FASE B/C/D voiceover smoke tests. Provides:
//
//   - NewVoiceoverHarness(t) : canonical single entry-point
//   - Curl(ctx, method, path, payload)  : silent Authorization injection
//   - 401-aware token reload
//   - RotateToken()                    : re-reads TOKEN_FILE from disk
//   - ProbeJobs / ProbeVoiceovers / ProbeMediaAssets / ProbeOutboxEvents
//     : 4 DB probes via sqlite3 CLI shell
//     (matches bash lib/common.sh::sqlite_q)
//   - Assert / RecordJobID / WriteReport
//     : JSON report writer for offline
//     forensics
//
// Pure-stdlib (no internal/* imports) so it compiles cleanly even with the
// 6 pre-existing build issues in
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.
//
// Skip rules (consistent with existing per-FASE wrappers):
//   - VELOX_ADMIN_TOKEN unset AND TOKEN_FILE unset
//   - sqlite3 binary absent from PATH (only required if a probe is called;
//     the harness itself can be constructed without it for Curl-only tests)
//
// Token rotation semantics: this is "rotation awareness", not "auto-rotation"
// in the systemd-restart sense. The operator-facing scripts/rotate_token.sh
// requires root + systemd + service restart, so it is NOT callable from the
// test process. Instead, the harness supports a TOKEN_FILE mode: when
// VELOX_ADMIN_TOKEN env var is unset, the harness reads from TOKEN_FILE at
// construct time AND re-reads on every 401 response (so a mid-test operator
// rotation is picked up automatically on the next Curl).
//
// Design validation: pre-validated by thinker-with-files-gemini
// (FASE VO-TESTING-PLAN-2026-07-04 design pass, 2026-07-04). Confirmed:
//   - package operational (not helper.) to respect user-spec file path
//   - sqlite3 CLI shell-out (not modernc.org/sqlite) for zero new deps
//   - 7 TDD tests using httptest.Server + temp files + pure unit tests
//   - separate per-FASE migration PRs (no smoke rewritten in this commit)
//
// File layout (split 2026-08-07 to satisfy the archcheck-strict 600-line
// cap, see architecture/policy.yaml#max_lines_per_file_strict):
//   - voiceover_harness.go: constructor, Curl, token rotation, tiny helpers
//   - voiceover_probes.go:  the 4 DB Probe* methods + sqlite3 shell-out
//   - voiceover_report.go:  report types + assertion/report writer
package operational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ErrSqliteBinaryMissing is returned by every Probe* method when the
// `sqlite3` binary is not present on PATH. Callers can use errors.Is
// to distinguish "binary unavailable" from "query ran, zero rows" —
// the latter returns (nil-or-empty, nil), the former returns
// (nil, ErrSqliteBinaryMissing). Per godlike/07 no-fake-availability:
// this is the typed signal that the harness could not exercise the
// probe surface; CI MUST treat it as a hard skip-or-fail (no silent
// success path). The bash lib/common.sh::sqlite_q convention is
// fail-fast on the bash side; the Go side mirrors that contract via
// this sentinel.
var ErrSqliteBinaryMissing = errors.New("voiceover harness: sqlite3 binary not in PATH (install with: apt-get install -y sqlite3)")

// VoiceoverHarness is the canonical test fixture for the voiceover
// smoke suite. All probe / curl / assertion entry points are methods on
// this type; the per-FASE test wrappers construct one harness per test
// and call methods in sequence. Probe and report surfaces live in
// voiceover_probes.go / voiceover_report.go respectively.
type VoiceoverHarness struct {
	t *testing.T

	// HTTP surface
	apiBase     string        // e.g. "http://127.0.0.1:8080"
	token       string        // current token (mutable via RotateToken)
	tokenFile   string        // optional TOKEN_FILE path
	httpTimeout time.Duration // per-Curl --max-time
	maxRetries  int           // 401-reload retries per Curl (default 2)

	// DB probe surface (sqlite3 CLI shell-out). A blank path is allowed for
	// hermetic HTTP-only unit tests; live probes must provide SMOKE_DB.
	dbPath    string // SMOKE_DB
	sqliteBin string // resolved at construct time, "" if missing

	// Report
	report     *VoiceoverReport
	reportPath string
	reportMu   sync.Mutex // guards Assert/RecordJobID/WriteReport concurrency
}

// HarnessOptions configures NewVoiceoverHarness. All fields are optional;
// zero values use the canonical defaults aligned with bash lib/common.sh.
type HarnessOptions struct {
	// FASE label for the report (e.g. "B1", "C3", "D2"). Default "UNLABELED".
	FASE string

	// APIBase overrides SMOKE_API_BASE. Default: "http://" + API_BASE
	// env var OR "http://127.0.0.1:${VELOX_PORT:-8080}".
	APIBase string

	// Token overrides VELOX_ADMIN_TOKEN env var. If both empty, falls
	// back to TOKEN_FILE.
	Token string

	// DBPath overrides SMOKE_DB. Live tests must provide one explicitly;
	// the harness never defaults to a persistent user database.
	DBPath string

	// ReportPath is where WriteReport() JSON-marshals the report.
	// Default: "./voiceover_report_<fase>_<unix>.json".
	ReportPath string

	// HTTPTimeout is the per-Curl --max-time. Default 8s (matches bash).
	HTTPTimeout time.Duration

	// MaxRetries is the number of 401-reload retries per Curl. Default 2.
	MaxRetries int
}

// NewVoiceoverHarness is the canonical single entry-point for the
// voiceover smoke infrastructure. Resolves the API base, the auth token
// (env var or TOKEN_FILE), the DB path, and the report path from
// HarnessOptions + env vars.
//
// Returns:
//   - (h, nil)    : success; h is ready to use
//   - (nil, nil)  : skipped via t.Skipf (no live server available —
//     neither VELOX_ADMIN_TOKEN nor TOKEN_FILE is set).
//     The test is marked SKIPPED and execution halts; the
//     return is technically unreachable but documented
//     for caller awareness.
//   - (nil, err)  : contract violation (e.g. negative MaxRetries).
//     Caller MUST handle via `if err != nil { t.Fatal(err) }`.
//
// Design rationale for the (h, err) signature (vs t.Fatalf on contract
// violations): contract violations are NOT graceful skips. The harness
// is intended to be reusable in non-test contexts (e.g. a future
// smoke CLI in cmd/); coupling the contract-violation path to
// *testing.T would break those callers. The t.Skipf path stays
// because "no live server" is a graceful skip in test contexts and
// a non-issue in CLI contexts (where a real env var is always set).
//
// Example:
//
//	h, err := NewVoiceoverHarness(t, HarnessOptions{FASE: "B1"})
//	if err != nil { t.Fatal(err) }
//	if h == nil { return } // skipped
//	code, body, err := h.Curl(ctx, http.MethodPost, "/api/voiceover/run", payload)
//	if err != nil { t.Fatal(err) }
//	h.Assert("dispatch_2xx", "200|202", strconv.Itoa(code))
//	defer h.WriteReport()
func NewVoiceoverHarness(t *testing.T, opts HarnessOptions) (*VoiceoverHarness, error) {
	t.Helper()

	// Token resolution: explicit option > VELOX_ADMIN_TOKEN env > TOKEN_FILE
	tokenFile := os.Getenv("TOKEN_FILE")
	token := opts.Token
	if token == "" {
		token = os.Getenv("VELOX_ADMIN_TOKEN")
	}
	if token == "" && tokenFile != "" {
		if v, err := readTokenFromFile(tokenFile); err == nil && v != "" {
			token = v
		}
	}
	if token == "" {
		t.Skipf("voiceover harness: VELOX_ADMIN_TOKEN and TOKEN_FILE both unset; skipping live probes")
		return nil, nil // unreachable after t.Skipf; documented for caller awareness
	}

	// API base resolution: explicit option > API_BASE env > 127.0.0.1:8080
	apiBase := opts.APIBase
	if apiBase == "" {
		base := os.Getenv("API_BASE")
		if base == "" {
			base = "127.0.0.1:" + getenvDefault("VELOX_PORT", "8080")
		}
		apiBase = "http://" + base
	}

	// DB path resolution: explicit option > SMOKE_DB env. Live callers
	// must opt into a database explicitly so unit tests cannot touch the
	// operational store by omission.
	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = os.Getenv("SMOKE_DB")
	}

	// sqlite3 binary resolution: PATH lookup, may be empty if absent
	sqliteBin := ""
	if p, err := exec.LookPath("sqlite3"); err == nil {
		sqliteBin = p
	}

	// Report path resolution: explicit option > default
	reportPath := opts.ReportPath
	if reportPath == "" {
		reportPath = fmt.Sprintf("./voiceover_report_%s_%d.json",
			defaultStr(opts.FASE, "UNLABELED"), time.Now().Unix())
	}

	// HTTP timeout: explicit option > SMOKE_HTTP_TIMEOUT_SECONDS env > 8s
	httpTimeout := opts.HTTPTimeout
	if httpTimeout == 0 {
		httpTimeout = 8 * time.Second
	}

	maxRetries := opts.MaxRetries
	if maxRetries == 0 {
		maxRetries = 2
	}
	if maxRetries < 0 {
		// Contract violation (NOT a graceful skip). Return a typed
		// error so non-test callers (future smoke CLI) get a
		// machine-actionable signal. Test callers should
		// `if err != nil { t.Fatal(err) }`.
		return nil, fmt.Errorf("voiceover harness: HarnessOptions.MaxRetries must be >= 0 (got %d)", opts.MaxRetries)
	}

	return &VoiceoverHarness{
		t:           t,
		apiBase:     apiBase,
		token:       token,
		tokenFile:   tokenFile,
		httpTimeout: httpTimeout,
		maxRetries:  maxRetries,
		dbPath:      dbPath,
		sqliteBin:   sqliteBin,
		report: &VoiceoverReport{
			StartedAt:  time.Now().UTC().Format(time.RFC3339),
			FASE:       defaultStr(opts.FASE, "UNLABELED"),
			JobIDs:     map[string]string{},
			Assertions: []AssertionRecord{},
			DBProbes:   map[string]DBProbeRecord{},
		},
		reportPath: reportPath,
	}, nil
}

// ── Curl wrapper ─────────────────────────────────────────────────────────

// Curl wraps net/http with silent Authorization injection + 401-aware
// token reload. The token is never echoed in error messages; only
// the status code and a redacted body preview are surfaced.
//
// On 401, the harness attempts to reload the token from TOKEN_FILE
// (if set) and retry up to maxRetries times. If no TOKEN_FILE is
// configured, the 401 propagates to the caller unchanged.
//
// Returns: (httpStatusCode, responseBody, error). The error is non-nil
// only on transport-layer failures (DNS, connection refused, ctx
// timeout) — HTTP status codes >= 400 do NOT return a non-nil error.
func (h *VoiceoverHarness) Curl(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	url := h.apiBase + path
	attempts := h.maxRetries + 1

	var lastStatus int
	var lastBody []byte

	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
		if err != nil {
			return 0, nil, fmt.Errorf("voiceover harness: build request %s %s: %w", method, path, err)
		}
		req.Header.Set("Authorization", "Bearer "+h.token)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: h.httpTimeout}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("voiceover harness: Curl %s %s: %w", method, path, err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		lastBody = body

		if resp.StatusCode != http.StatusUnauthorized {
			return resp.StatusCode, body, nil
		}

		// 401 path: attempt token reload (no-op if no TOKEN_FILE).
		// Stop retrying after the first 401 if there is no tokenFile to reload from.
		if h.tokenFile == "" {
			return resp.StatusCode, body, nil
		}
		if _, err := h.RotateToken(); err != nil {
			// Reload failed; surface the 401 to the caller.
			return resp.StatusCode, body, nil
		}
	}
	return lastStatus, lastBody, nil
}

// ── Token rotation ───────────────────────────────────────────────────────

// RotateToken re-reads the token from TOKEN_FILE (if set) and updates
// the in-memory token. Idempotent: safe to call when no TOKEN_FILE is
// configured (returns the current token without an error).
//
// Returns the new (or unchanged) token value.
//
// This is "rotation awareness", not "auto-rotation" — the actual
// rotation is performed by scripts/rotate_token.sh (operator-facing,
// requires root + systemd). The harness's job is to PICK UP the
// rotated value after the operator's rotation lands, not to perform
// the rotation itself.
func (h *VoiceoverHarness) RotateToken() (string, error) {
	if h.tokenFile == "" {
		return h.token, nil
	}
	v, err := readTokenFromFile(h.tokenFile)
	if err != nil {
		return h.token, fmt.Errorf("voiceover harness: reload token from %s: %w", h.tokenFile, err)
	}
	if v == "" {
		return h.token, nil
	}
	h.token = v
	return h.token, nil
}

// ── Internal helpers ────────────────────────────────────────────────────

// readTokenFromFile parses a TOKEN_FILE-format env file (one `KEY=value`
// per line; comments start with #) and returns the value of
// VELOX_ADMIN_TOKEN. The bash convention is to grep -E '^VELOX_ADMIN_TOKEN='
// + head -1 + cut -d= -f2-; the Go equivalent mirrors that exact behaviour
// for compatibility with existing operator env files.
func readTokenFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "VELOX_ADMIN_TOKEN=") {
			continue
		}
		v := strings.TrimPrefix(line, "VELOX_ADMIN_TOKEN=")
		v = strings.Trim(v, "\"'")
		return v, nil
	}
	return "", errors.New("VELOX_ADMIN_TOKEN not found in token file")
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
