package stock

import (
	"net"
	"net/url"
	"path"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/primitives"
)

// isValidURL validates that u is an absolute https URL with a
// resolvable hostname and rejects private / loopback IP addresses
// (RFC1918 SSRF mitigation). file:// URLs are accepted for local
// hermetic stock runs; the path portion is validated via isSafePath.
// ftp://, gopher://, jar: are rejected via the scheme check.
//
// godlike/06 SSOT: this is the single source of truth for URL
// validation at the HTTP boundary. The orchestrator's downstream
// path uses different rules (yt-dlp accepts http on some sources)
// but those are application-layer concerns.
//
// PR-DOMAIN-PRIMITIVES-NOMINAL (July 2026): signature accepts
// primitives.URL (canonical nominal type) so the compiler catches
// accidental swaps with other raw-string identifiers. The internal
// lexical/HTTP validation is unchanged — the primitive is a pure
// view of the underlying string. Callers pass primitives.NewURL(s)
// at the HTTP boundary.
func isValidURL(u primitives.URL) bool {
	if u.IsEmpty() {
		return false
	}
	raw := u.String()
	// Length cap — defense in depth against URL-flood DoS (10MB URLs).
	if len(raw) > MaxURLLength {
		return false
	}
	// Null-byte rejection — some libraries truncate at NUL.
	if strings.ContainsRune(raw, '\x00') {
		return false
	}
	// file:// scheme: accept for local hermetic stock runs.
	// Validate the path portion for traversal, null bytes, and backslashes
	// with inline checks (file paths are absolute so isSafePath can't be used).
	if strings.HasPrefix(raw, "file://") {
		filePath := strings.TrimPrefix(raw, "file://")
		// Null-byte rejection
		if strings.ContainsRune(filePath, '\x00') {
			return false
		}
		// Backslash escape — reject (mirrors isSafePath)
		if strings.Contains(filePath, "\\") {
			return false
		}
		// Path traversal rejection — check raw string BEFORE path.Clean
		// because Clean legitimately resolves /../ sequences for absolute paths.
		for _, part := range strings.Split(filePath, "/") {
			if part == ".." {
				return false
			}
		}
		return true
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	// Reject when Hostname is a private / loopback IP literal
	// (numeric IPv4 or IPv6 literal). Hostnames that resolve to
	// private IPs at DNS-time are out of scope for the HTTP-layer
	// validator — call the operator-side DNS pin at the runner level.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}

// isSafePath rejects path-traversal attempts (".." sequences and
// backslash escapes), absolute paths, and null-byte injections on
// folder fields. True for the empty string and for any value whose
// canonical path stays within the configured root.
//
// godlike/06 SSOT: single helper used across subfolder / folder_name /
// drive_folder_id / folder_id fields.
func isSafePath(p string) bool {
	if p == "" {
		return true
	}
	// Backslash escape — reject any path that contains "\".
	if strings.Contains(p, `\`) {
		return false
	}
	// Null-byte rejection — defense in depth against libtruncation bypass.
	if strings.ContainsRune(p, '\x00') {
		return false
	}
	// Absolute-path rejection (e.g. /etc/passwd). Windows drive letters
	// like "C:\foo" are caught by the backslash check above.
	if strings.HasPrefix(p, "/") {
		return false
	}
	clean := path.Clean(p)
	if clean == ".." {
		return false
	}
	if strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return false
	}
	return true
}
