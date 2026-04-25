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

// getSystemPrompt returns the system prompt based on language and tone
func getSystemPrompt(language, tone string) string {
	// Base system prompts per language
	basePrompts := map[string]map[string]string{
		"italian": {
			"default": "Sei un narratore eccezionale e un copywriter senior. Il tuo compito è scrivere script video AVVINCENTI, RICCHI DI DETTAGLI e NARRATIVAMENTE POTENTI.",
		},
		"english": {
			"default": "You are an exceptional storyteller and senior copywriter. Your task is to write COMPELLING, DETAIL-RICH, and NARRATIVELY POWERFUL video scripts.",
		},
		"spanish": {
			"default": "Eres un narrador excepcional e copywriter senior. Tu tarea es escribir guiones de video FASCINANTES, RICOS EN DETALLES y NARRATIVAMENTE PODEROSOS.",
		},
		"french": {
			"default": "Vous êtes un conteur exceptionnel et un rédacteur principal. Votre tâche consiste à rédiger des scripts vidéo CAPTIVANTS, RICHES EN DÉTAILS et NARRATIVEMENT PUISSANTS.",
		},
		"german": {
			"default": "Sie sind un außergewöhnlicher Geschichtenerzähler und Senior Copywriter. Ihre Aufgabe ist es, FESSELNDE, DETAILREICHE und NARRATIV STARKE Video-Skripte zu schreiben.",
		},
		"portuguese": {
			"default": "Você é um contador de histórias excepcional e redator sênior. Sua tarefa é escrever roteiros de vídeo ENVOLVENTES, RICOS EM DETALHES e NARRATIVAMENTE PODEROSOS.",
		},
		"russian": {
			"default": "Вы — выдающийся рассказчик и старший копирайтер. Ваша задача — писать ЗАХВАТЫВАЮЩИЕ, БОГАТЫЕ ДЕТАЛЯМИ и НАРРАТИВНО МОЩНЫЕ сценарии видео.",
		},
	}

	// Tone instructions per language
	toneInstructions := map[string]map[string]string{
		"italian": {
			"professional": "Usa uno stile documentaristico, autorevole e serio. Analizza profondamente ogni aspetto.",
			"casual":       "Usa uno stile colloquiale, moderno e amichevole. Sii coinvolgente come un creator di YouTube.",
			"enthusiastic": "Usa uno stile energico, epico e motivazionale. Ogni frase deve trasmettere passione.",
			"calm":         "Usa uno stile pacato, riflessivo e poetico. Crea un'atmosfera immersiva.",
			"funny":        "Usa uno stile ironico, brillante e divertente. Inserisci battute o osservazioni sagaci.",
			"educational":  "Usa uno stile chiaro, pedagogico e strutturato. Spiega i concetti in modo semplice ma esaustivo.",
		},
		"english": {
			"professional": "Use a documentary, authoritative, and serious style. Deeply analyze every aspect.",
			"casual":       "Use a colloquial, modern, and friendly style. Be engaging like a YouTube creator.",
			"enthusiastic": "Use an energetic, epic, and motivational style. Every sentence must convey passion.",
			"calm":         "Use a calm, reflective, and poetic style. Create an immersive atmosphere.",
			"funny":        "Use an ironic, brilliant, and funny style. Insert jokes or witty observations.",
			"educational":  "Use a clear, pedagogical, and structured style. Explain concepts simply but thoroughly.",
		},
		"spanish": {
			"professional": "Usa un estilo documental, autoritario y serio. Analiza profundamente cada aspecto.",
			"casual":       "Usa un estilo coloquial, moderno y amigable. Sé atractivo como un creador de YouTube.",
			"enthusiastic": "Usa un estilo enérgico, épico y motivacional. Cada frase debe transmitir pasión.",
			"calm":         "Usa un estilo calmado, reflexivo y poético. Crea una atmósfera inmersiva.",
			"funny":        "Usa un estilo irónico, brillante y divertido. Inserta chistes u observaciones ingeniosas.",
			"educational":  "Usa un estilo claro, pedagógico y estructurado. Explica conceptos de forma sencilla pero exhaustiva.",
		},
		"french": {
			"professional": "Utilisez un style documentaire, autoritaire et sérieux. Analysez profondément chaque aspect.",
			"casual":       "Utilisez un style familier, moderne et amical. Soyez captivant comme un créateur YouTube.",
			"enthusiastic": "Utilisez un style énergique, épique et motivant. Chaque phrase doit transmettre la passion.",
			"calm":         "Utilisez un style calme, réfléchi et poétique. Créez une atmosphère immersive.",
			"funny":        "Utilisez un style ironique, brillant et amusant. Insérez des blagues ou des observations astucieuses.",
			"educational":  "Utilisez un style clair, pédagogique et structuré. Expliquez les concepts simplement mais exhaustivement.",
		},
		"german": {
			"professional": "Verwenden Sie einen dokumentarischen, autoritären und seriösen Stil. Analysieren Sie jeden Aspekt tiefgreifend.",
			"casual":       "Verwenden Sie einen umgangssprachlichen, modernen und freundlichen Stil. Seien Sie fesselnd wie ein YouTube-Creator.",
			"enthusiastic": "Verwenden Sie einen energischen, epischen und motivierenden Stil. Jeder Satz muss Leidenschaft vermitteln.",
			"calm":         "Verwenden Sie einen ruhigen, reflektierten und poetischen Stil. Schaffen Sie eine immersive Atmosphäre.",
			"funny":        "Verwenden Sie einen ironischen, brillanten und lustigen Stil. Fügen Sie Witze oder geistreiche Beobachtungen ein.",
			"educational":  "Verwenden Sie einen klaren, pädagogischen und strukturierten Stil. Erklären Sie Konzepte einfach, aber gründlich.",
		},
	}

	// Get base prompt for language
	langPrompts, ok := basePrompts[language]
	if !ok {
		langPrompts = basePrompts["english"]
	}
	prompt := langPrompts["default"]

	// Get tone instructions for language
	langTones, ok := toneInstructions[language]
	if !ok {
		langTones = toneInstructions["english"]
	}

	if toneInstr, ok := langTones[tone]; ok {
		prompt += " " + toneInstr
	}

	return prompt
}

// cleanScript pulisce lo script generato rimuovendo markdown e meta-testo (musica, descrizioni immagini)
func cleanScript(script string) string {
	// 1. Rimuovi blocchi di codice markdown
	reCode := regexp.MustCompile("(?s)```[a-zA-Z]*\\n?(.*?)\\n?```")
	if matches := reCode.FindStringSubmatch(script); len(matches) > 1 {
		script = matches[1]
	}

	// 2. Rimuovi meta-testo tipo (Musica: ...), [Immagini: ...], **Musica:**
	// Gestisce parentesi tonde, quadre e tag in grassetto
	reMeta := regexp.MustCompile(`(?i)(\(|\[|\*\*)\s*(musica|immagini|scena|inquadratura|audio|video|clip|montaggio|sottofondo|background|visual|transition|transizione)\s*:.*(\)|\]|\*\*)`)
	script = reMeta.ReplaceAllString(script, "")

	// 3. Rimuovi timestamp tipo [00:00] o (01:30)
	reTime := regexp.MustCompile(`(\[|\()\d{1,2}:\d{2}(\]|\))`)
	script = reTime.ReplaceAllString(script, "")

	// 4. Pulizia backtick e spazi
	script = strings.TrimPrefix(script, "```")
	script = strings.TrimSuffix(script, "```")
	script = strings.TrimSpace(script)

	// 5. Rimuovi righe che sono puramente descrittive
	lines := strings.Split(script, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		// Salta righe che sembrano istruzioni dell'LLM o intestazioni di sezione
		if trimmed == "" ||
			strings.HasPrefix(lower, "introduzione:") ||
			strings.HasPrefix(lower, "conclusione:") ||
			strings.HasPrefix(lower, "scena ") ||
			(strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed, " ")) { // Salta titoli H1 vuoti o tag singoli
			continue
		}
		cleanLines = append(cleanLines, trimmed)
	}

	return strings.Join(cleanLines, "\n\n")
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
