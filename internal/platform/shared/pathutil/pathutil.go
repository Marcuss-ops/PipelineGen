// Package pathutil provides filesystem path and folder naming helpers.
package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// SafeFolderName sanitises a name for use as a filesystem folder.
// Replaces unsafe characters with underscores and trims whitespace.
func SafeFolderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := strings.TrimSpace(b.String())
	if result == "" {
		return "untitled"
	}
	return result
}

// BuildTimestampedSlug creates a slug from name with a timestamp prefix.
func BuildTimestampedSlug(name, ext string) string {
	base := SafeFolderName(name)
	ts := time.Now().UTC().Format("20060102-150405")
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return fmt.Sprintf("%s-%s%s", ts, base, ext)
}

// ExtractStyleFromPath extracts the style name from a relative path.
// Example: "Cinematic/Italy/scene_01.jpg" → "Cinematic"
func ExtractStyleFromPath(relPath string) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	parts := strings.SplitN(relPath, "/", 2)
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return ""
}

// SanitizeSubfolderSegment validates and sanitises a single-segment
// subfolder name for use under a parent root via filepath.Join.
//
// PR-VO-A4 (June 2026, path-traversal fix): the previous voiceover
// code path joined `req.Destination.SubfolderName` directly under
// `s.outputDir` without any per-segment validation, opening up:
//
//   - absolute paths via leading "/" (becomes `output_dir/abs` and
//     depending on Join behaviour either truncates or escapes);
//   - parent-traversal via ".." or "../foo" (escapes the root or
//     targets a sibling segment via join);
//   - multi-segment via embedded "/" or "\" (silently nests inside
//     an unintended sub-root that may not even exist);
//   - reserved reserved names "." and ".." (current/parent dir).
//
// This helper rejects all of those at the segment boundary so the
// caller can safely use the result as the second arg to
// filepath.Join(root, segment). Defenders of the helper SHOULD also
// call EnsureWithinDir below after the join as a defense-in-depth
// check (the helper protects the segment; EnsureWithinDir protects
// the join result — both layers are load-bearing).
//
// Returns the trimmed segment unchanged when it passes the checks.
// Permitted content: any unicode letter/digit, period, hyphen,
// underscore, space — matching the rules elsewhere in this package.
// Length is capped at 200 runes to prevent absurdly long names in
// OS error messages (most filesystems cap at 255 NAME_MAX bytes;
// 200 leaves margin for the parent root prefix). NUL bytes are
// rejected outright because they cannot appear in OS paths.
func SanitizeSubfolderSegment(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("subfolder_name is empty after trim")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("subfolder_name %q is reserved (current/parent dir)", name)
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("subfolder_name %q has leading separator (path traversal)", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("subfolder_name %q contains a separator (multi-segment paths not allowed here)", name)
	}
	if strings.Contains(name, "\x00") {
		return "", fmt.Errorf("subfolder_name contains NUL byte")
	}
	// Final defense: filepath.Clean collapses "foo/../bar" to "bar";
	// on a single-segment input that has already passed the checks
	// above, Clean is idempotent — but if any prefix makes it through
	// the static checks (e.g. a future OS with a different separator),
	// clean suppresses the residual "..".
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("subfolder_name %q resolves to reserved dir after Clean", name)
	}
	if cleaned != name {
		// Clean stripped something we did not expect on a string that
		// already passed the static checks; refuse for transparency.
		return "", fmt.Errorf("subfolder_name %q was modified by filepath.Clean to %q (unsupported character)", name, cleaned)
	}
	// Byte-length cap (not rune-length): most POSIX filesystems cap
	// NAME_MAX at 255 BYTES (ext4, btrfs, HFS+, APFS), so a 200-byte
	// ceiling leaves headroom for the parent root prefix while still
	// staying well under the OS limit on both ASCII and UTF-8 input.
	// Capping in runes would let 200 CJK characters (600 bytes)
	// slip through and fail later at os.MkdirAll with ENAMETOOLONG —
	// worse UX than failing at the request boundary.
	if len(cleaned) > 200 {
		return "", fmt.Errorf("subfolder_name %q exceeds 200 bytes", name)
	}
	return cleaned, nil
}

// EnsureWithinDir returns nil when path is the root itself OR when
// path lies strictly under root. It uses filepath.Rel to compute the
// canonical relative form of path under root; if the result starts
// with ".." or is itself absolute (e.g. on Windows an absolute target
// is "C:\\foo" relative to a Unix-style root), the path is treated
// as escaped.
//
// Use this AFTER filepath.Join(root, segment) as defense in depth —
// even when the segment was sanitized at construction time. A future
// refactor that re-introduces the segment through an unsanitized
// channel (e.g. a JSON-driven config that bypasses request bindings)
// will still be caught here because the join result is the canonical
// observable.
//
// Errors are operator-triage friendly: they carry the rejected path,
// the expected root, and the computed rel — enough to reproduce.
func EnsureWithinDir(root, path string) error {
	if root == "" {
		return fmt.Errorf("EnsureWithinDir: root is empty")
	}
	if path == "" {
		return fmt.Errorf("EnsureWithinDir: path is empty")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("EnsureWithinDir: filepath.Rel(%q, %q) error: %w", root, path, err)
	}
	// Treat any rel that points above root (a single ".." or any ".."-
	// prefixed segment) as escape. The two checks share the canonical
	// "escapes root" wording so log-audit tools can grep for one
	// substring instead of two.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("EnsureWithinDir: path %q escapes root %q (rel=%q)", path, root, rel)
	}
	if filepath.IsAbs(rel) {
		// filepath.Rel returns absolute on Windows when path is on a
		// different volume than root. Treat as escape.
		return fmt.Errorf("EnsureWithinDir: path %q escapes root %q across volumes (rel=%q)", path, root, rel)
	}
	return nil
}
