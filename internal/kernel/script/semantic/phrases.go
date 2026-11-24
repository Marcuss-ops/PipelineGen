package semantic

import "context"

// PhraseSelectionProfile is the lexicon data the Rust selector needs.
type PhraseSelectionProfile struct {
	StopWords          []string `json:"stop_words,omitempty"`
	FunctionWords      []string `json:"function_words,omitempty"`
	VisualVerbs        []string `json:"visual_verbs,omitempty"`
	MinWords           int      `json:"min_words,omitempty"`
	MaxWords           int      `json:"max_words,omitempty"`
	MaxResults         int      `json:"max_results,omitempty"`
	RejectVerbsWhenAll bool     `json:"reject_verbs_when_all"`
}

// PhraseSelectionRequest asks the Rust worker for source-grounded phrases.
type PhraseSelectionRequest struct {
	Text             string
	BlockedRuneSpans [][2]int
	Limit            int
	MinWords         int
	MaxWords         int
	Corpus           []string
	Profile          *PhraseSelectionProfile
}

// SelectedPhrase is one phrase returned by the Rust selector.
type SelectedPhrase struct {
	Text      string `json:"text"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	StartRune int    `json:"start_rune"`
	EndRune   int    `json:"end_rune"`
	Score     int64  `json:"score"`
}

// PhraseSelector is the single production seam for phrase selection.
type PhraseSelector interface {
	SelectPhrases(ctx context.Context, request PhraseSelectionRequest) ([]SelectedPhrase, error)
}
