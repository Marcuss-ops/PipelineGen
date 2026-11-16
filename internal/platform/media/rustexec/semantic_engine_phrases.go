package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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

type alignPhrasesWireRequest struct {
	Operation      string                             `json:"operation"`
	Words          []capabilityaudio.SpeechWordTiming `json:"words"`
	Phrases        []string                           `json:"phrases"`
	AllowMissing   bool                               `json:"allow_missing,omitempty"`
	AllOccurrences bool                               `json:"all_occurrences,omitempty"`
}

type alignPhrasesWireResponse struct {
	OK         bool                            `json:"ok"`
	Error      string                          `json:"error,omitempty"`
	Alignments []capabilityaudio.LocatedPhrase `json:"alignments"`
}

// AlignPhrases is the production boundary of the Rust deterministic word /
// phrase matcher. The artifact is validated here, and every returned span must
// reference the requested word boundaries; otherwise the call fails closed.
func (a *SemanticEngineAdapter) AlignPhrases(ctx context.Context, request scriptgen.PhraseAlignmentRequest) ([]capabilityaudio.LocatedPhrase, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	if err := request.Timing.Validate(); err != nil {
		return nil, fmt.Errorf("semantic-engine: align_phrases timing: %w", err)
	}
	if len(request.Phrases) == 0 {
		return nil, fmt.Errorf("semantic-engine: align_phrases phrases are required")
	}
	wire := alignPhrasesWireRequest{
		Operation:      "align_phrases",
		Words:          request.Timing.Words,
		Phrases:        request.Phrases,
		AllowMissing:   request.AllowMissing,
		AllOccurrences: request.AllOccurrences,
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: encode align_phrases: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: align_phrases execute: %w", err)
	}
	var response alignPhrasesWireResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine: decode align_phrases: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine: align_phrases worker: %s", strings.TrimSpace(response.Error))
	}
	if !request.AllowMissing && !request.AllOccurrences {
		if len(response.Alignments) != len(request.Phrases) {
			return nil, fmt.Errorf("semantic-engine: align_phrases returned %d alignments for %d phrases", len(response.Alignments), len(request.Phrases))
		}
		for i, alignment := range response.Alignments {
			if strings.TrimSpace(alignment.Text) != strings.TrimSpace(request.Phrases[i]) {
				return nil, fmt.Errorf("semantic-engine: align_phrases returned %q for phrase %q", alignment.Text, request.Phrases[i])
			}
		}
	}
	if request.AllowMissing || request.AllOccurrences {
		requested := make(map[string]struct{}, len(request.Phrases))
		for _, phrase := range request.Phrases {
			requested[normalizeAlignmentText(phrase)] = struct{}{}
		}
		for _, alignment := range response.Alignments {
			if _, ok := requested[normalizeAlignmentText(alignment.Text)]; !ok {
				return nil, fmt.Errorf("semantic-engine: align_phrases returned unrequested phrase %q", alignment.Text)
			}
		}
	}
	for _, alignment := range response.Alignments {
		if alignment.WordStart < 0 || alignment.WordEnd < alignment.WordStart || alignment.WordEnd >= len(request.Timing.Words) {
			return nil, fmt.Errorf("semantic-engine: align_phrases returned invalid word span %d..%d for %q", alignment.WordStart, alignment.WordEnd, alignment.Text)
		}
		first := request.Timing.Words[alignment.WordStart]
		last := request.Timing.Words[alignment.WordEnd]
		if alignment.StartUS != first.StartUS || alignment.EndUS != last.EndUS {
			return nil, fmt.Errorf("semantic-engine: align_phrases timestamps do not match the requested words for %q", alignment.Text)
		}
	}
	return response.Alignments, nil
}

func normalizeAlignmentText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

var (
	_ scriptgen.PhraseSelector = (*SemanticEngineAdapter)(nil)
	_ scriptgen.PhraseAligner  = (*SemanticEngineAdapter)(nil)
)
