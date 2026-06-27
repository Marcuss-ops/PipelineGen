// Package jsonextract — extractor.go provides the unified JSON extraction
// layer. It replaces the near-duplicate extractJSONFromOutput (from
// model_output_decoder.go) and extractArrayJSON (from
// compat/legacy_model_output_decoder.go) with a single function that
// handles both `{...}` and `[...]` shapes, optional code fences, and
// leading/trailing prose.
//
// The extraction strategy:
//   1. Fast path: trimmed text starts with `{` or `[` → find matching
//      close delimiter → validate → return.
//   2. Code-fence path: look for ```json ... ``` or ``` ... ``` blocks.
//   3. Last attempt: find first `{` or `[` with matching close.

package jsonextract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// extractJSON attempts to find a valid JSON object or array in raw
// LLM output bytes. Returns the extracted JSON bytes or an error.
//
// Tolerates:
//   - Leading/trailing prose around the JSON block
//   - Markdown code fences (```json ... ```)
//   - Both `{...}` (V1 object) and `[...]` (legacy array) shapes
func extractJSON(raw []byte) ([]byte, error) {
	text := string(raw)

	// Fast path: strip whitespace and check for leading delimiter.
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		open := trimmed[0]
		close := closingDelim(open)
		end := findMatchingDelim(trimmed, open, close)
		if end > 0 {
			candidate := []byte(trimmed[:end+1])
			if json.Valid(candidate) {
				return candidate, nil
			}
		}
	}

	// Code-fence path: look for ```json ... ``` or ``` ... ``` blocks.
	if jsonBytes := extractFromCodeFence(text); jsonBytes != nil {
		return jsonBytes, nil
	}

	// Last attempt: find the first `{` or `[` and its matching close.
	firstObj := strings.Index(text, "{")
	firstArr := strings.Index(text, "[")
	first := -1
	if firstObj >= 0 && firstArr >= 0 {
		if firstObj < firstArr {
			first = firstObj
		} else {
			first = firstArr
		}
	} else if firstObj >= 0 {
		first = firstObj
	} else if firstArr >= 0 {
		first = firstArr
	}

	if first >= 0 {
		open := text[first]
		close := closingDelim(open)
		after := text[first:]
		end := findMatchingDelim(after, open, close)
		if end > 0 {
			candidate := []byte(after[:end+1])
			if json.Valid(candidate) {
				return candidate, nil
			}
		}
	}

	return nil, fmt.Errorf("no valid JSON found in output (%d bytes)", len(raw))
}

// extractFromCodeFence attempts to find JSON inside ```json ... ```
// or ``` ... ``` code-fence blocks. Returns nil if no valid JSON
// is found inside a fence.
func extractFromCodeFence(text string) []byte {
	openFence := strings.Index(text, "```json")
	if openFence < 0 {
		openFence = strings.Index(text, "```")
	}
	if openFence < 0 {
		return nil
	}

	startJSON := strings.Index(text[openFence:], "\n")
	if startJSON < 0 {
		startJSON = 3 // length of ```
	}
	content := text[openFence+startJSON:]

	closeFence := strings.Index(content, "\n```")
	if closeFence < 0 {
		closeFence = strings.Index(content, "```")
	}
	if closeFence >= 0 {
		content = content[:closeFence]
	}

	content = strings.TrimSpace(content)
	if len(content) == 0 {
		return nil
	}
	if content[0] != '{' && content[0] != '[' {
		return nil
	}

	open := content[0]
	close := closingDelim(open)
	end := findMatchingDelim(content, open, close)
	if end > 0 {
		candidate := []byte(content[:end+1])
		if json.Valid(candidate) {
			return candidate
		}
	}
	return nil
}

// findMatchingDelim finds the position of the matching closing
// delimiter for the first opening delimiter in s. Handles
// string escaping and nested delimiters of both types (`{}` and
// `[]`). Accepts the open and close byte to support both shapes.
//
// Returns -1 if no match is found or nesting is broken.
func findMatchingDelim(s string, open, close byte) int {
	depth := 0
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}

		if ch == open {
			depth++
		} else if ch == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// closingDelim returns the matching closing delimiter for the given
// opening delimiter.
func closingDelim(open byte) byte {
	switch open {
	case '{':
		return '}'
	case '[':
		return ']'
	default:
		return 0
	}
}
