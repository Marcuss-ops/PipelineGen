package matching

import (
	"path/filepath"
	"strings"
)

// ScoredCandidate represents a generic candidate with a match score
type ScoredCandidate struct {
	ID     string
	Title  string
	Link   string
	Score  float64
	Reason string
	Tags   []string
}

// Matcher provides logic for scoring and ranking assets against search phrases
type Matcher struct{}

// NewMatcher creates a new matching service
func NewMatcher() *Matcher {
	return &Matcher{}
}

const (
	// Boost values for different match types (exported for use across packages)
	NameMatchBoost    = 20.0
	FilenameMatchBoost = 18.0
	FolderMatchBoost   = 10.0
	TopicMatchBoost    = 5.0
	SideTextBoost      = 5.0
)

// ScoreAsset calculates a match score (0-100) between a search phrase and an asset
func (m *Matcher) ScoreAsset(phrase string, name, filename, folder, tags string) (float64, string) {
	phraseNorm := m.normalize(phrase)
	if phraseNorm == "" {
		return 0, ""
	}

	phraseTokens := m.tokenize(phraseNorm)
	if len(phraseTokens) == 0 {
		return 0, ""
	}

	candidateText := strings.Join([]string{name, filename, folder, tags}, " ")
	candidateNorm := m.normalize(candidateText)
	if candidateNorm == "" {
		return 0, ""
	}

	candidateTokens := m.tokenize(candidateNorm)
	candidateSet := make(map[string]struct{}, len(candidateTokens))
	for _, tok := range candidateTokens {
		candidateSet[tok] = struct{}{}
	}

	hits := 0
	for _, tok := range phraseTokens {
		if _, ok := candidateSet[tok]; ok {
			hits++
		}
	}

	// Base score: percentage of phrase tokens found in candidate
	base := float64(hits) / float64(len(phraseTokens)) * 100
	if base <= 0 {
		return 0, ""
	}

	// Boosts
	boost := 0.0
	nameNorm := m.normalize(name)
	fileNorm := m.normalize(strings.TrimSuffix(filename, filepath.Ext(filename)))
	
	if nameNorm != "" && strings.Contains(phraseNorm, nameNorm) {
		boost += NameMatchBoost
	} else if fileNorm != "" && strings.Contains(phraseNorm, fileNorm) {
		boost += FilenameMatchBoost
	}

	score := base + boost
	if score > 100 {
		score = 100
	}

	reason := "token_overlap"
	if boost > 0 {
		reason += "+boost"
	}

	return score, reason
}

func (m *Matcher) tokenize(text string) []string {
	return strings.Fields(text)
}

func (m *Matcher) normalize(text string) string {
	t := strings.ToLower(text)
	t = strings.ReplaceAll(t, "_", " ")
	t = strings.ReplaceAll(t, "-", " ")
	t = strings.ReplaceAll(t, ".", " ")
	return strings.TrimSpace(t)
}
