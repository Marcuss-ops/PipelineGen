package jsonextract

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Mode controls Scanner fallback behaviour. Fresh plain text is the sole
// canonical mode and intentionally owns the zero value.
type Mode int

const ModeFreshPlainText Mode = 0

func (m Mode) String() string {
	if m == ModeFreshPlainText {
		return "fresh_plain_text"
	}
	return "unknown"
}

// Scanner converts raw model output into the canonical V1 script envelope.
type Scanner struct {
	Mode Mode
}

func NewScanner(mode Mode) *Scanner { return &Scanner{Mode: mode} }

// Scan accepts a valid V1 JSON envelope or raw prose. JSON-shaped malformed
// input fails closed instead of being silently wrapped as prose.
func (s *Scanner) Scan(raw []byte, _ string) (*scriptpkg.ModelScriptOutputV1, error) {
	if s == nil {
		s = &Scanner{}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty output", scriptpkg.ErrModelOutputMalformed)
	}

	jsonBytes, extractErr := extractJSON(raw)
	if extractErr == nil {
		return decodeV1(jsonBytes)
	}
	if isLegacyJSONShape(string(raw)) {
		return nil, fmt.Errorf("%w: %v", scriptpkg.ErrModelOutputMalformed, extractErr)
	}
	return ParsePlainTextFresh(raw)
}

func decodeV1(jsonBytes []byte) (*scriptpkg.ModelScriptOutputV1, error) {
	var output scriptpkg.ModelScriptOutputV1
	if err := json.Unmarshal(jsonBytes, &output); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %w", scriptpkg.ErrModelOutputMalformed, err)
	}
	normalizeOutput(&output)
	if err := output.Validate(); err != nil {
		return nil, err
	}
	return &output, nil
}

func normalizeOutput(output *scriptpkg.ModelScriptOutputV1) {
	if output == nil {
		return
	}
	output.Text = normalizeTextField(output.Text)
	for i := range output.SpecScene.Scenes {
		output.SpecScene.Scenes[i].Text = normalizeTextField(output.SpecScene.Scenes[i].Text)
	}

	// Recover known double-wrapped model output: the outer text field may
	// itself contain a complete V1 envelope with the real scenes.
	if len(output.SpecScene.Scenes) == 0 && output.Text != "" {
		scanner := &Scanner{Mode: ModeFreshPlainText}
		if inner, err := scanner.Scan([]byte(output.Text), "double-wrap-recovery"); err == nil && len(inner.SpecScene.Scenes) > 0 {
			output.SpecScene = inner.SpecScene
			for i := range output.SpecScene.Scenes {
				output.SpecScene.Scenes[i].Text = normalizeTextField(output.SpecScene.Scenes[i].Text)
			}
			output.Text = normalizeTextField(inner.Text)
		}
	}
}

func normalizeTextField(text string) string {
	current := strings.TrimSpace(text)
	for i := 0; i < 2; i++ {
		if current == "" {
			return ""
		}
		if unquoted, ok := tryUnquoteJSONString(current); ok {
			current = strings.TrimSpace(unquoted)
			continue
		}
		if looksLikeJSON(current) {
			var inner scriptpkg.ModelScriptOutputV1
			if err := json.Unmarshal([]byte(current), &inner); err == nil && strings.TrimSpace(inner.Text) != "" {
				current = strings.TrimSpace(inner.Text)
				continue
			}
		}
		break
	}
	return current
}

func looksLikeJSON(text string) bool {
	text = strings.TrimSpace(text)
	return len(text) > 0 && (text[0] == '{' || text[0] == '[')
}

func tryUnquoteJSONString(text string) (string, bool) {
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return "", false
	}
	var unquoted string
	if err := json.Unmarshal([]byte(text), &unquoted); err != nil {
		return "", false
	}
	return unquoted, true
}
