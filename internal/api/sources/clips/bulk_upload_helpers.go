package clips

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
)

// isLocalFolderAllowed returns true if abs lives under any of the configured
// storage base paths. This is a security check: the endpoint must not be
// usable to scan /etc or any other arbitrary directory just because the
// caller has the admin token.
//
// Both the input path and the allowed bases are resolved through symlinks
// (filepath.EvalSymlinks) so a symlink under an allowed base pointing to
// /etc cannot bypass the check.
func isLocalFolderAllowed(abs string, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	allowed := []string{
		cfg.Storage.MediaPath(),
		cfg.Storage.TempPath(),
		cfg.Storage.DataDir,
	}
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

// buildBulkClipID returns a deterministic-ish ID for a clip.
// Format: ytlocal_{HASH8}_{baseNameLowercasedShort} to keep IDs searchable and short.
// Non-ASCII characters (CJK, accented, emoji, …) are replaced with `_` so two
// non-ASCII clips with the same hash don't collide.
func buildBulkClipID(cand clipCandidate, fileHash string) string {
	const maxSuffix = 16
	clean := strings.Builder{}
	for _, r := range strings.ToLower(cand.Name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-' || r == '_':
			clean.WriteRune(r)
		case r == ' ' || r == '.' || r > 127:
			clean.WriteRune('_')
		}
		if clean.Len() >= maxSuffix {
			break
		}
	}
	suffix := clean.String()
	if suffix == "" {
		suffix = "clip"
	}
	return fmt.Sprintf("ytlocal_%s_%s", fileHash[:min(8, len(fileHash))], suffix)
}

// sanitiseDriveName removes characters that Drive rejects in filenames.
func sanitiseDriveName(name string) string {
	// Drive forbids: / \ : ? * " < > |
	bad := []rune{'/', '\\', ':', '?', '*', '"', '<', '>', '|'}
	out := name
	for _, c := range bad {
		out = strings.ReplaceAll(out, string(c), "_")
	}
	out = strings.TrimSpace(out)
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

func buildBulkDriveDescription(cand clipCandidate, fileHash string, payload jobservice.BulkUploadYouTubeClipsPayload) string {
	var b strings.Builder
	b.WriteString("Bulk-uploaded YouTube clip\n")
	b.WriteString("Source: " + payload.Source + "\n")
	if cand.Subdir != "" {
		b.WriteString("Subdir: " + cand.Subdir + "\n")
	}
	if cand.Manifest != nil {
		if v, ok := cand.Manifest["youtube_video_id"].(string); ok && v != "" {
			b.WriteString("YouTube ID: " + v + "\n")
		} else if v, ok := cand.Manifest["youtube_id"].(string); ok && v != "" {
			b.WriteString("YouTube ID: " + v + "\n")
		}
		if v, ok := cand.Manifest["youtube_url"].(string); ok && v != "" {
			b.WriteString("URL: " + v + "\n")
		} else if v, ok := cand.Manifest["url"].(string); ok && v != "" {
			b.WriteString("URL: " + v + "\n")
		}
		if v, ok := cand.Manifest["description"].(string); ok && v != "" {
			desc := v
			if len(desc) > 500 {
				desc = desc[:500] + "..."
			}
			b.WriteString("\nDescription:\n" + desc + "\n")
		}
	}
	b.WriteString("\nMD5: " + fileHash + "\n")
	b.WriteString("Imported via /api/media/bulk-upload-youtube-clips at " + time.Now().UTC().Format(time.RFC3339) + "\n")
	return b.String()
}

// deriveSearchText returns the best text we have for embedding (manifest description > transcript snippet > name).
// Manifest fields are capped to 5000 chars to prevent oversized search_text / embedding_json blobs.
func deriveSearchText(cand clipCandidate) string {
	const maxDesc = 5000
	capStr := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) > maxDesc {
			return s[:maxDesc]
		}
		return s
	}
	if cand.Manifest != nil {
		if v, ok := cand.Manifest["description"].(string); ok && strings.TrimSpace(v) != "" {
			return capStr(v)
		}
		if v, ok := cand.Manifest["search_text"].(string); ok && strings.TrimSpace(v) != "" {
			return capStr(v)
		}
	}
	if cand.Transcript != "" {
		// take first ~1000 chars as a coarse "topic" preview
		s := cand.Transcript
		if len(s) > 1000 {
			s = s[:1000]
		}
		return s
	}
	return cand.DisplayName()
}

func extractIntFromManifest(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}
