// cmd/admin/internal/drive/folder_path.go — shared Drive path helpers.
//
// "/" is the canonical segment separator for operator-supplied folder
// paths. Both drive-create-folder (`--name "job/it"`) and any future
// path-taking command resolve their input through here so the parsing
// rules stay in one place.
package drive

import "strings"

// splitFolderPath splits a folder name or nested path into canonical
// non-empty segments. "/" is the path separator; surrounding whitespace
// and empty segments ("a//b", "a/") are dropped so callers get a clean
// EnsureFolderPath input. Returns nil when the input carries no segment.
func splitFolderPath(name string) []string {
	raw := strings.Split(name, "/")
	segments := make([]string, 0, len(raw))
	for _, seg := range raw {
		if trimmed := strings.TrimSpace(seg); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
}

// joinFolderPath renders segments back as a "/"-joined path for display.
// The inverse of splitFolderPath for canonical (non-empty) segments.
func joinFolderPath(segments []string) string {
	return strings.Join(segments, "/")
}
