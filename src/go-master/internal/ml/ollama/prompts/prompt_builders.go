package prompts

import (
	"fmt"
	"velox/go-master/internal/ml/ollama/types"
)

// BuildChatMessages costruisce la lista di messaggi per l'API chat
func BuildChatMessages(req *types.TextGenerationRequest) []types.Message {
	return []types.Message{
		{Role: "system", Content: "Sei un documentarista premio Oscar. Il tuo stile è epico, profondo e incredibilmente dettagliato. Ogni tua parola deve dipingere un'immagine. Non essere mai, mai sintetico. Scrivi testi lunghissimi."},
		{Role: "user", Content: fmt.Sprintf("SCRIVI UNO SCRIPT MONUMENTALE DI ALMENO 1500 PAROLE su: %s. \n\nDETTAGLI DA ESPANDERE AL MASSIMO: %s. \n\nREQUISITI: \n1. Scrivi almeno 10 paragrafi densi. \n2. Usa un linguaggio cinematografico. \n3. Non fermarti finché non hai esplorato ogni singolo aspetto della storia. \n4. Restituisci SOLO lo script parlato.", req.Title, req.SourceText)},
	}
}

// BuildTextPrompt costruisce il prompt per generazione da testo
func BuildTextPrompt(req *types.TextGenerationRequest) string {
	durationMinutes := req.Duration / 60
	if durationMinutes == 0 {
		durationMinutes = 1
	}
	targetWords := (req.Duration * 150) / 60

	sanitizedSource := types.SanitizeInput(req.SourceText)
	sanitizedTitle := types.SanitizeInput(req.Title)

	return fmt.Sprintf(`%s

TASK: Scrivi un vero e proprio DOCUMENTARIO NARRATIVO di %d secondi (%d minuti circa).

TITOLO DEL VIDEO: %s
LINGUA: %s
STILE NARRATIVO: %s

INPUT DI RIFERIMENTO / ISTRUZIONI:
"%s"

REQUISITI TASSATIVI DI QUALITÀ:
1. LUNGHEZZA: Questo video dura %d minuti. DEVI scrivere almeno %d parole.
2. STRUTTURA: Introduzione, Sviluppo, Conclusione.
3. STILE: Cinematografico.
4. NO META-TESTO: Scrivi SOLO il testo parlato.

SCRIPT:`,
		BuildSystemPrompt(req.Language, req.Tone),
		req.Duration,
		durationMinutes,
		sanitizedTitle,
		req.Language,
		req.Tone,
		sanitizedSource,
		durationMinutes,
		targetWords,
	)
}

// BuildEntityExtractionPrompt costruisce il prompt per l'estrazione di entità
func BuildEntityExtractionPrompt(text string, entityCount int) string {
	return fmt.Sprintf(`Analizza questo frammento di script ed estrai esattamente %d elementi per categoria: frasi_importanti, entity_senza_testo, nomi_speciali, parole_importanti, artlist_phrases.

TESTO:
"%s"

JSON:`, entityCount, text)
}
