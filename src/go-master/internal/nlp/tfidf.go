// Package nlp provides TF-IDF calculation functionality.
package nlp

import (
	"math"
	"sort"
)

// TFIDFCalculator calcola TF-IDF per keyword extraction
type TFIDFCalculator struct {
	documents []map[string]int // word -> count per document
	df        map[string]int   // document frequency
	n         int              // numero documenti
}

// NewTFIDFCalculator crea un nuovo calcolatore TF-IDF
func NewTFIDFCalculator() *TFIDFCalculator {
	return &TFIDFCalculator{
		documents: make([]map[string]int, 0),
		df:        make(map[string]int),
	}
}

// AddDocument aggiunge un documento al corpus
func (t *TFIDFCalculator) AddDocument(text string) {
	tokens := Tokenize(text)

	// Calcola term frequency per questo documento
	tf := make(map[string]int)
	seen := make(map[string]bool)

	for _, token := range tokens {
		tf[token]++
		if !seen[token] {
			seen[token] = true
			t.df[token]++
		}
	}

	t.documents = append(t.documents, tf)
	t.n++
}

// AddDocumentRaw aggiunge un documento già tokenizzato
func (t *TFIDFCalculator) AddDocumentRaw(tokens []string) {
	tf := make(map[string]int)
	seen := make(map[string]bool)

	for _, token := range tokens {
		tf[token]++
		if !seen[token] {
			seen[token] = true
			t.df[token]++
		}
	}

	t.documents = append(t.documents, tf)
	t.n++
}

// Calculate calcola TF-IDF per tutti i termini
func (t *TFIDFCalculator) Calculate() map[string]float64 {
	scores := make(map[string]float64)

	for _, doc := range t.documents {
		docLength := 0
		for _, count := range doc {
			docLength += count
		}

		if docLength == 0 {
			continue
		}

		for word, count := range doc {
			tf := float64(count) / float64(docLength)
			idf := math.Log(float64(t.n+1) / float64(t.df[word]+1))
			tfidf := tf * idf

			// Mantieni il punteggio massimo per ogni parola
			if current, exists := scores[word]; !exists || tfidf > current {
				scores[word] = tfidf
			}
		}
	}

	return scores
}

// GetTopKeywords restituisce le top N keyword
func (t *TFIDFCalculator) GetTopKeywords(n int) []Keyword {
	scores := t.Calculate()

	keywords := make([]Keyword, 0, len(scores))
	for word, score := range scores {
		count := 0
		for _, doc := range t.documents {
			count += doc[word]
		}
		keywords = append(keywords, Keyword{
			Word:  word,
			Score: score,
			Count: count,
		})
	}

	// Sort by score descending
	sort.Slice(keywords, func(i, j int) bool {
		return keywords[i].Score > keywords[j].Score
	})

	if n > len(keywords) {
		n = len(keywords)
	}

	return keywords[:n]
}

// GetWordCount restituisce il conteggio totale delle parole
func (t *TFIDFCalculator) GetWordCount() int {
	total := 0
	for _, doc := range t.documents {
		for _, count := range doc {
			total += count
		}
	}
	return total
}

// GetUniqueWordCount restituisce il numero di parole uniche
func (t *TFIDFCalculator) GetUniqueWordCount() int {
	unique := make(map[string]bool)
	for _, doc := range t.documents {
		for word := range doc {
			unique[word] = true
		}
	}
	return len(unique)
}

// ExtractKeywords estrae le keyword da un singolo testo
func ExtractKeywords(text string, topN int) []Keyword {
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return []Keyword{}
	}

	// Count term frequencies
	freq := make(map[string]int)
	for _, t := range tokens {
		freq[t]++
	}

	// For single-document queries, use normalized term frequency instead of TF-IDF
	// TF-IDF returns 0 for all words when n=1 (IDF = log(2/2) = 0)
	keywords := make([]Keyword, 0, len(freq))
	for word, count := range freq {
		// Score = normalized term frequency (0-1 range)
		score := float64(count) / float64(len(tokens))
		keywords = append(keywords, Keyword{
			Word:  word,
			Score: score,
			Count: count,
		})
	}

	// Sort by score descending
	sort.Slice(keywords, func(i, j int) bool {
		return keywords[i].Score > keywords[j].Score
	})

	if topN > len(keywords) {
		topN = len(keywords)
	}

	return keywords[:topN]
}

// ExtractKeywordsWithFreq estrae keyword con frequenza
func ExtractKeywordsWithFreq(text string, topN int) []Keyword {
	tokens := Tokenize(text)

	// Conta frequenza
	freq := make(map[string]int)
	for _, t := range tokens {
		freq[t]++
	}

	// Converti in slice
	keywords := make([]Keyword, 0, len(freq))
	for word, count := range freq {
		keywords = append(keywords, Keyword{
			Word:  word,
			Score: float64(count) / float64(len(tokens)),
			Count: count,
		})
	}

	// Sort by count
	sort.Slice(keywords, func(i, j int) bool {
		return keywords[i].Count > keywords[j].Count
	})

	if topN > len(keywords) {
		topN = len(keywords)
	}

	return keywords[:topN]
}