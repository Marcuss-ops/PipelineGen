// Package nlp provides moment extraction from VTT files.
package nlp

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ParseVTT parsa contenuto WebVTT e restituisce i segmenti
func ParseVTT(content string) (*VTT, error) {
	var segments []Segment

	lines := strings.Split(content, "\n")
	var currentSegment Segment
	var inSegment bool

	// Regex per timestamp: 00:00:00.000 --> 00:00:00.000
	timeRegex := regexp.MustCompile(`(\d{2}:\d{2}:\d{2}\.\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}\.\d{3})`)

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" {
			if inSegment && currentSegment.Text != "" {
				segments = append(segments, currentSegment)
				currentSegment = Segment{}
				inSegment = false
			}
			continue
		}

		// Cerca timestamp
		matches := timeRegex.FindStringSubmatch(line)
		if len(matches) == 3 {
			start, _ := parseTimestamp(matches[1])
			end, _ := parseTimestamp(matches[2])
			currentSegment.Start = start
			currentSegment.End = end
			inSegment = true
			continue
		}

		// Salta header WEBVTT e numeri cue
		if strings.HasPrefix(line, "WEBVTT") || strings.HasPrefix(line, "NOTE") {
			continue
		}

		// Salta numeri di cue (interi all'inizio di un blocco)
		if isCueNumber(line) && !inSegment {
			continue
		}

		// Accumula testo
		if inSegment {
			if currentSegment.Text != "" {
				currentSegment.Text += " "
			}
			currentSegment.Text += line
		}
	}

	// Non dimenticare l'ultimo segmento
	if inSegment && currentSegment.Text != "" {
		segments = append(segments, currentSegment)
	}

	return &VTT{Segments: segments}, nil
}

// parseTimestamp converte un timestamp VTT in secondi
func parseTimestamp(ts string) (float64, error) {
	// Formato: HH:MM:SS.mmm
	parts := strings.Split(ts, ":")
	if len(parts) != 3 {
		return 0, nil
	}

	hours, _ := strconv.ParseFloat(parts[0], 64)
	minutes, _ := strconv.ParseFloat(parts[1], 64)
	seconds, _ := strconv.ParseFloat(parts[2], 64)

	return hours*3600 + minutes*60 + seconds, nil
}

// isCueNumber verifica se una linea è un numero di cue
func isCueNumber(line string) bool {
	_, err := strconv.Atoi(line)
	return err == nil
}

// ExtractMoments estrae momenti chiave da un VTT
func ExtractMoments(vtt *VTT, topicKeywords []string, maxMoments int) []Moment {
	if maxMoments <= 0 {
		maxMoments = 5
	}

	// Score each segment
	for i := range vtt.Segments {
		vtt.Segments[i].Score = scoreSegment(&vtt.Segments[i], topicKeywords)
	}

	// Sort by score descending
	sort.Slice(vtt.Segments, func(i, j int) bool {
		return vtt.Segments[i].Score > vtt.Segments[j].Score
	})

	// Take top moments
	if maxMoments > len(vtt.Segments) {
		maxMoments = len(vtt.Segments)
	}

	moments := make([]Moment, maxMoments)
	for i := 0; i < maxMoments; i++ {
		seg := vtt.Segments[i]
		moments[i] = Moment{
			StartTime:  seg.Start,
			EndTime:    seg.End,
			Text:       strings.TrimSpace(seg.Text),
			Score:      seg.Score,
			Importance: getImportanceLabel(seg.Score),
		}
	}

	// Sort by time for output
	sort.Slice(moments, func(i, j int) bool {
		return moments[i].StartTime < moments[j].StartTime
	})

	return moments
}

// scoreSegment calcola un punteggio per un segmento
func scoreSegment(seg *Segment, topicKeywords []string) float64 {
	score := 0.0
	text := strings.ToLower(seg.Text)
	tokens := Tokenize(text)

	// Fattore durata (preferiamo segmenti di media lunghezza)
	duration := seg.End - seg.Start
	if duration > 5 && duration < 60 {
		score += 10
	} else if duration > 3 && duration < 90 {
		score += 5
	}

	// Match keyword
	for _, keyword := range topicKeywords {
		keyword = strings.ToLower(keyword)
		for _, token := range tokens {
			if token == keyword {
				score += 20 // match esatto
			} else if strings.Contains(token, keyword) || strings.Contains(keyword, token) {
				score += 10 // match parziale
			}
		}
	}

	// Densità informativa (più parole uniche = punteggio più alto)
	uniqueWords := make(map[string]bool)
	for _, token := range tokens {
		uniqueWords[token] = true
	}
	score += float64(len(uniqueWords)) * 2

	// Bonus per segmenti con più contenuto
	if len(tokens) > 10 {
		score += 5
	}

	return score
}

// getImportanceLabel converte un punteggio in un'etichetta di importanza
func getImportanceLabel(score float64) string {
	if score > 50 {
		return "high"
	} else if score > 25 {
		return "medium"
	}
	return "low"
}

// ExtractMomentsFromText estrae momenti da testo semplice (non VTT)
func ExtractMomentsFromText(text string, keywords []string, maxMoments int) []Moment {
	// Dividi il testo in frasi
	sentences := GetSentences(text)
	if len(sentences) == 0 {
		return []Moment{}
	}

	// Crea segmenti fittizi
	var segments []Segment
	for i, sent := range sentences {
		seg := Segment{
			Start: float64(i * 10), // 10 secondi per frase
			End:   float64((i + 1) * 10),
			Text:  sent,
		}
		segments = append(segments, seg)
	}

	vtt := &VTT{Segments: segments}
	return ExtractMoments(vtt, keywords, maxMoments)
}

// FindBestMoment trova il momento migliore
func FindBestMoment(vtt *VTT, topicKeywords []string) *Moment {
	moments := ExtractMoments(vtt, topicKeywords, 1)
	if len(moments) == 0 {
		return nil
	}
	return &moments[0]
}

// GetMomentsByImportance filtra momenti per importanza
func GetMomentsByImportance(moments []Moment, importance string) []Moment {
	var result []Moment
	for _, m := range moments {
		if m.Importance == importance {
			result = append(result, m)
		}
	}
	return result
}

// MergeCloseMoments unisce momenti vicini
func MergeCloseMoments(moments []Moment, gapThreshold float64) []Moment {
	if len(moments) <= 1 {
		return moments
	}

	// Sort by start time
	sort.Slice(moments, func(i, j int) bool {
		return moments[i].StartTime < moments[j].StartTime
	})

	var merged []Moment
	current := moments[0]

	for i := 1; i < len(moments); i++ {
		next := moments[i]

		// Se la distanza è sotto la soglia, unisci
		if next.StartTime-current.EndTime < gapThreshold {
			current.EndTime = next.EndTime
			current.Text += " " + next.Text
			if next.Score > current.Score {
				current.Score = next.Score
			}
			if next.Importance == "high" || current.Importance == "high" {
				current.Importance = "high"
			}
		} else {
			merged = append(merged, current)
			current = next
		}
	}
	merged = append(merged, current)

	return merged
}