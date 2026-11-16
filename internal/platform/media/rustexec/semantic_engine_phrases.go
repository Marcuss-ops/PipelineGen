package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type selectPhrasesWireRequest struct {
	Operation        string                            `json:"operation"`
	Text             string                            `json:"text"`
	BlockedRuneSpans [][2]int                          `json:"blocked_rune_spans,omitempty"`
	Limit            int                               `json:"limit,omitempty"`
	MinWords         int                               `json:"min_words,omitempty"`
	MaxWords         int                               `json:"max_words,omitempty"`
	Corpus           []string                          `json:"corpus,omitempty"`
	Profile          *scriptgen.PhraseSelectionProfile `json:"profile,omitempty"`
}

type selectPhrasesWireResponse struct {
	OK      bool                       `json:"ok"`
	Error   string                     `json:"error,omitempty"`
	Phrases []scriptgen.SelectedPhrase `json:"phrases"`
}

// SelectPhrases is the production boundary of the Rust deterministic phrase
// selector. Every returned phrase must be the exact requested-text slice at
// its byte span; an ungrounded response fails closed.
func (a *SemanticEngineAdapter) SelectPhrases(ctx context.Context, request scriptgen.PhraseSelectionRequest) ([]scriptgen.SelectedPhrase, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	if strings.TrimSpace(request.Text) == "" {
		return nil, fmt.Errorf("semantic-engine: select_phrases text is required")
	}
	wire := selectPhrasesWireRequest{
		Operation:        "select_phrases",
		Text:             request.Text,
		BlockedRuneSpans: request.BlockedRuneSpans,
		Limit:            request.Limit,
		MinWords:         request.MinWords,
		MaxWords:         request.MaxWords,
		Corpus:           request.Corpus,
		Profile:          request.Profile,
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: encode select_phrases: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: select_phrases execute: %w", err)
	}
	var response selectPhrasesWireResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine: decode select_phrases: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine: select_phrases worker: %s", strings.TrimSpace(response.Error))
	}
	text := request.Text
	for _, phrase := range response.Phrases {
		if phrase.StartByte < 0 || phrase.EndByte <= phrase.StartByte || phrase.EndByte > len(text) ||
			text[phrase.StartByte:phrase.EndByte] != phrase.Text {
			return nil, fmt.Errorf("semantic-engine: select_phrases returned an ungrounded phrase %q", phrase.Text)
		}
		if phrase.StartRune < 0 || phrase.EndRune <= phrase.StartRune ||
			phrase.EndRune > utf8.RuneCountInString(text) ||
			utf8.RuneCountInString(text[:phrase.StartByte]) != phrase.StartRune {
			return nil, fmt.Errorf("semantic-engine: select_phrases returned invalid rune spans for %q", phrase.Text)
		}
	}
	return response.Phrases, nil
}

var _ scriptgen.PhraseSelector = (*SemanticEngineAdapter)(nil)
