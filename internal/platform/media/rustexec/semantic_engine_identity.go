package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type localizedSourceEntityJSON struct {
	Kind          string `json:"kind"`
	CanonicalName string `json:"canonical_name,omitempty"`
	Text          string `json:"text,omitempty"`
}

type localizedVisualPersonJSON struct {
	Text string `json:"text"`
}

type localizedIdentityMatchJSON struct {
	SourceIndex int    `json:"source_index"`
	Kind        string `json:"kind"`
	StartRune   int    `json:"start_rune"`
	EndRune     int    `json:"end_rune"`
	Surface     string `json:"surface"`
}

type localizeIdentitiesRequest struct {
	Operation string                       `json:"operation"`
	Mode      string                       `json:"mode"`
	Language  string                       `json:"language"`
	Text      string                       `json:"text"`
	Sources   []localizedSourceEntityJSON  `json:"sources"`
	Visual    []localizedVisualPersonJSON  `json:"visual,omitempty"`
	Existing  []localizedIdentityMatchJSON `json:"existing,omitempty"`
}

type localizeIdentitiesResponse struct {
	OK      bool                         `json:"ok"`
	Error   string                       `json:"error"`
	Matches []localizedIdentityMatchJSON `json:"matches"`
}

// MatchLocalizedIdentities grounds SOURCE identities in translated text. Rust
// owns exact matching, person inflection and span projection; Go only carries
// the source identity forward.
func (a *SemanticEngineAdapter) MatchLocalizedIdentities(
	ctx context.Context,
	language, text string,
	sources []scriptgen.LocalizedSourceEntity,
) ([]scriptgen.LocalizedIdentityMatch, error) {
	return a.localizeIdentities(ctx, "match", language, text, sources, nil, nil)
}

// CompleteLocalizedIdentities applies the ru/tr/pl ordered-person fallback.
// Completion only fills still-unmatched people when the translated NER proves
// exactly one non-overlapping candidate per person.
func (a *SemanticEngineAdapter) CompleteLocalizedIdentities(
	ctx context.Context,
	language, text string,
	sources []scriptgen.LocalizedSourceEntity,
	visual []string,
	existing []scriptgen.LocalizedIdentityMatch,
) ([]scriptgen.LocalizedIdentityMatch, error) {
	return a.localizeIdentities(ctx, "complete", language, text, sources, visual, existing)
}

func (a *SemanticEngineAdapter) localizeIdentities(
	ctx context.Context,
	mode, language, text string,
	sources []scriptgen.LocalizedSourceEntity,
	visual []string,
	existing []scriptgen.LocalizedIdentityMatch,
) ([]scriptgen.LocalizedIdentityMatch, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return nil, fmt.Errorf("semantic-engine: localized identity language is required")
	}
	request := localizeIdentitiesRequest{
		Operation: "localize_identities",
		Mode:      mode,
		Language:  language,
		Text:      text,
		Sources:   make([]localizedSourceEntityJSON, 0, len(sources)),
		Visual:    make([]localizedVisualPersonJSON, 0, len(visual)),
		Existing:  make([]localizedIdentityMatchJSON, 0, len(existing)),
	}
	for _, source := range sources {
		request.Sources = append(request.Sources, localizedSourceEntityJSON{
			Kind:          source.Kind,
			CanonicalName: source.CanonicalName,
			Text:          source.Text,
		})
	}
	for _, value := range visual {
		if strings.TrimSpace(value) == "" {
			continue
		}
		request.Visual = append(request.Visual, localizedVisualPersonJSON{Text: value})
	}
	for _, match := range existing {
		request.Existing = append(request.Existing, localizedIdentityMatchJSON{
			SourceIndex: match.SourceIndex,
			Kind:        match.Kind,
			StartRune:   match.StartRune,
			EndRune:     match.EndRune,
			Surface:     match.Surface,
		})
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: encode localized identity: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: localized identity execute: %w", err)
	}
	var response localizeIdentitiesResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine: localized identity decode: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine: localized identity worker: %s", strings.TrimSpace(response.Error))
	}

	runes := []rune(text)
	out := make([]scriptgen.LocalizedIdentityMatch, 0, len(response.Matches))
	for index, match := range response.Matches {
		if match.SourceIndex < 0 || match.SourceIndex >= len(sources) {
			return nil, fmt.Errorf("semantic-engine: localized identity match %d has source index %d out of range", index, match.SourceIndex)
		}
		if strings.TrimSpace(match.Kind) == "" || strings.TrimSpace(match.Surface) == "" {
			return nil, fmt.Errorf("semantic-engine: localized identity match %d is missing kind or surface", index)
		}
		if match.StartRune < 0 || match.EndRune <= match.StartRune || match.EndRune > len(runes) {
			return nil, fmt.Errorf("semantic-engine: localized identity match %d has invalid span %d..%d", index, match.StartRune, match.EndRune)
		}
		if got := string(runes[match.StartRune:match.EndRune]); got != match.Surface {
			return nil, fmt.Errorf("semantic-engine: localized identity match %d surface %q does not match text span %q", index, match.Surface, got)
		}
		out = append(out, scriptgen.LocalizedIdentityMatch{
			SourceIndex: match.SourceIndex,
			Kind:        match.Kind,
			StartRune:   match.StartRune,
			EndRune:     match.EndRune,
			Surface:     match.Surface,
		})
	}
	return out, nil
}
