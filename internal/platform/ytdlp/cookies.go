// Package ytdlp — cookies.go
//
// Cookie-jar format validation.
//
// yt-dlp accepts exactly one cookie-jar shape: the Netscape/Mozilla
// `cookies.txt` format (a first line starting with
// `# Netscape HTTP Cookie File`, then one tab-separated row per cookie). Any
// other shape — a JSON browser export, a truncated download, an empty file —
// makes yt-dlp abort the whole invocation with
//
//	ERROR: '<path>' does not look like a Netscape format cookies file
//
// Because the jar is passed to every YouTube-leg invocation (search, metadata,
// download, subtitles), a malformed jar does not fail loudly where it is wrong:
// it fails as an unrelated-looking 5xx on whichever endpoint happens to touch
// YouTube first (observed: 503 on GET /api/clips/search and on every stock
// acquisition). The operator then has to reconstruct the cause from a
// subprocess stderr line.
//
// This file owns the two probes that make the misconfiguration impossible to
// hide: ValidateCookiesFile is called by the composition root (fail loud, at
// boot) and by the diagnostics payload, so the verdict is named where it is
// actionable instead of surfacing as a runtime 503.
//
// The check deliberately MIRRORS yt-dlp's own acceptance rule rather than
// approximating it: an over-strict validator would block a working deployment,
// an under-strict one would let the original 503 back in.
package ytdlp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// NetscapeHeaderCanonical is the canonical jar header yt-dlp requires.
	NetscapeHeaderCanonical = "# Netscape HTTP Cookie File"

	// NetscapeHeaderLegacy is the older header spelling yt-dlp also accepts.
	NetscapeHeaderLegacy = "# HTTP Cookie File"
)

// ErrCookiesFileInvalid is wrapped by every validation failure, so callers can
// branch on the class (errors.Is) without matching message text.
var ErrCookiesFileInvalid = errors.New("yt-dlp cookies file invalid")

const (
	// maxCookiesFileBytes bounds how much of the jar is read. A real jar is a
	// few kilobytes; the cap keeps a wrong path (a device node, a huge log that
	// happens to be named cookies.txt) from being pulled into memory.
	maxCookiesFileBytes = 1 << 20

	// cookieRowFields is the minimum number of tab-separated fields in a
	// Netscape row: domain, include-subdomains flag, path, secure, expiry,
	// name, value.
	cookieRowFields = 6
)

// ValidateCookiesFile reports whether path is a jar yt-dlp will accept.
//
// Semantics, each chosen so a legitimate deployment is never blocked:
//
//   - "" (or whitespace) → nil. Cookies are OPTIONAL: with no path the command
//     builder omits --cookies entirely, so an unconfigured jar is a supported
//     mode rather than a fault.
//   - missing / unreadable → error.
//   - empty or whitespace-only → error.
//   - first non-empty line is not a Netscape header → error. A UTF-8 BOM in
//     front of the header is tolerated and stripped: it is a common
//     hand-export artefact and yt-dlp rejects it, so reporting it as a format
//     fault without saying so would be a trap.
//   - header present but no parseable cookie row → error. yt-dlp would load an
//     empty jar and then fail later with a much less obvious symptom.
func ValidateCookiesFile(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}

	f, err := os.Open(trimmed)
	if err != nil {
		return fmt.Errorf("%w: %s: cannot be read: %v", ErrCookiesFileInvalid, trimmed, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxCookiesFileBytes))
	if err != nil {
		return fmt.Errorf("%w: %s: cannot be read: %v", ErrCookiesFileInvalid, trimmed, err)
	}

	if err := ValidateCookiesContent(string(data)); err != nil {
		return fmt.Errorf("%s: %w", trimmed, err)
	}
	return nil
}

// ValidateCookiesContent applies the format rule to the jar's bytes. It is the
// pure half of ValidateCookiesFile (no filesystem), so the rule is unit-testable
// exhaustively.
func ValidateCookiesContent(content string) error {
	content = stripCookieBOM(content)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: file is empty", ErrCookiesFileInvalid)
	}

	lines := strings.Split(content, "\n")

	// Only the FIRST non-empty line is inspected, exactly like yt-dlp: a header
	// further down the file does not make the jar loadable.
	headerIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isNetscapeHeader(trimmed) {
			headerIdx = i
		}
		break
	}
	if headerIdx < 0 {
		return fmt.Errorf(
			"%w: missing Netscape header (first line must start with %q or %q — a JSON browser export is NOT a valid jar)",
			ErrCookiesFileInvalid, NetscapeHeaderCanonical, NetscapeHeaderLegacy,
		)
	}

	if countNetscapeRows(lines[headerIdx+1:]) == 0 {
		return fmt.Errorf(
			"%w: Netscape header present but the jar carries no cookie rows (yt-dlp would load it empty)",
			ErrCookiesFileInvalid,
		)
	}
	return nil
}

// isNetscapeHeader reports whether line is one of the two header spellings
// yt-dlp accepts. Callers pass an already-trimmed line.
func isNetscapeHeader(line string) bool {
	return strings.HasPrefix(line, NetscapeHeaderCanonical) ||
		strings.HasPrefix(line, NetscapeHeaderLegacy)
}

// countNetscapeRows counts tab-separated cookie rows. Blank lines and plain
// comments fall out of the field test on their own, so they need no special
// case — and that matters: a `#HttpOnly_<domain>` row starts with '#' but
// carries a full row, so excluding every '#'-prefixed line would wrongly count
// an HttpOnly-only jar as empty.
func countNetscapeRows(lines []string) int {
	rows := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Count(trimmed, "\t") >= cookieRowFields-1 {
			rows++
		}
	}
	return rows
}

// stripCookieBOM removes a leading UTF-8 BOM, which yt-dlp's header comparison
// rejects and which hand-edited/Windows-exported jars carry.
func stripCookieBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}
