// Package translation fornisce traduzione keywords IT→EN per ricerca clip
package translation

import (
	"strings"
)

// ClipSearchTranslator traduce keywords in inglese per ricerca clip
// Artlist e stock library usano SOLO inglese - keywords italiane non trovano nulla
type ClipSearchTranslator struct {
	dictionary map[string]string
}

// NewClipSearchTranslator crea un nuovo translator
func NewClipSearchTranslator() *ClipSearchTranslator {
	return &ClipSearchTranslator{
		dictionary: buildClipDictionary(),
	}
}

// TranslateKeywords traduce un array di keywords in inglese
// Le parole non trovate nel dizionario vengono mantenute così come sono
func (t *ClipSearchTranslator) TranslateKeywords(keywords []string) []string {
	translated := make([]string, 0, len(keywords))
	seen := make(map[string]bool)

	for _, kw := range keywords {
		lower := strings.ToLower(kw)
		
		// Se già tradotta, skip
		if seen[lower] {
			continue
		}

		// Cerca nel dizionario
		if eng, ok := t.dictionary[lower]; ok {
			if !seen[eng] {
				translated = append(translated, eng)
				seen[eng] = true
			}
		} else {
			// Mantieni originale (potrebbe già essere inglese)
			if !seen[lower] {
				translated = append(translated, lower)
				seen[lower] = true
			}
		}
	}

	return translated
}

// TranslateQuery traduce una query intera (stringa singola)
func (t *ClipSearchTranslator) TranslateQuery(query string) string {
	words := strings.Fields(query)
	translated := t.TranslateKeywords(words)
	return strings.Join(translated, " ")
}

// translateScene traduce tutti i campi di una scena per ricerca clip
func (t *ClipSearchTranslator) TranslateScene(keywords []string, entities []string, emotions []string) (translatedKeywords []string, translatedEntities []string, translatedEmotions []string) {
	translatedKeywords = t.TranslateKeywords(keywords)
	translatedEntities = t.TranslateKeywords(entities)
	translatedEmotions = t.TranslateEmotions(emotions)
	return
}

// TranslateEmotions traduce emozioni in inglese (sono già in inglese nel sistema)
func (t *ClipSearchTranslator) TranslateEmotions(emotions []string) []string {
	// Le emozioni sono GIA' in inglese nel sistema (sadness, joy, etc.)
	// Ma traduciamo se arrivano in italiano
	emotionMap := map[string]string{
		"tristezza":      "sadness",
		"gioia":          "joy",
		"felicità":       "joy",
		"rabbia":         "anger",
		"paura":          "fear",
		"sorpresa":       "surprise",
		"fiducia":        "trust",
		"aspettativa":    "anticipation",
		"malinconia":     "sadness",
		"energia":        "energy",
		"motivazione":    "motivation",
		"nostalgia":      "nostalgia",
		"speranza":       "hope",
	}

	translated := make([]string, 0, len(emotions))
	seen := make(map[string]bool)

	for _, emotion := range emotions {
		lower := strings.ToLower(emotion)
		if eng, ok := emotionMap[lower]; ok {
			if !seen[eng] {
				translated = append(translated, eng)
				seen[eng] = true
			}
		} else {
			if !seen[lower] {
				translated = append(translated, lower)
				seen[lower] = true
			}
		}
	}

	return translated
}

// buildClipDictionary crea dizionario IT→EN per clip search
// QUESTO È CRITICO: Artlist ha clip in INGLESE, cercare in italiano = 0 risultati
func buildClipDictionary() map[string]string {
	return map[string]string{
		// === TECH / COMPUTING ===
		"calcolo":       "computing",
		"quantistico":   "quantum",
		"computer":      "computer",
		"tecnologia":    "technology",
		"software":      "software",
		"hardware":      "hardware",
		"algoritmo":     "algorithm",
		"dati":          "data",
		"server":        "server",
		"rete":          "network",
		"internet":      "internet",
		"digitale":      "digital",
		"schermo":       "screen",
		"monitor":       "monitor",
		"tastiera":      "keyboard",
		"codice":        "code",
		"programma":     "program",
		"sistema":       "system",
		"intelligenza":  "intelligence",
		"artificiale":   "artificial",
		"ai":            "ai",
		"robot":         "robot",
		"automazione":   "automation",
		"chip":          "chip",
		"processore":    "processor",
		"silicio":       "silicon",
		"circuito":      "circuit",
		"elettronica":   "electronics",
		"innovazione":   "innovation",
		"laboratorio":   "laboratory",
		"laboratori":    "laboratory",
		"ricerca":       "research",
		"scienza":       "science",
		"scientifico":   "scientific",
		"molecolare":    "molecular",
		"simulazione":   "simulation",
		"simulazioni":   "simulation",
		"raffreddate":   "cooled",
		"liquido":       "liquid",
		"farm":          "farm",
		"superconduttori": "superconductor",
		"neuromorfici":  "neuromorphic",
		"qubit":         "qubit",

		// === BUSINESS / MARKETING ===
		"business":      "business",
		"azienda":       "company",
		"marketing":     "marketing",
		"vendite":       "sales",
		"vendita":       "sale",
		"brand":         "brand",
		"marca":         "brand",
		"logo":          "logo",
		"nicchia":       "niche",
		"strategia":     "strategy",
		"social":        "social media",
		"pubblicità":    "advertising",
		"successo":      "success",
		"denaro":        "money",
		"soldi":         "money",
		"finanza":       "finance",
		"economia":      "economy",
		"mercato":       "market",
		"commercio":     "commerce",
		"lavoro":        "work",
		"lavorare":      "working",
		"ufficio":       "office",
		"riunione":      "meeting",
		"presentazione": "presentation",
		"grafico":       "chart",
		"statistica":    "statistics",

		// === EMOTIONS / MOOD ===
		"felice":        "happy",
		"gioia":         "joy",
		"triste":        "sad",
		"malinconico":   "melancholic",
		"malinconia":    "melancholy",
		"solitudine":    "loneliness",
		"energia":       "energy",
		"energico":      "energetic",
		"motivazione":   "motivation",
		"motivazionale": "motivational",
		"solare":        "sunny",
		"sorriso":       "smile",
		"sorridere":     "smiling",
		"pioggia":       "rain",
		"piovoso":       "rainy",
		"alba":          "dawn",
		"tramonto":      "sunset",
		"notte":         "night",
		"giorno":        "day",
		"sole":          "sun",
		"cielo":         "sky",
		"città":         "city",
		"città grande":  "big city",
		"strada":        "street",
		"strade":        "streets",
		"vuoto":         "empty",
		"vuota":         "empty",
		"persona":       "person",
		"persone":       "people",
		"correre":       "running",
		"corrono":       "running",

		// === EDUCATION ===
		"educazione":    "education",
		"scuola":        "school",
		"imparare":      "learning",
		"insegnare":     "teaching",
		"guida":         "guide",
		"tutorial":      "tutorial",
		"corso":         "course",
		"lezione":       "lesson",
		"pratica":       "practical",
		"creare":        "create",
		"creazione":     "creation",
		"disegnare":     "design",
		"analizzare":    "analyze",
		"analisi":       "analysis",

		// === VISUAL / CINEMA ===
		"video":         "video",
		"filmato":       "footage",
		"clip":          "clip",
		"panoramica":    "panoramic",
		"aereo":         "aerial",
		"dall'alto":     "aerial view",
		"primo piano":   "close-up",
		"dettaglio":     "detail",
		"paesaggio":     "landscape",
		"natura":        "nature",
		"montagna":      "mountain",
		"mare":          "sea",
		"spiaggia":      "beach",
		"foresta":       "forest",
		"parco":         "park",
		"edificio":      "building",
		"grattacielo":   "skyscraper",
		"architettura":  "architecture",
		"moderno":       "modern",
		"futuristico":   "futuristic",
		"cinematografico": "cinematic",
		"drammatico":    "dramatic",
		"epico":         "epic",
		"mostra":        "showing",
		"appare":        "appearing",
		"cambia":        "changing",
		"transizione":   "transition",

		// === GENERAL ===
		"futuro":        "future",
		"presente":      "present",
		"passato":       "past",
		"tempo":         "time",
		"veloce":        "fast",
		"velocità":      "speed",
		"lento":         "slow",
		"grande":        "big",
		"piccolo":       "small",
		"nuovo":         "new",
		"vecchio":       "old",
		"mondiale":      "global",
		"mondo":         "world",
		"italia":        "italy",
		"europa":        "europe",
		"america":       "america",
	}
}
