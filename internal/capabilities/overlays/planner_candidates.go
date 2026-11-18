package overlays

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// promptInstructionEchoes are the openings of the analysis instructions that
// the 2026-11-11 golden run's model copied into scene text. They are grounded
// in the contaminated script, so a text gate alone accepts them; if the phrase
// planner does too they become full-screen cards such as "Esaminare
// attentamente atti e decisioni ufficiali...". Keep the list literal: these
// are prompt echoes, not editorial sentences, and a broad heuristic such as
// "contains analisi" would also drop legitimate reporting.
var promptInstructionEchoes = []string{
	"esaminare attentamente atti e decisioni ufficiali",
	"le fonti giornalistiche spesso mescolano questi elementi",
	"le dichiarazioni televisive offrono uno spaccato mediatico",
	"le consulenze tecniche emergono nel dibattito",
	"l'obiettivo primario è presentare il quadro informativo",
	"è fondamentale procedere con cautela",
	"un singolo elemento non va isolato",
	"il contraddittorio consente alla difesa di esaminare gli atti",
	"i fatti accertati, le ipotesi investigative, le dichiarazioni e i punti contestati",
}

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
