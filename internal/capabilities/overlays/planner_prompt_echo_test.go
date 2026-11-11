package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The 2026-11-11 golden v3.1 maps run put these exact sentences into
// text_phrase cards: they are instructions about how to analyse the case, not
// editorial content. The planner must drop them before lane/budget admission.
func TestRankedPhraseValidDropsPromptInstructionEchoes(t *testing.T) {
	echoes := []TimedAnnotation{
		{Text: "Esaminare attentamente atti e decisioni ufficiali permette di distinguere chiaramente tra fatti accertati e interpretazioni successive.", StartMs: 0, EndMs: 1000},
		{Text: "Le fonti giornalistiche spesso mescolano questi elementi, rendendo necessaria una lettura critica e metodica.", StartMs: 0, EndMs: 1000},
		{Text: "Le dichiarazioni televisive offrono uno spaccato mediatico degli eventi, presentando spesso ricostruzioni che meritano un'analisi attenta.", StartMs: 0, EndMs: 1000},
		{Text: "Le consulenze tecniche emergono nel dibattito come un elemento complesso, spesso oggetto di interpretazioni divergenti.", StartMs: 0, EndMs: 1000},
		{Text: "L'obiettivo primario è presentare il quadro informativo senza sostituire l'analisi legale con speculazioni non fondate.", StartMs: 0, EndMs: 1000},
		{Text: "È fondamentale procedere con cautela, ancorando ogni narrazione alle fonti documentate disponibili.", StartMs: 0, EndMs: 1000},
		{Text: "Un singolo elemento non va isolato dal contesto né descritto", StartMs: 0, EndMs: 1000},
		{Text: "Il contraddittorio consente alla difesa di esaminare gli atti", StartMs: 0, EndMs: 1000},
		{Text: "I fatti accertati, le ipotesi investigative, le dichiarazioni e i punti contestati devono restare distinti.", StartMs: 0, EndMs: 1000},
	}
	editorial := []TimedAnnotation{
		{Text: "morte di Chiara Poggi", StartMs: 1000, EndMs: 2000, Score: 0.9},
		{Text: "presunzione di innocenza", StartMs: 2000, EndMs: 3000, Score: 0.8},
	}

	got := rankedPhraseValid(append(echoes, editorial...), 0)
	require.Len(t, got, len(editorial))
	for _, candidate := range got {
		require.False(t, isPromptInstructionEcho(candidate.Text),
			"prompt instruction leaked into phrase candidates: %q", candidate.Text)
	}
}

func TestRankedValidDropsPromptInstructionEchoesFromQuotes(t *testing.T) {
	got := rankedValid([]TimedAnnotation{
		{Text: "È fondamentale procedere con cautela, ancorando ogni narrazione alle fonti documentate disponibili.", StartMs: 0, EndMs: 1000},
		{Text: "respinge le accuse", StartMs: 0, EndMs: 1000},
	}, 0)
	require.Len(t, got, 1)
	require.Equal(t, "respinge le accuse", got[0].Text)
}
