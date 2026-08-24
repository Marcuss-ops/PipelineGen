package wiring

import "testing"

func TestRetrievedImageRightsPolicy(t *testing.T) {
	if got := retrievedImageRightsStatus("CC-BY-SA-4.0"); got != "verified" {
		t.Fatalf("licensed source status = %q, want verified", got)
	}
	if got := retrievedImageRightsStatus("Unknown"); got != "unknown_allowed" {
		t.Fatalf("unknown source status = %q, want unknown_allowed", got)
	}
	if got := retrievedImageRightsBasis("CC-BY-SA-4.0", "Wikipedia Contributors"); got == "" {
		t.Fatal("licensed source must preserve a rights basis")
	}
}
