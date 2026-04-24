package script

import (
	"sort"
	"strings"

	"velox/go-master/internal/service/scriptdocs"
)

const maxEntityListItems = 5

// buildGenericClipTerms returns generic stock/artlist concepts (not sentence-literal terms).
func buildGenericClipTerms(topic, text string) []string {
	raw := strings.ToLower(strings.TrimSpace(topic + " " + text))
	terms := make([]string, 0, 12)

	add := func(v string) {
		v = strings.TrimSpace(strings.ToLower(v))
		if v == "" {
			return
		}
		for _, e := range terms {
			if e == v {
				return
			}
		}
		terms = append(terms, v)
	}

	// Domain-specific concepts first (keeps searches reusable across future matches)
	if containsAny(raw, []string{"box", "boxing", "pugil", "fighter", "fight", "ring", "campione", "champion", "match"}) {
		add("boxing")
		add("boxing match")
		add("fighter")
		add("champion")
		add("ring")
		add("trophy")
		add("training")
		add("victory")
	}

	if containsAny(raw, []string{"court", "courtroom", "tribunal", "process", "judge", "legal"}) {
		add("courtroom")
		add("judge")
		add("lawyer")
		add("trial")
		add("legal documents")
	}

	// Fallback generic terms from keywords (without full phrase fallback)
	for _, kw := range scriptdocs.ExtractKeywords(text) {
		kw = normalizeEntityToken(kw)
		if kw == "" || len(strings.Fields(kw)) > 3 {
			continue
		}
		add(kw)
		if len(terms) >= maxEntityListItems {
			break
		}
	}

	if len(terms) == 0 {
		for _, kw := range scriptdocs.ExtractKeywords(topic) {
			kw = normalizeEntityToken(kw)
			if kw == "" || len(strings.Fields(kw)) > 3 {
				continue
			}
			add(kw)
			if len(terms) >= maxEntityListItems {
				break
			}
		}
	}

	return terms
}

// compactSearchTopic reduces long titles to a stable search key for clip folders.
func compactSearchTopic(topic string) string {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return ""
	}

	parts := strings.FieldsFunc(topic, func(r rune) bool {
		switch r {
		case ',', ':', ';', '-', '–', '—', '|', '/', '(', ')', '[':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}

	words := strings.Fields(parts[0])
	if len(words) == 0 {
		return ""
	}
	if len(words) > 2 {
		words = words[:2]
	}
	return strings.Join(words, " ")
}

func containsAny(text string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

func isSystemWord(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	systemWords := map[string]bool{
		"titolo": true, "durata": true, "introduzione": true, "contenuto": true,
		"principale": true, "analisi": true, "tecnica": true, "strategia": true,
		"dati": true, "performance": true, "carattere": true, "dedizione": true,
		"conclusione": true, "immagine": true, "musica": true, "voce": true,
		"campo": true, "fuori": true, "circa": true, "minuto": true, "secondi": true,
		"azione": true, "questo": true, "quello": true, "ogni": true, "tutti": true,
		"della": true, "delle": true, "degli": true, "dallo": true, "dalla": true,
		"nelle": true, "nello": true, "nella": true, "sulla": true, "sulle": true,
		"davis": true, "gervonta": true, // Anche se sono nomi propri, l'utente li trova ridondanti se ripetuti ovunque
	}
	return systemWords[s]
}

func normalizeEntityToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.Trim(s, ".,;:!?\"'`()[]{}*_-/\\")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) < 3 || isSystemWord(s) {
		return ""
	}
	return s
}

func uniqueAndLimit(values []string, limit int) []string {
	if limit <= 0 {
		return []string{}
	}
	out := make([]string, 0, limit)
	seen := make(map[string]bool)
	for _, v := range values {
		n := normalizeEntityToken(v)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func uniqueEntitiesWithImage(values []EntityImage, limit int) []EntityImage {
	if limit <= 0 {
		return []EntityImage{}
	}
	out := make([]EntityImage, 0, limit)
	seen := make(map[string]bool)
	for _, v := range values {
		n := normalizeEntityToken(v.Entity)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Entity < out[j].Entity
	})
	return out
}
