// Package nlp provides tokenization functionality.
package nlp

import (
	"strings"
	"unicode"
)

// StopWords map delle stopwords (inglese + italiano)
var StopWords = map[string]bool{
	// English stopwords
	"the": true, "and": true, "or": true, "but": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "of": true, "with": true, "by": true,
	"from": true, "as": true, "is": true, "was": true, "are": true, "be": true,
	"this": true, "that": true, "it": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "can": true, "shall": true,
	"a": true, "an": true, "not": true, "no": true, "yes": true,
	"i": true, "you": true, "he": true, "she": true, "we": true, "they": true,
	"me": true, "him": true, "her": true, "us": true, "them": true,
	"my": true, "your": true, "his": true, "its": true, "our": true, "their": true,
	"what": true, "which": true, "who": true, "whom": true, "where": true, "when": true, "why": true, "how": true,
	"all": true, "each": true, "every": true, "both": true, "few": true, "more": true,
	"most": true, "other": true, "some": true, "such": true, "only": true, "own": true,
	"same": true, "so": true, "than": true, "too": true, "very": true, "just": true,
	// Italian stopwords
	"il": true, "la": true, "lo": true, "gli": true, "le": true,
	"di": true, "da": true, "con": true, "su": true,
	"per": true, "tra": true, "fra": true, "e": true, "o": true, "ma": true,
	"che": true, "chi": true, "cui": true, "non": true, "è": true, "sono": true,
	"un": true, "una": true, "del": true, "della": true, "dello": true,
	"al": true, "alla": true, "allo": true, "dal": true, "dalla": true, "dallo": true,
	"nel": true, "nella": true, "nello": true, "sul": true, "sulla": true, "sullo": true,
	"questo": true, "questa": true, "quello": true, "quella": true,
	"mio": true, "mia": true, "tuo": true, "tua": true, "suo": true, "sua": true,
	"nostro": true, "nostra": true, "vostro": true, "vostra": true,
	"essere": true, "avere": true, "fare": true, "dire": true, "andare": true,
	"venire": true, "vedere": true, "sapere": true, "potere": true, "volere": true,
	"come": true, "dove": true, "quando": true, "perché": true, "quanto": true,
	"anche": true, "ancora": true, "già": true, "sempre": true, "mai": true,
	"più": true, "meno": true, "molto": true, "poco": true, "troppo": true,
	"tutto": true, "tutti": true, "tutta": true, "tutte": true,
	"si": true, "ok": true,
}

// Tokenize divide il testo in token (parole)
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	text = RemovePunctuation(text)
	words := strings.Fields(text)

	var tokens []string
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word != "" && !IsStopWord(word) && len(word) > 2 {
			tokens = append(tokens, word)
		}
	}

	return tokens
}

// TokenizeAll tokenizza mantenendo tutte le parole (incluso stopwords)
func TokenizeAll(text string) []string {
	text = strings.ToLower(text)
	text = RemovePunctuation(text)
	return strings.Fields(text)
}

// RemovePunctuation rimuove la punteggiatura dal testo
func RemovePunctuation(text string) string {
	var result strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) {
			result.WriteRune(r)
		} else {
			result.WriteRune(' ')
		}
	}
	return result.String()
}

// IsStopWord verifica se una parola è una stopword
func IsStopWord(word string) bool {
	return StopWords[strings.ToLower(word)]
}

// AddStopWord aggiunge una stopword
func AddStopWord(word string) {
	StopWords[strings.ToLower(word)] = true
}

// CountWords conta le parole in un testo
func CountWords(text string) int {
	return len(strings.Fields(text))
}

// CountSentences conta le frasi in un testo
func CountSentences(text string) int {
	count := 0
	for _, r := range text {
		if r == '.' || r == '!' || r == '?' {
			count++
		}
	}
	if count == 0 && len(strings.TrimSpace(text)) > 0 {
		count = 1
	}
	return count
}

// GetSentences estrae le frasi da un testo
func GetSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	for _, r := range text {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}

	if current.Len() > 0 {
		s := strings.TrimSpace(current.String())
		if s != "" {
			sentences = append(sentences, s)
		}
	}

	return sentences
}

// AverageWordLength calcola la lunghezza media delle parole
func AverageWordLength(text string) float64 {
	tokens := TokenizeAll(text)
	if len(tokens) == 0 {
		return 0
	}

	totalLen := 0
	for _, t := range tokens {
		totalLen += len(t)
	}

	return float64(totalLen) / float64(len(tokens))
}