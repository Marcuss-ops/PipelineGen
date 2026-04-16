// Package ollama provides prompt templates for script generation.
package ollama

import (
	"fmt"
	"regexp"
	"strings"
)

// sanitizeInput rimuove potenziali injection da prompt
func sanitizeInput(input string) string {
	// Limita la lunghezza per prevenire DoS (aumentato per supportare script lunghi)
	if len(input) > 100000 {
		input = input[:100000]
	}
	// Rimuovi sequenze di istruzioni che potrebbero confondere il modello
	// (mantieni solo il testo normale)
	input = strings.ReplaceAll(input, "\n\n\n\n", "\n\n\n")
	return input
}

// buildTextPrompt costruisce il prompt per generazione da testo
func buildTextPrompt(req *TextGenerationRequest) string {
	durationMinutes := req.Duration / 60
	targetWords := (req.Duration * 140) / 60 // ~140 parole al minuto (speech rate medio)

	// Sanitize source text to prevent prompt injection
	sanitizedSource := sanitizeInput(req.SourceText)
	sanitizedTitle := sanitizeInput(req.Title)

	return fmt.Sprintf(`%s

TASK: Scrivi uno script DETTAGLIATO E COMPLETO per un video di %d secondi (%d minuti circa).

TITOLO: %s
LINGUA: %s
TONO: %s

CONTENUTO DI RIFERIMENTO:
%s

ISTRUZIONI IMPORTANTI:
- Questo è un video LUNGO di %d minuti - devi scrivere uno script MOLTO dettagliato
- Target: circa %d parole (NON essere breve, espandi ogni concetto)
- Scrivi solo il testo dello script, niente altro
- Lo script deve essere adatto per una narrazione vocale
- Struttura: Introduzione (10%%), Contenuto principale (80%%), Conclusione (10%%)
- Usa un linguaggio naturale e conversazionale
- Includi pause naturali indicate con "..."
- Espandi ogni fatto con dettagli, contesto e spiegazioni approfondite
- Non essere sintetico - ogni punto deve essere sviluppato ampiamente

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
	return fmt.Sprintf(`Sei un esperto di NLP specializzato nell'estrazione di entità da testi.

TESTO DA ANALIZZARE:
%s

TASK: Estrai esattamente %d entità per ognuna delle seguenti 4 categorie.

CATEGORIE RICHIESTE:

1. frasi_importanti: Frasi complete multi-parola (massimo 125 caratteri ciascuna)
   - Esempi: "La sentenza del tribunale", "Il processo di riforma europea"
   - NON singole parole, ma frasi significative

2. entity_senza_testo: Entità che hanno un'immagine associata (loghi, stemmi, foto)
   - Formato JSON object: {"Nome Entità": "URL immagine"}
   - Esempi: {"Logo Tesla": "https://logo.clearbit.com/tesla.com", "Foto Elon Musk": "https://..."}
   - Se non trovi URL reali, usa placeholder: {"Nome Entità": "https://via.placeholder.com/300x200"}

3. nomi_speciali: Nomi propri, organizzazioni, prodotti, termini tecnici, gergo
   - Esempi: "Tesla", "RemoteCodex", "Elon Musk", "DataServer"

4. parole_importanti: Singole keyword o termini brevi estratti dal testo
   - Esempi: "tecnologia", "innovazione", "futuro", "giustizia"

RISPOSTA: Restituisci SOLO un oggetto JSON valido con questa struttura:
{
  "frasi_importanti": ["frase 1", "frase 2", ...],
  "entity_senza_testo": {"Nome 1": "URL 1", "Nome 2": "URL 2", ...},
  "nomi_speciali": ["nome 1", "nome 2", ...],
  "parole_importanti": ["parola 1", "parola 2", ...]
}

IMPORTANTE:
- Esattamente %d entità per categoria
- Solo JSON, nessun testo aggiuntivo
- Non usare markdown o code blocks
- Se non trovi abbastanza entità, riempi con le migliori disponibili

JSON:`, text, entityCount, entityCount)
}

// getSystemPrompt restituisce il prompt di sistema in base alla lingua
func getSystemPrompt(language, tone string) string {
	prompts := map[string]string{
		"italian":  "Sei un copywriter esperto specializzato in script video.",
		"english":  "You are an expert copywriter specialized in video scripts.",
		"spanish":  "Eres un copywriter experto especializado en guiones de video.",
		"french":   "Vous êtes un rédacteur expert spécialisé dans les scripts vidéo.",
		"german":   "Sie sind ein erfahrener Copywriter, spezialisiert auf Video-Skripte.",
	}

	prompt := prompts[language]
	if prompt == "" {
		prompt = prompts["english"]
	}

	toneInstructions := map[string]string{
		"professional": "Tono professionale e autorevole.",
		"casual":       "Tono informale e amichevole.",
		"enthusiastic": "Tono entusiasta ed energico.",
		"calm":         "Tono calmo e rilassante.",
		"funny":        "Tono divertente e spiritoso.",
		"educational":  "Tono educativo e chiaro.",
	}

	if toneInstr, ok := toneInstructions[tone]; ok {
		prompt += " " + toneInstr
	}

	return prompt
}

// cleanScript pulisce lo script generato rimuovendo markdown code blocks
func cleanScript(script string) string {
	// Rimuovi markdown code blocks con language tag (```python, ```text, etc.)
	// Usa regex per gestire tutti i casi: ```lang\n...\n``` e ```...\n```
	re := regexp.MustCompile("(?s)^```[a-zA-Z]*\\n?(.*?)\\n?```$")
	if matches := re.FindStringSubmatch(script); len(matches) > 1 {
		script = matches[1]
	}

	// Fallback: se il regex non matcha, prova a rimuovere backtick grezzi
	script = strings.TrimPrefix(script, "```")
	script = strings.TrimSuffix(script, "```")

	// Trim spazi
	script = strings.TrimSpace(script)

	return script
}

// estimateDuration stima la durata in secondi basata sul word count
func estimateDuration(wordCount int) int {
	// ~140 parole al minuto (speech rate medio)
	return (wordCount * 60) / 140
}

// countWords conta le parole in una stringa
func countWords(text string) int {
	return len(strings.Fields(text))
}