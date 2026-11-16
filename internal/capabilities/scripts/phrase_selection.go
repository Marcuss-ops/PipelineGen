package scriptgeneration

import (
	"context"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// PhraseSelectionProfile is the lexicon data the Rust selector needs. The
// selector decision stays in Rust; Go only loads and forwards the profile.
type PhraseSelectionProfile struct {
	StopWords          []string `json:"stop_words,omitempty"`
	FunctionWords      []string `json:"function_words,omitempty"`
	VisualVerbs        []string `json:"visual_verbs,omitempty"`
	MinWords           int      `json:"min_words,omitempty"`
	MaxWords           int      `json:"max_words,omitempty"`
	MaxResults         int      `json:"max_results,omitempty"`
	RejectVerbsWhenAll bool     `json:"reject_verbs_when_all"`
}

// PhraseSelectionRequest asks the Rust worker for source-grounded phrases in
// editorial order. BlockedRuneSpans are the entity ranges the caller already
// grounded; the selector may not overlap them.
type PhraseSelectionRequest struct {
	Text             string
	BlockedRuneSpans [][2]int
	Limit            int
	MinWords         int
	MaxWords         int
	Corpus           []string
	Profile          *PhraseSelectionProfile
}

// SelectedPhrase is one phrase returned by the Rust selector. Byte spans index
// the trimmed request text; rune spans are the same coordinates Go annotations
// use.
type SelectedPhrase struct {
	Text      string `json:"text"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	StartRune int    `json:"start_rune"`
	EndRune   int    `json:"end_rune"`
	Score     int64  `json:"score"`
}

// PhraseSelector is the single production seam for phrase selection. The
// semantic-engine adapter implements it over the Rust worker.
type PhraseSelector interface {
	SelectPhrases(ctx context.Context, request PhraseSelectionRequest) ([]SelectedPhrase, error)
}

// NewPhraseSelectionProfile projects a loaded lexicon profile onto the wire
// contract. Keys are sorted so the request is deterministic across map
// iteration.
func NewPhraseSelectionProfile(profile *linguistics.LexiconProfile) *PhraseSelectionProfile {
	if profile == nil {
		return nil
	}
	return &PhraseSelectionProfile{
		StopWords:          sortedWordSet(profile.StopWords),
		FunctionWords:      sortedWordSet(profile.FunctionWords),
		VisualVerbs:        sortedWordSet(profile.VisualVerbs),
		MinWords:           profile.PhrasePolicy.MinWords,
		MaxWords:           profile.PhrasePolicy.MaxWords,
		MaxResults:         profile.PhrasePolicy.MaxResults,
		RejectVerbsWhenAll: profile.PhrasePolicy.RejectVerbsWhenAll,
	}
}

func sortedWordSet(words map[string]struct{}) []string {
	if len(words) == 0 {
		return nil
	}
	out := make([]string, 0, len(words))
	for word := range words {
		if word = strings.TrimSpace(word); word != "" {
			out = append(out, word)
		}
	}
	sort.Strings(out)
	return out
}
