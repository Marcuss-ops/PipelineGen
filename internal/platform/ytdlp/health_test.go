package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseVersionAge_DateShapedVersion(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		version string
		want    int
		wantOK  bool
	}{
		{"2026.09.03", 10, true},
		{"2026.08.19", 25, true},
		{"2026.08.19.123456", 25, true}, // nightly suffix ignored
		{" 2026.09.03 ", 10, true},      // trimmed
		{"2026.9.3", 10, true},          // non zero-padded
		{"2026.10.01", 0, true},         // future date clamps to 0
		{"2026.13.01", 0, false},        // invalid month
		{"2026.02.31", 0, false},        // invalid day
		{"unknown", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		age, ok := ParseVersionAge(tc.version, now)
		if ok != tc.wantOK || age != tc.want {
			t.Errorf("ParseVersionAge(%q) = (%d, %v), want (%d, %v)", tc.version, age, ok, tc.want, tc.wantOK)
		}
	}
}

func TestVersionIsStale(t *testing.T) {
	if !VersionIsStale(91, 90) {
		t.Error("91 days must be stale at threshold 90")
	}
	if VersionIsStale(90, 90) {
		t.Error("exactly at the threshold must not be stale")
	}
	if VersionIsStale(90, 0) {
		t.Error("a non-positive threshold must disable the check")
	}
}

func TestParsePOTProviders(t *testing.T) {
	real := "[debug] [pot] PO Token Providers: bgutil:http-1.3.1 (external), bgutil:script-node-1.3.1 (external, unavailable)\n"
	got := ParsePOTProviders(real)
	if len(got) != 2 || got[0] != "bgutil:http-1.3.1 (external)" || got[1] != "bgutil:script-node-1.3.1 (external, unavailable)" {
		t.Fatalf("ParsePOTProviders(real) = %#v", got)
	}

	if got := ParsePOTProviders("[debug] [pot] PO Token Providers: none\n"); got != nil {
		t.Fatalf("ParsePOTProviders(none) = %#v, want nil", got)
	}
	if got := ParsePOTProviders("no pot line here\n"); got != nil {
		t.Fatalf("ParsePOTProviders(no line) = %#v, want nil", got)
	}
	if got := ParsePOTProviders("[pot] PO Token Providers:\n"); got != nil {
		t.Fatalf("ParsePOTProviders(empty) = %#v, want nil", got)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ytdlp.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeVersion_ParsesTrimmedOutput(t *testing.T) {
	path := writeScript(t, "echo '2026.08.19'\n")
	got, err := ProbeVersion(context.Background(), path)
	if err != nil {
		t.Fatalf("ProbeVersion: %v", err)
	}
	if got != "2026.08.19" {
		t.Fatalf("ProbeVersion = %q, want 2026.08.19", got)
	}
}

func TestProbeVersion_EmptyOutputIsError(t *testing.T) {
	path := writeScript(t, "exit 0\n")
	if _, err := ProbeVersion(context.Background(), path); err == nil {
		t.Fatal("ProbeVersion must fail closed when the tool prints nothing")
	}
}

func TestProbePOTProviders_ParsesCombinedOutput(t *testing.T) {
	path := writeScript(t, "echo '[debug] [pot] PO Token Providers: bgutil:http-1.3.1 (external)' 1>&2\n")
	got, err := ProbePOTProviders(context.Background(), path, "https://example.test/v")
	if err != nil {
		t.Fatalf("ProbePOTProviders: %v", err)
	}
	if len(got) != 1 || got[0] != "bgutil:http-1.3.1 (external)" {
		t.Fatalf("ProbePOTProviders = %#v", got)
	}
}

func TestProbePOTProviders_NoneReturnsEmptySliceNoError(t *testing.T) {
	path := writeScript(t, "echo '[debug] [pot] PO Token Providers: none'\n")
	got, err := ProbePOTProviders(context.Background(), path, "https://example.test/v")
	if err != nil {
		t.Fatalf("ProbePOTProviders: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ProbePOTProviders(none) = %#v, want empty", got)
	}
}

func TestProbePOTProviders_BlankURLFallsBackToDefault(t *testing.T) {
	// The fake script ignores its args, but this pins that a blank probe URL
	// does not become an empty argv entry.
	path := writeScript(t, "echo '[pot] PO Token Providers: bgutil:http-1.3.1 (external)'\n")
	if _, err := ProbePOTProviders(context.Background(), path, "   "); err != nil {
		t.Fatalf("ProbePOTProviders(blank url): %v", err)
	}
}
