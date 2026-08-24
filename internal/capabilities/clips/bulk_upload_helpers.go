// Package clips (bulk_upload_helpers) — pure helper functions extracted from
// internal/api/assets/clips/bulk_upload_helpers.go during Wave 14 PR2 slice 2
// (June 2026). The helpers previously lived in the API transport layer and
// `isLocalFolderAllowed` imported `internal/platform/config` — a direct
// infrastructure import from the transport layer in violation of AGENTS.md
// Pattern 8. After extraction: every function is a pure leaf helper with zero
// infrastructure imports.
//
// The functions that previously took a clipCandidate struct now take
// individual fields so callers in different packages (api/assets/clips
// vs application/clips) can pass their own local types without
// a shared DTO.
package clips

import (
	"fmt"
	"path/filepath"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// IsLocalFolderAllowed returns true if abs lives under any of the configured
// storage base paths. This is a security check: the endpoint must not be
// usable to scan /etc or any other arbitrary directory just because the
// caller has the admin token.
//
// Both the input path and the allowed bases are resolved through symlinks
// (filepath.EvalSymlinks) so a symlink under an allowed base pointing to
// /etc cannot bypass the check.
//
// Wave 14 PR2 slice 2: takes the three path strings directly instead of
// *config.Config, keeping this package free of infrastructure imports.
func IsLocalFolderAllowed(abs, mediaPath, tempPath, dataDir string) bool {
	allowed := []string{mediaPath, tempPath, dataDir}
	resolve := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return ""
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return p
	}
	resolvedAbs := resolve(abs)
	if resolvedAbs == "" {
		return false
	}
	for _, base := range allowed {
		base = resolve(base)
		if base == "" {
			continue
		}
		if resolvedAbs == base {
			return true
		}
		if strings.HasPrefix(resolvedAbs, base+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// BuildBulkClipID returns a deterministic-ish ID for a clip from its
// display name, raw name, and file hash.
// Format: ytlocal_{HASH8}_{slug}.
func BuildBulkClipID(displayName, rawName, hash string) string {
	short := hash
	if len(short) > 8 {
		short = short[:8]
	}
	slug := SanitiseDriveName(displayName)
	if slug == "" {
		slug = SanitiseDriveName(rawName)
	}
	return "ytlocal_" + short + "_" + slug
}

// SanitiseDriveName removes characters that Drive rejects in filenames.
func SanitiseDriveName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// BuildBulkDriveDescription builds the Drive file description for a
// bulk-uploaded clip from its individual fields.
func BuildBulkDriveDescription(
	displayName, subdir, hash string,
	manifest map[string]any,
	payload appjobs.BulkUploadYouTubeClipsPayload,
) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Name: %s", displayName))
	if subdir != "" {
		parts = append(parts, fmt.Sprintf("Subdir: %s", subdir))
	}
	parts = append(parts, fmt.Sprintf("Hash: %s", hash))
	if payload.Source != "" {
		parts = append(parts, fmt.Sprintf("Source: %s", payload.Source))
	}
	if payload.Category != "" {
		parts = append(parts, fmt.Sprintf("Category: %s", payload.Category))
	}
	if manifest != nil {
		if t, ok := manifest["title"].(string); ok && t != "" {
			parts = append(parts, fmt.Sprintf("Title: %s", t))
		}
		if u, ok := manifest["youtube_url"].(string); ok && u != "" {
			parts = append(parts, fmt.Sprintf("URL: %s", u))
		}
		if yid, ok := manifest["youtube_video_id"].(string); ok && yid != "" {
			parts = append(parts, fmt.Sprintf("YouTube ID: %s", yid))
		}
	}
	return strings.Join(parts, "\n")
}

// DeriveSearchText returns the best text we have for embedding from the
// clip's individual fields.
func DeriveSearchText(displayName, name, subdir string, manifest map[string]any) string {
	bits := []string{displayName, name}
	if subdir != "" {
		bits = append(bits, subdir)
	}
	if manifest != nil {
		if t, ok := manifest["title"].(string); ok && t != "" {
			bits = append(bits, t)
		}
		if desc, ok := manifest["description"].(string); ok && desc != "" {
			bits = append(bits, desc)
		}
	}
	return strings.TrimSpace(strings.Join(bits, " "))
}

// ExtractIntFromManifest extracts an integer value from a manifest map.
func ExtractIntFromManifest(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return 0
}

// ── Package-local wrappers kept for the application-layer worker ──────
// These thin wrappers adapt the new field-based signatures back to the
// clipCandidate struct style used by bulk_upload_worker.go. They live
// here (not in the worker file) so there is exactly one definition of
// each helper. Once the worker migrates to the field-based signatures
// directly, these wrappers can be deleted.

// clipCandidate is the internal per-file candidate shape for the
// bulk-upload scanning logic. Defined here (single owner) and
// consumed by bulk_upload_worker.go.
type clipCandidate struct {
	Name       string
	LocalPath  string
	Subdir     string
	Manifest   map[string]any
	Transcript string
}

// DisplayName returns the readable clip name (manifest title preferred).
func (c clipCandidate) DisplayName() string {
	if c.Manifest != nil {
		if t, ok := c.Manifest["title"].(string); ok && t != "" {
			return t
		}
		if t, ok := c.Manifest["name"].(string); ok && t != "" {
			return t
		}
	}
	return c.Name
}

func buildBulkClipID(cand clipCandidate, hash string) string {
	return BuildBulkClipID(cand.DisplayName(), cand.Name, hash)
}

func sanitiseDriveName(s string) string {
	return SanitiseDriveName(s)
}

func buildBulkDriveDescription(cand clipCandidate, hash string, payload appjobs.BulkUploadYouTubeClipsPayload) string {
	return BuildBulkDriveDescription(cand.DisplayName(), cand.Subdir, hash, cand.Manifest, payload)
}

func deriveSearchText(cand clipCandidate) string {
	return DeriveSearchText(cand.DisplayName(), cand.Name, cand.Subdir, cand.Manifest)
}

func extractIntFromManifest(m map[string]any, key string) int {
	return ExtractIntFromManifest(m, key)
}

// Verify that exported helpers are referenced.
var (
	_ = BuildBulkClipID
	_ = SanitiseDriveName
	_ = BuildBulkDriveDescription
	_ = DeriveSearchText
	_ = ExtractIntFromManifest
	_ = IsLocalFolderAllowed
)
