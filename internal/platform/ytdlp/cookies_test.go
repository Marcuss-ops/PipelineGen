package ytdlp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validJar is the minimal jar yt-dlp accepts: the canonical header plus one
// full tab-separated row.
const validJar = NetscapeHeaderCanonical + "\n" +
	".youtube.com\tTRUE\t/\tTRUE\t1799999999\tSID\tsecret\n"

// cookieRow is a single well-formed Netscape row, reused by the rejection
// table so each case differs only in the property under test.
const cookieRow = ".youtube.com\tTRUE\t/\tTRUE\t1799999999\tSID\tsecret\n"

// TestValidateCookiesContent_AcceptsWhatYtDlpAccepts guards the mirror-fidelity
// contract: the validator must not be STRICTER than yt-dlp, because an
// over-strict check would refuse to boot a working deployment.
func TestValidateCookiesContent_AcceptsWhatYtDlpAccepts(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"canonical header", validJar},
		{"legacy header", strings.Replace(validJar, NetscapeHeaderCanonical, NetscapeHeaderLegacy, 1)},
		{"utf8 bom before header", "\ufeff" + validJar},
		{"crlf line endings", strings.ReplaceAll(validJar, "\n", "\r\n")},
		// An HttpOnly-only jar is legitimate and carries full rows; it must not
		// be mistaken for a header-only file.
		{"httpOnly rows only", NetscapeHeaderCanonical + "\n#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1799999999\tSID\tsecret\n"},
		{"blank and comment lines before a row", NetscapeHeaderCanonical + "\n\n# a comment\n" + cookieRow},
		{"multiple rows", NetscapeHeaderCanonical + "\n" + cookieRow + cookieRow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCookiesContent(tc.content); err != nil {
				t.Fatalf("expected the jar to be accepted, got %v", err)
			}
		})
	}
}

// TestValidateCookiesContent_RejectsWhatYtDlpRejects guards the other half: an
// under-strict check would let the original runtime 503 back in. Every case
// must both wrap ErrCookiesFileInvalid and explain the cause.
func TestValidateCookiesContent_RejectsWhatYtDlpRejects(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantMsg string
	}{
		// The production incident: a JSON browser export is readable, non-empty
		// and yet unusable — yt-dlp aborts with "does not look like a Netscape
		// format cookies file".
		{"json browser export", `[{"domain":".youtube.com","name":"SID","value":"x"}]`, "missing Netscape header"},
		{"empty", "", "file is empty"},
		{"whitespace only", "   \n\n\t\n", "file is empty"},
		{"header only, no rows", NetscapeHeaderCanonical + "\n", "no cookie rows"},
		{"header followed only by a comment", NetscapeHeaderCanonical + "\n# just a comment\n", "no cookie rows"},
		{"truncated download", ".youtube.com\tTRUE\t/\tTRUE\t1799", "missing Netscape header"},
		// Only the FIRST non-empty line is inspected, exactly like yt-dlp: a
		// header further down does not make the jar loadable.
		{"header after a row", cookieRow + NetscapeHeaderCanonical + "\n", "missing Netscape header"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCookiesContent(tc.content)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !errors.Is(err, ErrCookiesFileInvalid) {
				t.Errorf("error must wrap ErrCookiesFileInvalid so callers can branch, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q must explain the cause (%q)", err.Error(), tc.wantMsg)
			}
		})
	}
}

// TestValidateCookiesFile covers the filesystem half. The unconfigured case is
// load-bearing: cookies are optional, and with no path the command builder
// omits --cookies entirely, so an empty setting must never be reported as a
// fault (that would break every cookie-free deployment).
func TestValidateCookiesFile(t *testing.T) {
	dir := t.TempDir()

	validPath := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(validPath, []byte(validJar), 0o600); err != nil {
		t.Fatal(err)
	}
	// Readable, non-empty, wrong format — the exact production shape.
	jsonPath := filepath.Join(dir, "cookies-export.json")
	if err := os.WriteFile(jsonPath, []byte(`{"cookies":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("unconfigured jar is a supported mode", func(t *testing.T) {
		for _, path := range []string{"", "   ", "\t\n"} {
			if err := ValidateCookiesFile(path); err != nil {
				t.Fatalf("path %q must be accepted (cookies are optional), got %v", path, err)
			}
		}
	})

	t.Run("valid jar", func(t *testing.T) {
		if err := ValidateCookiesFile(validPath); err != nil {
			t.Fatalf("expected acceptance, got %v", err)
		}
	})

	t.Run("readable but not netscape is rejected and names the path", func(t *testing.T) {
		err := ValidateCookiesFile(jsonPath)
		if !errors.Is(err, ErrCookiesFileInvalid) {
			t.Fatalf("expected ErrCookiesFileInvalid, got %v", err)
		}
		if !strings.Contains(err.Error(), jsonPath) {
			t.Errorf("error must name the offending path so the operator knows which jar to fix, got %q", err.Error())
		}
	})

	t.Run("missing file", func(t *testing.T) {
		err := ValidateCookiesFile(filepath.Join(dir, "absent.txt"))
		if !errors.Is(err, ErrCookiesFileInvalid) {
			t.Fatalf("expected ErrCookiesFileInvalid, got %v", err)
		}
		if !strings.Contains(err.Error(), "cannot be read") {
			t.Errorf("error must say the jar cannot be read, got %q", err.Error())
		}
	})
}
