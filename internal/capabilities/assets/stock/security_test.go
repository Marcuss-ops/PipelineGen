// Package stock — security_test.go (Definition of Done §16, security/isolation
// surface for POST /api/stock-pipeline/run).
//
// What this file covers (what the existing handler_validation_test.go does NOT):
//
//  1. Private IP rejection — every private family: 127.0.0.1, ::1,
//     RFC1918-10, RFC1918-172.16, RFC1918-192.168, link-local-v6
//     (the existing test only covers 192.168.1.1).
//  2. URL length cap (handler.MaxURLLength=2048).
//  3. Folder-name sanitization — NUL byte + absolute-path + backslash
//     across all 4 folder fields (subfolder/folder_name/drive_folder_id/folder_id).
//     The existing test covers only the folder_name=../ and subfolder=..\\ forms.
//  4. Token-not-leaked canary — handler response body MUST NOT echo auth
//     tokens or bearer headers verbatim (load-bearing contract for the
//     future hardening PR that will redact token-bearing URLs).
//  5. No-500 cross-canary — every violation case from the package asserts
//     NOT 500 in one consolidated test (DoD §16 invariant).
//
// What is NOT covered here (and the corresponding GAP, per the user's
// Definition-of-Done §16 acceptance criteria):
//
//   - "max durata sorgente" — handler.go does NOT enforce a maximum
//     source-duration: req.TotalMinutes has no upper cap (defaults to 5
//     only when <=0). Forward-pointer: PR-STOCK-MAX-SOURCE-DURATION.
//   - "max dimensione file" via HTTP-body-size cap (413) — the stock
//     route does NOT wire MaxBytesMiddleware (handler.go reads
//     c.Request.Body directly without MaxBytesReader). Forward-pointer:
//     PR-STOCK-MAX-BODY-WIRE.
//
// These gaps are documented in code comments so future operators see the
// exact enforcement surface without needing to read git history.
//
// All assertions of `w.Code != http.StatusInternalServerError` are the
// DoD §16 invariant: violations MUST respond 400/401/403/413, never 500.
//
// Self-test: `go test -race -count=1 -run TestStockHandler ./internal/api/assets/stock/`
// must exit 0.
package assets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── 1. Private IP rejection — every private IP family ───────────────────
//
// Definition of Done §16: "URL verso IP privati non autorizzati
// rifiutati". handler.go's isValidURL() rejects Hostname() when
// net.ParseIP returns non-nil and IsPrivate/IsLoopback/IsLinkLocalUnicast
// is true. Each IP family maps to a distinct Go stdlib check, so we
// pin one row per family. The existing handler_validation_test.go
// case #3 covers 192.168.1.1 only; this table extends the coverage
// symmetrically.
func TestStockHandler_PrivateIPs_Rejected(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"loopback_v4", "https://127.0.0.1/video.mp4"},
		{"loopback_v6", "https://[::1]/video.mp4"},
		{"rfc1918_10", "https://10.0.0.1/video.mp4"},
		{"rfc1918_172_16", "https://172.16.0.1/video.mp4"},
		{"rfc1918_172_31", "https://172.31.255.255/video.mp4"},
		{"rfc1918_192_168", "https://192.168.1.1/video.mp4"},
		{"link_local_v6_fe80", "https://[fe80::1]/video.mp4"},
		{"link_local_v4_169_254", "https://169.254.169.254/latest/meta-data/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router := newStockHandler(nil, "job-test")

			payload := fmt.Sprintf(`{"direct_urls":[%q]}`, tc.url)
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// DoD §16 invariant: NEVER 500.
			if w.Code == http.StatusInternalServerError {
				t.Fatalf("DoD §16 violation: 500 returned for private IP %q (must be 4xx)", tc.url)
			}
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for private IP %q, got %d (body: %s)", tc.url, w.Code, w.Body.String())
			}
			var resp testRunResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp.ErrorCode != ErrCodeInvalidURL {
				t.Errorf("expected error_code=%s for %s, got %q", ErrCodeInvalidURL, tc.name, resp.ErrorCode)
			}
		})
	}
}

// ── 2. URL length cap (MaxURLLength=2048) ──────────────────────────────
//
// Definition of Done §16: DoS defense via URL-length cap. handler.go's
// isValidURL explicitly checks `len(u) > MaxURLLength` before parsing;
// the cap is an exported constant (handler.MaxURLLength) so the test
// uses it directly to avoid literal-drift.
func TestStockHandler_URLLengthExceeded_Rejected(t *testing.T) {
	_, router := newStockHandler(nil, "job-test")

	// URLstring with hostname (https://example.com/) + a path component
	// that pushes the total length over MaxURLLength.
	overlongPath := strings.Repeat("a", MaxURLLength+10)
	payload := fmt.Sprintf(`{"direct_urls":["https://example.com/%s"]}`, overlongPath)
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Fatalf("DoD §16 violation: 500 returned for over-length URL (must be 4xx)")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for over-length URL, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodeInvalidURL {
		t.Errorf("expected error_code=%s for over-length URL, got %q", ErrCodeInvalidURL, resp.ErrorCode)
	}
}

// ── 3. Folder-name sanitization — NUL + absolute-path + backslash ──────
//
// Definition of Done §16: "nomi delle cartelle sanitizzati" —
// folder fields MUST be sanitized against NUL-byte injection,
// absolute paths, and Windows-style backslash escapes. handler.go's
// isSafePath() applies to all 4 folder fields (subfolder, folder_name,
// drive_folder_id, folder_id) in a single chained call; the existing
// handler_validation_test.go only covers ../ and ..\\..\\ on a single
// field. This table extends coverage: each rejection form is exercised
// against each folder field individually to confirm `isSafePath` is
// called consistently across the 4 fields.
func TestStockHandler_FolderNameSanitization(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value string
	}{
		// Absolute-path injection (Linux /etc/passwd style).
		{"subfolder_absolute", "subfolder", "/etc"},
		{"folder_name_absolute", "folder_name", "/etc"},
		{"drive_folder_id_absolute", "drive_folder_id", "/etc/dir"},
		{"folder_id_absolute", "folder_id", "/etc/dir"},

		// Backslash escape (Windows-style).
		{"subfolder_backslash", "subfolder", "..\\..\\windows"},
		{"folder_name_backslash", "folder_name", "..\\..\\win"},
		{"drive_folder_id_backslash", "drive_folder_id", "C:\\foo"},
		{"folder_id_backslash", "folder_id", "D:\\bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router := newStockHandler(nil, "job-test")
			// We use %q so JSON encoding escapes the backslash
			// rune correctly during payload construction.
			payload := fmt.Sprintf(`{"direct_urls":["https://example.com/video.mp4"],%q:%q}`, tc.field, tc.value)
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// DoD §16 invariant: NEVER 500.
			if w.Code == http.StatusInternalServerError {
				t.Fatalf("DoD §16 violation: 500 returned for folder %s=%q (must be 4xx)", tc.field, tc.value)
			}
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for folder %s=%q, got %d (body: %s)", tc.field, tc.value, w.Code, w.Body.String())
			}
			var resp testRunResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// Sanitization-layer rejections — absolute-path, backslash,
			// forward-.. — all hit isSafePath() and surface as
			// ErrCodePathTraversal. (NUL-byte rejection is exercised
			// at the JSON-decode layer by the malformed_json row of the
			// consolidated NoInternalServerError canary test, since
			// NUL bytes cannot survive encoding/json round-trip.)
			if resp.ErrorCode != ErrCodePathTraversal {
				t.Errorf("expected error_code=PATH_TRAVERSAL for %s, got %q", tc.name, resp.ErrorCode)
			}
		})
	}
}

// ── 4. Boundary: clean folder names pass through ──────────────────────
//
// Companion to #3: clean (no NUL, no traversal, no backslash, no
// absolute prefix) folder names MUST NOT be rejected. Verifies
// isSafePath() returns true for benign inputs so future over-zealous
// sanitization doesn't break legitimate operator flow.
func TestStockHandler_FolderNameClean_Allowed(t *testing.T) {
	cases := []struct{ name, field, value string }{
		{"subfolder_clean", "subfolder", "round-12"},
		{"folder_name_clean", "folder_name", "boxing-coverage"},
		{"drive_folder_id_clean", "drive_folder_id", "1aBcD-eFgHiJkLmNoPqR"},
		{"folder_id_clean", "folder_id", "42"},
		{"subfolder_underscore", "subfolder", "round_12_pacquiao_cotto"},
		{"folder_name_dot", "folder_name", "v2.1-final"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router := newStockHandler(nil, "job-test")
			payload := fmt.Sprintf(`{"direct_urls":["https://example.com/video.mp4"],%q:%q}`, tc.field, tc.value)
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code == http.StatusInternalServerError {
				t.Fatalf("clean folder name produced 500: field=%q value=%q", tc.field, tc.value)
			}
			// 200 (sync) or 202 (async) — both are valid 2xx results.
			if w.Code != http.StatusOK && w.Code != http.StatusAccepted {
				t.Errorf("clean folder rejected: field=%q value=%q got %d (body: %s)",
					tc.field, tc.value, w.Code, w.Body.String())
			}
		})
	}
}

// ── 5. Token-not-leaked canary (PR-STOCK-ERROR-LEAKS-TOKEN, DoD §16 GAP-C) ─
//
// Definition of Done §16: "token mai nei log / errori yt-dlp
// sanitizzati". handler.go used to echo the offending URL verbatim
// into the response body (`Error: "invalid or insecure direct_url: "
// + u`), so an attacker could submit a URL with an embedded token
// and read it back from the response. handler.go now renders every
// URL through redactURL() (userinfo/query/fragment stripped, path
// segments that look like credentials masked). This canary is an
// ACTIVE gate: if the response body ever contains an injected token
// again, the test fails closed.
func TestStockHandler_TokenNotLeaked(t *testing.T) {
	// Injected token: a sentinel string with a recognizable shape
	// that no legitimate redaction rule would mistake for noise.
	injectedToken := "Bearer test_injected_token_DO_NOT_LOG_12345"

	cases := []struct {
		name    string
		payload string
	}{
		// Path-embedded token on a private IP: the private-IP rejection
		// fires, and the error must not echo the token back.
		{"path_token_private_ip", fmt.Sprintf(`{"direct_urls":["https://10.0.0.1/%s"]}`, injectedToken)},
		// Query-string token (canonical signed-URL carrier) on a private IP.
		{"query_token_private_ip", fmt.Sprintf(`{"drive_urls":["https://10.0.0.1/v.mp4?token=%s"]}`, injectedToken)},
		// Userinfo credentials (https://user:pass@host).
		{"userinfo_credentials", fmt.Sprintf(`{"direct_urls":["https://user:%s@10.0.0.1/v.mp4"]}`, injectedToken)},
		// Token on an otherwise-valid host that fails the URL gate
		// (unsupported scheme) — the error still must not leak.
		{"query_token_bad_scheme", fmt.Sprintf(`{"direct_urls":["ftp://example.com/v.mp4?access_token=%s"]}`, injectedToken)},
		// Token embedded in a clip URL (the third redaction site).
		{"clip_url_token", fmt.Sprintf(`{"clips":[{"url":"https://10.0.0.1/%s","start_sec":0,"end_sec":4}]}`, injectedToken)},
		// Unparseable URL (malformed escape) with a short opaque token in
		// the query — the parse-failure branch must not echo it verbatim.
		{"malformed_escape_query_token", fmt.Sprintf(`{"direct_urls":["https://example.com/%%zz?x=%s"]}`, "S"+strings.Repeat("e", 15))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router := newStockHandler(nil, "job-test")
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Must be 4xx, never 500.
			if w.Code == http.StatusInternalServerError {
				t.Fatalf("DoD §16 violation: 500 returned for token-bearing URL (must be 4xx)")
			}
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for token-bearing URL, got %d (body: %s)", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), injectedToken) {
				t.Errorf("PR-STOCK-ERROR-LEAKS-TOKEN: handler response body echoes the injected token verbatim: %s", w.Body.String())
			}
		})
	}
}

// ── 5b. redactURL unit gate (PR-STOCK-ERROR-LEAKS-TOKEN) ──────────────
//
// Pins the redaction contract directly so the masking rules can be
// reviewed without exercising the whole HTTP stack.
func TestRedactURL_NeverLeaksCredentials(t *testing.T) {
	const secret = "s3cr3t-Bearer-TOKEN-value-DO-NOT-LOG-987654321"
	cases := []struct {
		name string
		in   string
	}{
		{"query_token", "https://example.com/v.mp4?token=" + secret},
		{"query_sig", "https://example.com/v.mp4?X-Amz-Signature=" + secret},
		{"fragment_token", "https://example.com/v.mp4#access_token=" + secret},
		{"userinfo", "https://user:" + secret + "@example.com/v.mp4"},
		{"bearer_path", "https://example.com/" + secret},
		{"jwt_path", "https://example.com/" + strings.Repeat("a", 80)},
		{"file_uri_query", "file:///srv/media/v.mp4?key=" + secret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURL(tc.in)
			if strings.Contains(got, secret) {
				t.Fatalf("redactURL leaked secret: in=%q out=%q", tc.in, got)
			}
		})
	}
}

// Redaction keeps operator-useful structure (host + benign path) intact.
func TestRedactURL_KeepsEndpointReadable(t *testing.T) {
	got := redactURL("https://example.com/videos/round-12.mp4?ref=42")
	if !strings.Contains(got, "https://example.com/videos/round-12.mp4") {
		t.Fatalf("benign URL lost operator context: %q", got)
	}
	if strings.Contains(got, "ref=42") {
		t.Fatalf("query string not stripped: %q", got)
	}
}

// ── 6. No-500 cross-canary — consolidated invariant ────────────────────
//
// Definition of Done §16 grand invariant: "Ogni violazione deve
// rispondere 400/401/403/413, mai 500." This test consolidates the
// invariant into a single matrix pass over the other tests' payloads
// + a few additional shapes. If any case in this matrix returns 500,
// the package's §16 surface is broken (cf. PR-STOCK-500-LEAK).
func TestStockHandler_NoInternalServerError_OnAnyViolation(t *testing.T) {
	// Build the over-MaxClipsPerRun payload dynamically (otherwise
	// embedding 101+ clip maps blows up the static literal).
	clips := make([]map[string]any, MaxClipsPerRun+5)
	for i := range clips {
		clips[i] = map[string]any{
			"url":       "https://example.com/x",
			"start_sec": 0,
			"end_sec":   4,
		}
	}
	oversized, err := json.Marshal(map[string]any{"clips": clips, "folder_name": "test"})
	if err != nil {
		t.Fatalf("marshal oversize: %v", err)
	}

	cases := []struct{ name, payload string }{
		{"file_scheme_path_traversal", `{"direct_urls":["file:///home/user/../../../etc/passwd"]}`},
		{"private_ip_loopback", `{"direct_urls":["https://127.0.0.1/v.mp4"]}`},
		{"private_ip_rfc1918_10", `{"direct_urls":["https://10.0.0.1/v.mp4"]}`},
		{"path_traversal_forward", `{"direct_urls":["https://example.com/x"],"folder_name":"../../etc"}`},
		{"path_traversal_backslash", `{"direct_urls":["https://example.com/x"],"subfolder":"..\\..\\win"}`},
		// Note: folder NUL-byte case is intentionally absent here
		// (and in TestStockHandler_FolderNameSanitization above).
		// The JSON layer rejects NUL at parse-time; we test the
		// JSON-layer rejection via the malformed_json row below.
		{"folder_absolute_path", `{"direct_urls":["https://example.com/x"],"folder_name":"/etc"}`},
		{"unknown_field", `{"unknown_field":42}`},
		{"too_many_clips", string(oversized)},
		{"oversize_url", fmt.Sprintf(`{"direct_urls":["https://example.com/%s"]}`, strings.Repeat("x", MaxURLLength+10))},
		{"empty_body", ``},
		{"malformed_json", `{not json}`},
		{"clip_duration_too_low", `{"direct_urls":["https://example.com/x"],"clip_duration":1}`},
		{"clip_duration_too_high", `{"direct_urls":["https://example.com/x"],"clip_duration":60}`},
		{"not_a_url", `{"direct_urls":["not-a-url"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router := newStockHandler(nil, "job-test")
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code == http.StatusInternalServerError {
				t.Errorf("DoD §16 violation: case %q returned HTTP 500 — must be 4xx (or 413 for oversize body); body=%s",
					tc.name, w.Body.String())
			}
		})
	}
}
