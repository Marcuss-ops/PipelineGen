package health

import (
	"context"
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
