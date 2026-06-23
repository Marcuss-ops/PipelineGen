package health

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestDriveChecker_NoCredentials_Applicable verifies the Commit 2 fix:
// when Drive credentials are not configured, the check returns
// {ok: true, applicable: false} instead of failing. This prevents
// HTTP 503 from the health endpoint in deployments that opt out
// of the Drive capability (unit-test runs, batch CI runners, …).
func TestDriveChecker_NoCredentials_Applicable(t *testing.T) {
	c := NewDriveChecker("", "")
	res := c.CheckDrive(context.Background())

	ok, _ := res["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true when capability is opted out, got %v", res)
	}
	app, _ := res["applicable"].(bool)
	if app {
		t.Fatalf("expected applicable=false when credentials are empty, got %v", res)
	}
	note, _ := res["note"].(string)
	if note != "Drive credentials not configured" {
		t.Fatalf("expected 'Drive credentials not configured' note, got %q", note)
	}
	if _, hasError := res["error"]; hasError {
		t.Fatalf("expected no 'error' key when applicable=false, got %v", res)
	}
}

// TestDriveChecker_PartialCredentials_TokenEmpty verifies that the
// opt-out branch also fires when only the tokenPath is missing
// (deployment scenario: creds present but token not yet generated).
func TestDriveChecker_PartialCredentials_TokenEmpty(t *testing.T) {
	c := NewDriveChecker("/nonexistent/creds.json", "")
	res := c.CheckDrive(context.Background())

	ok, _ := res["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true, got %v", res)
	}
	app, _ := res["applicable"].(bool)
	if app {
		t.Fatalf("expected applicable=false, got %v", res)
	}
}

// TestDriveChecker_TokenFileUnreadable covers the failure path that
// fires when credsPath is provided but the tokenPath does not exist
// or is unreadable. Should report ok=false with a "token file not
// readable" error (NOT applicable=false — the capability IS wired
// but the local token file is missing).
func TestDriveChecker_TokenFileUnreadable(t *testing.T) {
	dir := t.TempDir()
	credsPath := "/nonexistent/creds.json"
	tokenPath := filepath.Join(dir, "missing-token.json")
	c := NewDriveChecker(credsPath, tokenPath)
	res := c.CheckDrive(context.Background())

	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false on unreadable token, got %v", res)
	}
	if app := res["applicable"]; app != nil {
		t.Fatalf("expected no applicable key on real failure, got %v", res)
	}
	if msg, _ := res["error"].(string); msg != "token file not readable" {
		t.Fatalf("expected 'token file not readable' error, got %q", msg)
	}
}

// TestDriveChecker_TokenMalformed covers the path where the token
// file is readable but its JSON does not contain a usable access_token
// field. Reports ok=false with the malformed-token error.
func TestDriveChecker_TokenMalformed(t *testing.T) {
	dir := t.TempDir()
	credsPath := "/nonexistent/creds.json"
	tokenPath := filepath.Join(dir, "token.json")
	if err := os.WriteFile(tokenPath, []byte(`{"refresh_token": "x", "scope": "drive"}`), 0644); err != nil {
		t.Fatalf("write token: %v", err)
	}
	c := NewDriveChecker(credsPath, tokenPath)
	res := c.CheckDrive(context.Background())

	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false on malformed token, got %v", res)
	}
	if msg, _ := res["error"].(string); msg != "token file invalid or missing access_token" {
		t.Fatalf("expected 'token file invalid or missing access_token' error, got %q", msg)
	}
}

// TestDriveChecker_HTTP403 covers the path where the token parses
// cleanly but Google Drive /about returns a non-OK status. Reports
// ok=false with the HTTP-status error.
func TestDriveChecker_HTTP403(t *testing.T) {
	dir := t.TempDir()
	credsPath := "/nonexistent/creds.json"
	tokenPath := filepath.Join(dir, "token.json")
	if err := os.WriteFile(tokenPath, []byte(`{"access_token": "fake-token-xyz"}`), 0644); err != nil {
		t.Fatalf("write token: %v", err)
	}
	// The DriveChecker uses a hard-coded Google URL, so we can't
	// easily stub it. This test verifies that the failure path is hit
	// by exercising the existing public endpoint (the test will see
	// a real non-200 OR a network error, both of which produce
	// ok=false + 'error'). If network is unreachable, the
	// 'Drive API unreachable' branch fires; if reachable with non-2xx,
	// the 'Drive API returned HTTP N' branch fires.
	c := NewDriveChecker(credsPath, tokenPath)
	res := c.CheckDrive(context.Background())

	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false on Drive token probe (real network), got %v", res)
	}
	if _, has := res["error"]; !has {
		t.Fatalf("expected 'error' key on Drive probe failure, got %v", res)
	}
}
