package drive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

// mutableTokenSource lets a test change the token the source returns, so the
// refreshingTokenSource's change-detection can be exercised deterministically.
type mutableTokenSource struct {
	mu    sync.Mutex
	token *oauth2.Token
}

func (m *mutableTokenSource) Token() (*oauth2.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *m.token
	return &clone, nil
}

func (m *mutableTokenSource) set(token *oauth2.Token) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = token
}

func tokenFileContents(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	return string(data)
}

func assertNoTempResidue(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".token-*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic write left temp residue: %v", matches)
	}
}

// TestSaveToken_RoundTripsAndLeavesNoResidue pins the on-disk contract: the
// written file is a parseable oauth2 token at 0600 and no temp file survives.
func TestSaveToken_RoundTripsAndLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	want := &oauth2.Token{AccessToken: "access-abc", RefreshToken: "refresh-xyz", TokenType: "Bearer"}

	if err := SaveToken(path, want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600 (credential)", perm)
	}
	got, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("round trip = %#v, want access=%q refresh=%q", got, want.AccessToken, want.RefreshToken)
	}
	assertNoTempResidue(t, dir)
}

// TestSaveToken_ReplacesExistingFileAtomically pins that a second write fully
// replaces the previous document (no truncation leftover, no partial JSON).
func TestSaveToken_ReplacesExistingFileAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	if err := SaveToken(path, &oauth2.Token{AccessToken: "first", RefreshToken: "r1"}); err != nil {
		t.Fatalf("first SaveToken: %v", err)
	}
	if err := SaveToken(path, &oauth2.Token{AccessToken: "second", RefreshToken: "r2"}); err != nil {
		t.Fatalf("second SaveToken: %v", err)
	}
	got, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken after replace: %v", err)
	}
	if got.AccessToken != "second" || got.RefreshToken != "r2" {
		t.Fatalf("replaced token = %#v, want access=second refresh=r2", got)
	}
	// The file must be a complete JSON document, not a truncated prefix.
	var raw map[string]any
	if err := json.Unmarshal([]byte(tokenFileContents(t, path)), &raw); err != nil {
		t.Fatalf("replaced token file is not valid JSON: %v", err)
	}
	assertNoTempResidue(t, dir)
}

// TestRefreshingTokenSource_DoesNotRewriteUnchangedToken pins the cost fix:
// oauth2 reuses a cached token until it expires, so a call that returns the
// same access token must not touch the file at all. A sentinel loaded into the
// file after the first call is the detector — an unconditional writer would
// overwrite it.
func TestRefreshingTokenSource_DoesNotRewriteUnchangedToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	source := &mutableTokenSource{token: &oauth2.Token{AccessToken: "stable-token", RefreshToken: "r"}}
	refresher := &refreshingTokenSource{source: source, tokenFile: path}

	if _, err := refresher.Token(); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	const sentinel = `{"sentinel":"must survive a no-op call"}`
	if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	for i := 0; i < 3; i++ {
		token, err := refresher.Token()
		if err != nil {
			t.Fatalf("Token #%d: %v", i, err)
		}
		if token.AccessToken != "stable-token" {
			t.Fatalf("Token #%d returned %q", i, token.AccessToken)
		}
	}
	if got := tokenFileContents(t, path); got != sentinel {
		t.Fatalf("unchanged token rewrote the file; contents = %q, want the sentinel untouched", got)
	}
	assertNoTempResidue(t, dir)
}

// TestRefreshingTokenSource_PersistsChangedToken pins the other half: a real
// refresh (a new access token) IS persisted, so a restart reuses it.
func TestRefreshingTokenSource_PersistsChangedToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	source := &mutableTokenSource{token: &oauth2.Token{AccessToken: "old-token", RefreshToken: "r"}}
	refresher := &refreshingTokenSource{source: source, tokenFile: path}

	if _, err := refresher.Token(); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	source.set(&oauth2.Token{AccessToken: "new-token", RefreshToken: "r"})
	if _, err := refresher.Token(); err != nil {
		t.Fatalf("second Token: %v", err)
	}

	got, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if got.AccessToken != "new-token" {
		t.Fatalf("persisted access token = %q, want new-token", got.AccessToken)
	}
	assertNoTempResidue(t, dir)
}

// TestRefreshingTokenSource_RetriesPersistAfterFailedWrite pins that the
// change-tracker records only SUCCESSFUL writes: a failed persist must be
// retried by the next call instead of being silently treated as done.
func TestRefreshingTokenSource_RetriesPersistAfterFailedWrite(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-yet")
	path := filepath.Join(missing, "token.json")
	source := &mutableTokenSource{token: &oauth2.Token{AccessToken: "retry-token", RefreshToken: "r"}}
	refresher := &refreshingTokenSource{source: source, tokenFile: path}

	// The parent directory does not exist, so the atomic write must fail…
	if _, err := refresher.Token(); err != nil {
		t.Fatalf("failed persist must not fail the token call: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file unexpectedly exists after a failed write: %v", err)
	}
	if refresher.persistedAccessToken != "" {
		t.Fatalf("failed write recorded as persisted: %q", refresher.persistedAccessToken)
	}

	// …and once the directory exists the SAME (unchanged) token is persisted,
	// because the earlier failure never marked it persisted.
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := refresher.Token(); err != nil {
		t.Fatalf("retry Token: %v", err)
	}
	got, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken after retry: %v", err)
	}
	if got.AccessToken != "retry-token" {
		t.Fatalf("persisted access token = %q, want retry-token", got.AccessToken)
	}
}

// TestRefreshingTokenSource_ConcurrentCallsLeaveParseableFile pins atomicity
// under concurrency: parallel Drive requests share one token source, and the
// file must never be observable in a truncated state.
func TestRefreshingTokenSource_ConcurrentCallsLeaveParseableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	source := &mutableTokenSource{token: &oauth2.Token{AccessToken: "concurrent-token", RefreshToken: "r"}}
	refresher := &refreshingTokenSource{source: source, tokenFile: path}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				if _, err := refresher.Token(); err != nil {
					t.Errorf("concurrent Token: %v", err)
					return
				}
				if _, err := ParseTokenFile(path); err != nil {
					t.Errorf("concurrent read saw a non-parseable token file: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	assertNoTempResidue(t, dir)
}
