package script

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

type scoredMatch struct {
	Title   string
	Path    string
	Score   int
	Source  string
	Link    string
	Details string
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func collectTopicTerms(topic string) []string {
	seen := make(map[string]struct{})
	add := func(text string) {
		for _, term := range tokenize(text) {
			if len(term) < 3 || isStopWord(term) {
				continue
			}
			seen[term] = struct{}{}
		}
	}

	add(topic)

	terms := make([]string, 0, len(seen))
	for term := range seen {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	return terms
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func isStopWord(term string) bool {
	switch term {
	case "the", "and", "for", "with", "that", "this", "from", "then", "into", "over",
		"una", "uno", "del", "della", "delle", "degli", "nel", "nella", "nei",
		"per", "con", "tra", "gli", "le", "dei", "dai", "dalle", "dagli", "sul", "sulla", "sugli":
		return true
	default:
		return false
	}
}

func scoreText(candidate string, terms []string) int {
	candidate = strings.ToLower(candidate)
	score := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(candidate, term) {
			score++
		}
	}
	return score
}

func sortTopMatches(matches []scoredMatch, limit int) []scoredMatch {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Title < matches[j].Title
		}
		return matches[i].Score > matches[j].Score
	})
	if limit > 0 && len(matches) > limit {
		return matches[:limit]
	}
	return matches
}

func selectBestMatchLink(matches []scoredMatch) string {
	if len(matches) == 0 {
		return ""
	}

	cloned := make([]scoredMatch, len(matches))
	copy(cloned, matches)
	sort.SliceStable(cloned, func(i, j int) bool {
		if cloned[i].Score == cloned[j].Score {
			return strings.ToLower(cloned[i].Title) < strings.ToLower(cloned[j].Title)
		}
		return cloned[i].Score > cloned[j].Score
	})

	for _, match := range cloned {
		if strings.TrimSpace(match.Link) != "" {
			return strings.TrimSpace(match.Link)
		}
	}
	return ""
}

func renderMatches(matches []scoredMatch) string {
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n")
		}
		headline := match.Title
		if strings.TrimSpace(match.Path) != "" {
			headline = match.Path
		}
		b.WriteString("- ")
		b.WriteString(headline)
		b.WriteString("\n")
		if strings.TrimSpace(match.Path) != "" && strings.TrimSpace(match.Title) != strings.TrimSpace(match.Path) {
			b.WriteString("  Name: ")
			b.WriteString(match.Title)
			b.WriteString("\n")
		}
		b.WriteString("  Source: ")
		b.WriteString(match.Source)
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  Score: %d\n", match.Score))
		if strings.TrimSpace(match.Link) != "" {
			b.WriteString("  Link: ")
			b.WriteString(match.Link)
			b.WriteString("\n")
		}
		if strings.TrimSpace(match.Details) != "" {
			b.WriteString("  Details: ")
			b.WriteString(match.Details)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}
