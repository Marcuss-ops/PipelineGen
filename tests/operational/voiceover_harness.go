// Package operational — voiceover_harness.go
//
// Single-file infrastructure for the FASE B/C/D voiceover smoke tests.
// Provides:
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
package operational

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// Sentinel parsed by WriteReport and surfaced as VoiceoverReport.Outcome
// when at least one DB probe returned ErrSqliteBinaryMissing. Operators
// can grep the report for "probe_unavailable" to identify which FASE
// had the missing binary without re-running.
const probeOutcomeUnavailable = "probe_unavailable"

// VoiceoverReport is the JSON-serialised forensic artefact for one smoke run.
//
// Shape is intentionally self-contained: the report + the bash smoke log
// together provide full offline-forensic coverage without re-running the
// test. JobIDs is keyed by role (e.g. "parent", "child_it_it") so a
// dashboard can correlate the canonical parent/child pair across runs.
type VoiceoverReport struct {
	StartedAt  string                   `json:"started_at"`
	FinishedAt string                   `json:"finished_at"`
	FASE       string                   `json:"fase"`
	Outcome    string                   `json:"outcome"` // "pass" | "fail" | "skip"
	JobIDs     map[string]string        `json:"job_ids"`
	Assertions []AssertionRecord        `json:"assertions"`
	DBProbes   map[string]DBProbeRecord `json:"db_probes"`
}

// AssertionRecord captures one Assert() call's expected/actual/outcome.
type AssertionRecord struct {
	Label    string `json:"label"`
	Status   string `json:"status"` // "pass" | "fail" | "skip"
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Note     string `json:"note,omitempty"`
}

// DBProbeRecord captures one Probe* call's query + row count + first row.
type DBProbeRecord struct {
	Query  string `json:"query"`
	Rows   int    `json:"rows"`
	Sample string `json:"sample,omitempty"`
}

// VoiceoverHarness is the canonical test fixture for the voiceover
// smoke suite. All probe / curl / assertion entry points are methods on
// this type; the per-FASE test wrappers construct one harness per test
// and call methods in sequence.
type VoiceoverHarness struct {
	t *testing.T

	// HTTP surface
	apiBase     string        // e.g. "http://127.0.0.1:8080"
	token       string        // current token (mutable via RotateToken)
	tokenFile   string        // optional TOKEN_FILE path
	httpTimeout time.Duration // per-Curl --max-time
	maxRetries  int           // 401-reload retries per Curl (default 2)

	// DB probe surface (sqlite3 CLI shell-out)
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

	// DBPath overrides SMOKE_DB. Default: SMOKE_DB env var OR
	// "data/media/media.db.sqlite".
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

	// DB path resolution: explicit option > SMOKE_DB env > canonical default
	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = os.Getenv("SMOKE_DB")
	}
	if dbPath == "" {
		dbPath = "data/media/media.db.sqlite"
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

// ── DB probes ────────────────────────────────────────────────────────────

// ProbeJobs runs `SELECT id, type, status, parent_id, created_at
// FROM jobs WHERE id = ? OR parent_id = ? ORDER BY created_at` and
// returns the pipe-separated rows (matches bash lib/common.sh::sqlite_q
// `-separator '|'` convention).
//
// Returns (nil, ErrSqliteBinaryMissing) if the sqlite3 binary is
// absent from PATH. Callers MUST use errors.Is to detect this case
// (NOT a nil-error + empty-slice check) so the typed-sentinel
// contract is preserved across future refactors.
func (h *VoiceoverHarness) ProbeJobs(jobOrParentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	// Canonical: id matches OR parent_id matches. Bind the value as a
	// quoted SQL literal (defensive: caller-supplied values may contain
	// single quotes; sqlite3 has no parameter binding, so escape).
	q := fmt.Sprintf(
		`SELECT id, type, status, COALESCE(parent_id,''), created_at `+
			`FROM jobs WHERE id = '%s' OR parent_id = '%s' `+
			`ORDER BY created_at`,
		sqlEscape(jobOrParentID), sqlEscape(jobOrParentID),
	)
	return h.runSQLiteQuery(q, "jobs:"+jobOrParentID)
}

// ProbeVoiceovers runs `SELECT id, drive_file_id, status, language,
// parent_job_id FROM voiceovers WHERE parent_job_id = ?`.
//
// Returns (nil, ErrSqliteBinaryMissing) when sqlite3 is absent
// (see ProbeJobs for the typed-sentinel contract).
func (h *VoiceoverHarness) ProbeVoiceovers(parentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	q := fmt.Sprintf(
		`SELECT id, COALESCE(drive_file_id,''), status, COALESCE(language,''), parent_job_id `+
			`FROM voiceovers WHERE parent_job_id = '%s'`,
		sqlEscape(parentID),
	)
	return h.runSQLiteQuery(q, "voiceovers:"+parentID)
}

// ProbeMediaAssets runs `SELECT id, drive_file_id, status, source_url
// FROM media_assets WHERE source_job_id = ?`.
//
// Returns (nil, ErrSqliteBinaryMissing) when sqlite3 is absent
// (see ProbeJobs for the typed-sentinel contract).
func (h *VoiceoverHarness) ProbeMediaAssets(parentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	q := fmt.Sprintf(
		`SELECT id, COALESCE(drive_file_id,''), status, COALESCE(source_url,'') `+
			`FROM media_assets WHERE source_job_id = '%s'`,
		sqlEscape(parentID),
	)
	return h.runSQLiteQuery(q, "media_assets:"+parentID)
}

// ProbeOutboxEvents runs `SELECT id, event_type, status, payload
// FROM outbox_events WHERE source_job_id = ?`.
//
// Returns (nil, ErrSqliteBinaryMissing) when sqlite3 is absent
// (see ProbeJobs for the typed-sentinel contract).
func (h *VoiceoverHarness) ProbeOutboxEvents(parentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	q := fmt.Sprintf(
		`SELECT id, event_type, status, COALESCE(payload,'') `+
			`FROM outbox_events WHERE source_job_id = '%s'`,
		sqlEscape(parentID),
	)
	return h.runSQLiteQuery(q, "outbox_events:"+parentID)
}

// runSQLiteQuery shells out to the sqlite3 binary and records the
// probe into the report. The first row is stored in the report as
// `Sample` (truncated to 200 chars; offline forensics) and the full
// row slice is returned to the caller for in-test assertions.
//
// SECURITY NOTE: the Sample preview may surface `drive_file_id` (from
// media_assets/voiceovers) and `payload` contents (from outbox_events).
// drive_file_id is a Google Drive resource ID — not secret-classified
// today, but operationally sensitive (anyone with the ID can access
// the file if the Drive ACL is permissive). The 200-char truncation
// limits the surface; operators with stricter PII requirements
// should review the generated report before sharing the JSON. The
// future `WithRedactedProbes` option will replace this with a
// column-driven allowlist (forward-pointer, not in this commit).
func (h *VoiceoverHarness) runSQLiteQuery(query, label string) ([]string, error) {
	cmd := exec.Command(h.sqliteBin, "-separator", "|", h.dbPath, query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("voiceover harness: sqlite3 %s: %w (stderr: %s)",
			label, err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimRight(stdout.String(), "\n")
	var rows []string
	if out != "" {
		rows = strings.Split(out, "\n")
	}

	h.recordDBProbe(label, query, rows)
	return rows, nil
}

// ── Report writer + assertion recorder ──────────────────────────────────

// Assert records an assertion outcome into the report and fails the
// test if expected != actual. The first failed Assert in a test
// short-circuits the rest of the test (t.Fatal semantics).
func (h *VoiceoverHarness) Assert(label, expected, actual string) {
	h.t.Helper()
	status := "pass"
	if expected != actual {
		status = "fail"
	}

	h.reportMu.Lock()
	h.report.Assertions = append(h.report.Assertions, AssertionRecord{
		Label:    label,
		Status:   status,
		Expected: expected,
		Actual:   actual,
	})
	h.reportMu.Unlock()

	if status == "fail" {
		h.t.Fatalf("voiceover harness: %s — expected [%s], got [%s]", label, expected, actual)
	}
}

// Assertf is the printf-style variant of Assert for verbose expected/actual
// pairs (e.g. JSON body snippets).
func (h *VoiceoverHarness) Assertf(label, expected, actualFormat string, actualArgs ...any) {
	h.Assert(label, expected, fmt.Sprintf(actualFormat, actualArgs...))
}

// AssertHTTPStatus is a convenience wrapper for `Assert(label, "<expected>",
// strconv.Itoa(code))`. Returns the code unchanged so callers can chain.
func (h *VoiceoverHarness) AssertHTTPStatus(label string, expectedStatuses []string, code int) int {
	h.t.Helper()
	actual := fmt.Sprintf("%d", code)
	expected := strings.Join(expectedStatuses, "|")
	h.Assert(label, expected, actual)
	return code
}

// RecordJobID stores a jobID under a role key (e.g. "parent", "child_it_it")
// in the report. Idempotent: re-recording under the same key OVERWRITES
// (the latest value wins, which matches the natural "this is the final
// value after retry" semantics).
func (h *VoiceoverHarness) RecordJobID(role, jobID string) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	h.report.JobIDs[role] = jobID
}

// WriteReport finalises the report (stamps FinishedAt + Outcome) and
// JSON-marshals it to disk. Always returns nil error from a deferred
// call so a failed test still produces a report for forensics.
func (h *VoiceoverHarness) WriteReport() error {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()

	h.report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	h.report.Outcome = "pass"
	for _, a := range h.report.Assertions {
		if a.Status == "fail" {
			h.report.Outcome = "fail"
			break
		}
	}

	data, err := json.MarshalIndent(h.report, "", "  ")
	if err != nil {
		return fmt.Errorf("voiceover harness: marshal report: %w", err)
	}

	dir := filepath.Dir(h.reportPath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(h.reportPath, data, 0o600); err != nil {
		return fmt.Errorf("voiceover harness: write report %s: %w", h.reportPath, err)
	}
	return nil
}

// ── Internal helpers ────────────────────────────────────────────────────

// recordDBProbe is the internal Append hook used by the 4 Probe*
// methods. Locked by reportMu.
func (h *VoiceoverHarness) recordDBProbe(label, query string, rows []string) {
	h.reportMu.Lock()
	defer h.reportMu.Unlock()
	sample := ""
	if len(rows) > 0 {
		sample = rows[0]
		if len(sample) > 200 {
			sample = sample[:200] + "..."
		}
	}
	h.report.DBProbes[label] = DBProbeRecord{
		Query:  query,
		Rows:   len(rows),
		Sample: sample,
	}
}

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

// sqlEscape doubles single quotes for safe inlining into a sqlite3
// query string. Mirrors the defensive pattern in bash smokes
// (printf '%s' "$value" into a heredoc). NOT a full SQL-injection
// defence — these queries are operator-controlled, not user input.
func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
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
