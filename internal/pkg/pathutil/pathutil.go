package pathutil

import "strings"

func SafeFolderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}

	var b strings.Builder
	lastSpace := false

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if b.Len() > 0 && !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			if r < 128 {
				if b.Len() > 0 && !lastSpace {
					b.WriteByte(' ')
					lastSpace = true
				}
			}
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return "untitled"
	}
	return out
}
