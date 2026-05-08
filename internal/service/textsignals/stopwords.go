package textsignals

import (
	"bufio"
	"os"
	"strings"
)

type StopwordProvider struct {
	stopwords map[string]map[string]bool // lang -> term -> true
}

func NewStopwordProvider(configDir string) (*StopwordProvider, error) {
	p := &StopwordProvider{
		stopwords: make(map[string]map[string]bool),
	}

	// Load stopwords for supported languages
	langs := []string{"en", "it", "pt", "es"}
	for _, lang := range langs {
		path := configDir + "/stopwords/" + lang + ".txt"
		if err := p.loadStopwords(lang, path); err != nil {
			// Log warning but continue - stopwords are optional
			continue
		}
	}

	return p, nil
}

func (p *StopwordProvider) loadStopwords(lang, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if p.stopwords[lang] == nil {
		p.stopwords[lang] = make(map[string]bool)
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word != "" && !strings.HasPrefix(word, "#") {
			p.stopwords[lang][strings.ToLower(word)] = true
		}
	}

	return scanner.Err()
}

func (p *StopwordProvider) IsStopWord(lang, term string) bool {
	termLower := strings.ToLower(term)
	if langMap, ok := p.stopwords[lang]; ok {
		return langMap[termLower]
	}
	// Fallback to English if language not found
	if langMap, ok := p.stopwords["en"]; ok {
		return langMap[termLower]
	}
	return false
}
