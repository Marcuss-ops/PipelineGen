package filesystem

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// SafeFolderName sanitises a name for use as a filesystem folder.
// Replaces unsafe characters with underscores and trims whitespace.
// Hyphens, underscores, and spaces are preserved (dots are not):
// the legacy pkg/pathutil contract that naming tests and drive/
// stockpipeline folder layouts are pinned against.
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

// ExtractStyleFromPath extracts the style segment from a relative image path.
// Paths follow the pattern: images/downloaded/{source}/{style}/{subStyle}/{genID}/{hash}.ext
// or: images/generated/{style}/{subStyle}/{genID}/{hash}.ext
func BuildTimestampedSlug(name, ext string) string {
	base := SafeFolderName(name)
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return fmt.Sprintf("%s-%s%s", time.Now().UTC().Format("20060102-150405"), base, ext)
}

func SanitizeSubfolderSegment(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty after trim")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("reserved segment")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("leading separator")
	}
	if strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("separator is not allowed")
	}
	if strings.Contains(name, "\x00") {
		return "", fmt.Errorf("NUL byte")
	}
	if len([]byte(name)) > 200 {
		return "", fmt.Errorf("exceeds 200 bytes")
	}
	clean := filepath.Clean(name)
	if clean != name {
		return "", fmt.Errorf("invalid subfolder segment %q", name)
	}
	for _, r := range name {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-_ ", r)) {
			return "", fmt.Errorf("invalid character in subfolder segment %q", name)
		}
	}
	return name, nil
}

func EnsureWithinDir(root, path string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return fmt.Errorf("root and path are required")
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if p != r && !strings.HasPrefix(p, r+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes root %q", path, root)
	}
	return nil
}

func ExtractStyleFromPath(pathRel string) string {
	normalized := strings.ReplaceAll(pathRel, "\\", "/")
	parts := strings.Split(normalized, "/")
	if len(parts) < 3 {
		return ""
	}
	switch parts[1] {
	case "downloaded":
		if len(parts) >= 4 {
			return parts[3]
		}
	case "generated":
		if len(parts) >= 3 {
			return parts[2]
		}
	}
	return ""
}
