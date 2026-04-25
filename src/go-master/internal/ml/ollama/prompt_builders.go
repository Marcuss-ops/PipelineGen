package ollama

import "fmt"

// buildChatMessages costruisce la lista di messaggi per l'API chat
func buildChatMessages(req *TextGenerationRequest) []Message {
	return []Message{
		{Role: "system", Content: "Sei un documentarista premio Oscar. Il tuo stile è epico, profondo e incredibilmente dettagliato. Ogni tua parola deve dipingere un'immagine. Non essere mai, mai sintetico. Scrivi testi lunghissimi."},
		{Role: "user", Content: fmt.Sprintf("SCRIVI UNO SCRIPT MONUMENTALE DI ALMENO 1500 PAROLE su: %s. \n\nDETTAGLI DA ESPANDERE AL MASSIMO: %s. \n\nREQUISITI: \n1. Scrivi almeno 10 paragrafi densi. \n2. Usa un linguaggio cinematografico. \n3. Non fermarti finché non hai esplorato ogni singolo aspetto della storia. \n4. Restituisci SOLO lo script parlato.", req.Title, req.SourceText)},
	}
}

// buildTextPrompt costruisce il prompt per generazione da testo
func buildTextPrompt(req *TextGenerationRequest) string {
	durationMinutes := req.Duration / 60
	if durationMinutes == 0 {
		durationMinutes = 1
	}
	targetWords := (req.Duration * 150) / 60 // ~150 parole al minuto per un ritmo narrativo buono

	// Sanitize source text to prevent prompt injection
	sanitizedSource := sanitizeInput(req.SourceText)
	sanitizedTitle := sanitizeInput(req.Title)

	return fmt.Sprintf(`%s

TASK: Scrivi un vero e proprio DOCUMENTARIO NARRATIVO di %d secondi (%d minuti circa).

TITOLO DEL VIDEO: %s
LINGUA: %s
STILE NARRATIVO: %s

INPUT DI RIFERIMENTO / ISTRUZIONI:
"%s"

REQUISITI TASSATIVI DI QUALITÀ:
1. LUNGHEZZA: Questo video dura %d minuti. DEVI scrivere almeno %d parole. Se l'input è breve, USI LA TUA CONOSCENZA per espandere il racconto con fatti, aneddoti e dettagli storici.
2. STRUTTURA: 
   - [00:00] Gancio iniziale esplosivo.
   - [00:15] Introduzione al tema.
   - [00:45] Sviluppo approfondito (diviso in blocchi narrativi).
   - [01:45] Conclusione e riflessione finale.
3. STILE: Scrivi per l'orecchio, non per l'occhio. Usa frasi che fluiscono bene.
4. DETTAGLI: Evita descrizioni piatte. Invece di dire semplicemente "è successo X", arricchisci la narrazione con dettagli sensoriali, contesto storico o descrizioni cinematografiche che trasportino l'ascoltatore nella scena.
5. NO META-TESTO: Scrivi SOLO il testo parlato. Non scrivere "Introduzione:", "Musica:", ecc.

NON LIMITARTI A RISPONDERE ALLA DOMANDA. SCRIVI UN RACCONTO EPICO ED ESAUSTIVO.

SCRIPT:`,
		getSystemPrompt(req.Language, req.Tone),
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

// buildYouTubePrompt costruisce il prompt per generazione da YouTube
func buildYouTubePrompt(transcript, title, language, tone string, duration int) string {
	durationMinutes := float64(duration) / 60.0

	// Sanitize inputs to prevent prompt injection
	sanitizedTranscript := sanitizeInput(transcript)
	sanitizedTitle := sanitizeInput(title)

	return fmt.Sprintf(`%s

TASK: Scrivi uno script per un video di %d secondi (%.1f minuti circa) basato sulla trascrizione YouTube fornita.

TITOLO: %s
LINGUA: %s
TONO: %s

TRASCRIZIONE YOUTUBE:
%s

ISTRUZIONI:
- Usa la trascrizione come riferimento per i fatti e le informazioni
- Riscrivi in stile narrativo coinvolgente
- Non copiare parola per parola, ri elabora il contenuto
- Struttura: Introduzione, Contenuto principale, Conclusione
- Mantieni le informazioni chiave ma rendi il racconto fluido

SCRIPT:`,
		getSystemPrompt(language, tone),
		duration,
		durationMinutes,
		sanitizedTitle,
		language,
		tone,
		sanitizedTranscript,
	)
}

// buildRegeneratePrompt costruisce il prompt per rigenerazione
func buildRegeneratePrompt(req *RegenerationRequest) string {
	// Sanitize inputs to prevent prompt injection
	sanitizedScript := sanitizeInput(req.OriginalScript)
	sanitizedTitle := sanitizeInput(req.Title)

	return fmt.Sprintf(`%s

Il seguente script è stato generato ma necessita di essere migliorato/riscritto:

TITOLO: %s

%s

Per favore, riscrivi lo script mantenendo lo stesso tema ma migliorando:
- Chiarezza e scorrevolezza
- Coinvolgimento del pubblico
- Struttura narrativa

NUOVO SCRIPT:`,
		getSystemPrompt(req.Language, req.Tone),
		sanitizedTitle,
		sanitizedScript,
	)
}

// buildEntityExtractionPrompt costruisce il prompt per l'estrazione di entità da un segmento
func buildEntityExtractionPrompt(text string, entityCount int) string {
	return fmt.Sprintf(`Analizza attentamente questo frammento di script video e agisci come un esperto di metadata per YouTube.

TESTO DA ANALIZZARE:
"%s"

TASK: Estrai esattamente %d elementi di altissima qualità per ognuna di queste categorie.

⚠️ REGOLA DI LINGUA TASSATIVA:
Usa ESCLUSIVAMENTE la stessa lingua del testo fornito (se il testo è in Inglese, rispondi in Inglese. Se in Italiano, rispondi in Italiano). NON tradurre nulla.

1. frasi_importanti: Le citazioni più potenti dallo script (Max 100 caratteri).
2. entity_senza_testo: Un oggetto JSON dove le CHIAVI sono i nomi di persone o oggetti visivi e i VALORI sono link placeholder.
3. nomi_speciali: Nomi propri presenti nel testo.
4. parole_importanti: Keyword chiave.
5. artlist_phrases: Un oggetto JSON dove ogni CHIAVE è una frase di almeno 5 parole COPIATA IDENTICA dallo script e il VALORE è una lista di keyword inglesi per cercare quel video.

❌ VIETATO INVENTARE: Se una frase non esiste IDENTICA nel testo, non includerla.
❌ NO EXAMPLES: Non includere "Mariana Trench", "Leone", "frase_letterale" o "nome" nei risultati.

JSON:`, text, entityCount)
}