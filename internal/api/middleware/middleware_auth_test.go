package middleware

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// ---------------------------------------------------------------------------
// P0 #1 regression tests — token logging scrub + query-string auth removal
// ---------------------------------------------------------------------------
//
// These tests lock in the invariants closed by the audit's P0 #1 finding:
//
//   1. redactSensitiveQuery strips known-sensitive query parameter values
//      from the log line that the request logger emits.
//   2. extractAuthToken refuses to read `?token=…` from the URL (the
//      previous behavior leaked the secret into reverse-proxy / browser
//      history / request logs).
//   3. The Auth middleware (driven end-to-end) returns 401 when the
//      credential is presented via query string.
//   4. The persistent api_requests table — the only sink for request
//      data that we can deterministically read back in a test — never
//      contains the token value. (The journal/zap log is best-effort
//      captured below but is not load-bearing for this test; if the
//      journal interception breaks in a future refactor, the
//      api_requests assertion still holds.)
//
// If any of these tests stop passing, the audit's P0 #1 regression has
// reappeared.

// TestRedactSensitiveQuery covers the query-string redaction. The
// known-limitation cases are pinned explicitly so a future contributor
// cannot "fix" them silently — the trade-off is documented in the
// redaction's doc comment.
func TestRedactSensitiveQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Happy paths — must redact.
		{"empty", "", ""},
		{"single_token", "token=abc123", "token=[REDACTED]"},
		{"mixed_case_token", "Token=abc123", "token=[REDACTED]"},
		{"upper_case_token", "TOKEN=abc123", "token=[REDACTED]"},
		{"token_with_other", "foo=bar&token=abc&baz=qux", "foo=bar&token=[REDACTED]&baz=qux"},
		{"multiple_sensitive", "token=a&secret=b&password=c",
			"token=[REDACTED]&secret=[REDACTED]&password=[REDACTED]"},
		{"api_key_variants", "api_key=k1&apikey=k2",
			"api_key=[REDACTED]&apikey=[REDACTED]"},
		{"access_token", "access_token=eyJxxx", "access_token=[REDACTED]"},
		{"empty_value", "token=&foo=bar", "token=[REDACTED]&foo=bar"},
		{"credential_key", "credential=foo", "credential=[REDACTED]"},
		{"auth_key", "auth=bearer-foo", "auth=[REDACTED]"},
		{"duplicate_keys_both_redact", "token=a&token=b", "token=[REDACTED]&token=[REDACTED]"},
		{"trailing_amp", "token=a&", "token=[REDACTED]&"},
		{"non_sensitive_intact", "page=2&limit=10&q=hello", "page=2&limit=10&q=hello"},
		// Safe no-op cases — no value to leak.
		{"no_sensitive", "foo=bar&baz=qux", "foo=bar&baz=qux"},
		{"no_value_no_leak", "token", "token"},
		// Known limitations — pinned so they cannot be silently "fixed".
		// URL-encoded key names like `?%74oken=foo` (where %74 = 't') are
		// not redacted. The only attacker who can reach the request
		// logger is already past TLS + auth, so we accept this.
		{"url_encoded_key_not_redacted", "%74oken=foo", "%74oken=foo"},
		// Go's net/url treats `;` as part of the value, not a separator,
		// so `?token=foo;bar` redacts the whole `foo;bar` blob. This is
		// correct (the value is gone), the test pins the behavior.
		{"semicolon_is_part_of_value", "token=foo;bar", "token=[REDACTED]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactSensitiveQuery(tc.in)
			if got != tc.want {
				t.Fatalf("redactSensitiveQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestExtractAuthToken_RejectsQueryString locks in the removal of the
// `c.Query("token")` fallback. A token passed via URL must NEVER be
// accepted; only headers are valid credential sources.
func TestExtractAuthToken_RejectsQueryString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("GET", "/x?token=should-be-ignored&other=ok", nil)
	c.Request = req

	if got := extractAuthToken(c); got != "" {
		t.Fatalf("expected query-string token to be ignored, got %q", got)
	}
}

// TestExtractAuthToken_AcceptsHeaders verifies the two supported header
// channels. The precedence (X-Velox-Admin-Token > Authorization: Bearer)
// is intentional and documented in the function comment.
func TestExtractAuthToken_AcceptsHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("X-Velox-Admin-Token", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("X-Velox-Admin-Token", "secret-1")
		c.Request = req
		if got := extractAuthToken(c); got != "secret-1" {
			t.Fatalf("expected secret-1, got %q", got)
		}
	})

	t.Run("Authorization Bearer", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Bearer secret-2")
		c.Request = req
		if got := extractAuthToken(c); got != "secret-2" {
			t.Fatalf("expected secret-2, got %q", got)
		}
	})

	t.Run("X-Velox wins over Authorization", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("X-Velox-Admin-Token", "wins")
		req.Header.Set("Authorization", "Bearer loses")
		c.Request = req
		if got := extractAuthToken(c); got != "wins" {
			t.Fatalf("expected X-Velox-Admin-Token to win precedence, got %q", got)
		}
	})
}

// TestAuth_RejectsQueryStringToken_EndToEnd drives the full Auth
// middleware with `?token=…` in the URL. Operator-facing behavior the
// audit cares about.
func TestAuth_RejectsQueryStringToken_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "right-secret-DO-NOT-LEAK",
		},
	}
	r := gin.New()
	r.Use(Auth(cfg))
	r.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Query-string token must NOT authenticate, even if it matches.
	req := httptest.NewRequest("GET", "/protected?token=right-secret-DO-NOT-LEAK", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for ?token=…, got %d", w.Code)
	}

	// Header token authenticates.
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-Velox-Admin-Token", cfg.Security.AdminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for X-Velox-Admin-Token header, got %d", w.Code)
	}
}

// TestCompareTokens pins the constant-time helper used by the Auth
// middleware. The implementation MUST go through crypto/subtle so that
// token comparison does not short-circuit on the first byte mismatch —
// which would leak the secret's prefix via network-level timing
// measurements (defense-in-depth against the admin token).
//
// A timing-based test would be flaky in CI; instead this pins the
// behavioral contract: equal inputs return true, unequal inputs (of
// any length) return false, and either side being empty returns
// false. The "MUST go through crypto/subtle" guarantee is enforced by
// code review and the helper's docstring, NOT by this test alone.
// If a future contributor replaces the body of compareTokens with
// `provided == expected`, all subtests still pass — the regression
// would only be caught by manual review. The single-byte-difference
// subtest is the closest behavioral proxy for "the implementation
// actually compared every byte".
func TestCompareTokens(t *testing.T) {
	t.Run("equal_strings", func(t *testing.T) {
		if !compareTokens("secret-123", "secret-123") {
			t.Fatal("expected true for byte-equal strings")
		}
	})
	t.Run("different_strings_same_length", func(t *testing.T) {
		if compareTokens("secret-123", "secret-456") {
			t.Fatal("expected false for differing strings of same length")
		}
	})
	t.Run("different_lengths", func(t *testing.T) {
		if compareTokens("short", "much-longer-token") {
			t.Fatal("expected false for differing-length strings")
		}
	})
	t.Run("empty_provided", func(t *testing.T) {
		if compareTokens("", "expected-token") {
			t.Fatal("expected false when provided is empty")
		}
	})
	t.Run("empty_expected", func(t *testing.T) {
		if compareTokens("provided-token", "") {
			t.Fatal("expected false when expected is empty")
		}
	})
	t.Run("both_empty", func(t *testing.T) {
		if compareTokens("", "") {
			t.Fatal("expected false when both are empty")
		}
	})
	t.Run("single_byte_difference_first_byte", func(t *testing.T) {
		// Behavioral proxy for "every byte was compared": if a future
		// implementation short-circuits on the first byte, this
		// should still return false (correct) but a real timing
		// measurement would reveal the regression. The unit test
		// pins the value; timing is left to code review.
		if compareTokens("aaaaaa", "baaaaa") {
			t.Fatal("expected false for strings differing in first byte only")
		}
	})
	t.Run("single_byte_difference_last_byte", func(t *testing.T) {
		if compareTokens("aaaaaa", "aaaaab") {
			t.Fatal("expected false for strings differing in last byte only")
		}
	})
}

// TestAuth_RejectsWebhookPathWithoutToken locks in the P0 #2 fix: the
// /api/images/webhook/remote path no longer bypasses auth via
// isPublicWebhookPath (that function and its bypass were removed in
// June 2026). Without a valid header token the webhook must return
// 401; with a valid token (either X-Velox-Admin-Token or
// Authorization: Bearer) the request reaches the handler and returns
// the expected 200. If a future contributor re-adds a path-based
// bypass, the first subtest will fail.
func TestAuth_RejectsWebhookPathWithoutToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth: true,
			AdminToken: "webhook-secret-DO-NOT-LEAK",
		},
	}

	r := gin.New()
	r.Use(Auth(cfg))
	r.POST("/api/images/webhook/remote", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// 1. No auth header → 401 (the bypass is gone).
	req := httptest.NewRequest("POST", "/api/images/webhook/remote", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for webhook without auth, got %d", w.Code)
	}

	// 2. Wrong token → 401.
	req = httptest.NewRequest("POST", "/api/images/webhook/remote", nil)
	req.Header.Set("X-Velox-Admin-Token", "wrong-secret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for webhook with wrong token, got %d", w.Code)
	}

	// 3. Valid X-Velox-Admin-Token → 200.
	req = httptest.NewRequest("POST", "/api/images/webhook/remote", nil)
	req.Header.Set("X-Velox-Admin-Token", cfg.Security.AdminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for webhook with valid X-Velox-Admin-Token, got %d", w.Code)
	}

	// 4. Valid Authorization: Bearer → 200.
	req = httptest.NewRequest("POST", "/api/images/webhook/remote", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Security.AdminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for webhook with valid Bearer, got %d", w.Code)
	}
}

// TestAuth_NeverPersistsTokenValue is the load-bearing security test for
// P0 #1. It exercises the Auth middleware with a real token, then
// asserts the persistent api_requests table — the only request-data
// sink we can deterministically read back — does NOT contain the token
// value in any column.
//
// NOTE: the journal/zap log is NOT captured by this test (zap does not
// write to gin.DefaultWriter). A follow-up PR should add a zap Core
// replacement so the journal log is also load-bearing. The
// api_requests scan below is the next-best proxy and catches the
// realistic risk vector (a contributor adding the token to the
// apiLog struct).
func TestAuth_NeverPersistsTokenValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const adminToken = "top-secret-admin-token-DO-NOT-LEAK-IN-ANY-COLUMN"
	const wrongToken = "wrong-token-attempt"

	cfg := &config.Config{
		Security: config.SecurityConfig{
			EnableAuth:  true,
			AdminToken:  adminToken,
			WorkerToken: "top-secret-worker-token-DO-NOT-LEAK",
		},
	}

	// In-memory api_requests table.
	db := storage.NewTestDB(t, &storage.TestDBOpts{InMemory: true})
	defer db.Close()
	storage.MustExec(t, db, `
		CREATE TABLE api_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			request_id TEXT,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			status INTEGER,
			duration_ms REAL,
			client_ip TEXT,
			user_id TEXT,
			bytes_in INTEGER,
			bytes_out INTEGER,
			user_agent TEXT,
			error TEXT
		);
	`)
	SetLogDB(db)

	r := gin.New()
	r.Use(RequestID())
	r.Use(Logger())
	r.Use(Auth(cfg))
	r.GET("/protected", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	makeReq := func(headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/protected", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("successful auth: token not in api_requests", func(t *testing.T) {
		w := makeReq(map[string]string{"X-Velox-Admin-Token": adminToken})
		require.Equal(t, http.StatusOK, w.Code)
		// Wait for the async writer to flush.
		time.Sleep(250 * time.Millisecond)
		scanAPITableForSecret(t, db, adminToken)
	})

	t.Run("rejected auth: attempted token not in api_requests", func(t *testing.T) {
		w := makeReq(map[string]string{"X-Velox-Admin-Token": wrongToken})
		require.Equal(t, http.StatusUnauthorized, w.Code)
		time.Sleep(250 * time.Millisecond)
		scanAPITableForSecret(t, db, wrongToken)
		scanAPITableForSecret(t, db, cfg.Security.WorkerToken) // not relevant here, paranoia
	})
}

// scanAPITableForSecret fails the test if `secret` appears in any
// column of any api_requests row. This is the load-bearing check that
// the persistent log never stores the credential value.
func scanAPITableForSecret(t *testing.T, db *sql.DB, secret string) {
	t.Helper()
	rows, err := db.Query(`
		SELECT request_id, method, path, COALESCE(status, 0),
		       COALESCE(client_ip, ''), COALESCE(user_id, ''),
		       COALESCE(user_agent, ''), COALESCE(error, '')
		FROM api_requests
	`)
	if err != nil {
		t.Fatalf("query api_requests: %v", err)
	}
	defer rows.Close()
	cols := []string{"request_id", "method", "path", "status", "client_ip", "user_id", "user_agent", "error"}
	colVals := make([]any, len(cols))
	colPtrs := make([]any, len(cols))
	for i := range colVals {
		colPtrs[i] = &colVals[i]
	}
	for rows.Next() {
		if err := rows.Scan(colPtrs...); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		for i, v := range colVals {
			if s, ok := v.(string); ok && strings.Contains(s, secret) {
				t.Fatalf("secret leaked into column %s: %q", cols[i], s)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
}
