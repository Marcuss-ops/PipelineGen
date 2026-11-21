package overlays

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func itemID(sceneID, kind, value string) string {
	raw := strings.ToLower(sceneID + "-" + kind + "-" + value)
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	const maxIDRunes = 110 // Leaves room for the stable suffix under subgroup_id's 128-rune limit.
	runes := []rune(id)
	if len(runes) <= maxIDRunes {
		return id
	}
	sum := digest.SHA256String(raw)
	return strings.TrimRight(string(runes[:maxIDRunes]), "-") + "-" + sum[:12]
}
