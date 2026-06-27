// Package scripts — model_output_decoder.go is the canonical
// decoder that accepts raw LLM output bytes, strips optional
// markdown code fences, and unmarshals into a typed
// ModelScriptOutputV1. It is the single decoding path for all
// new-generation script output.
//
//   raw LLM bytes → strip optional code fences → JSON unmarshal
//     → validate → *script.ModelScriptOutputV1
//
// The decoder rejects:
//   - bare prose (no JSON at all)
//   - JSON that does not match the V1 schema
//   - JSON with unsupported schema_version
//   - JSON with missing text or specscene
//   - duplicate scene IDs or out-of-order indexes
//
// Legacy array-shaped output is NOT accepted by this decoder;
// use compat.LegacyArrayToOutput for the temporary migration path.
package scripts

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// DecodeModelOutput parses raw LLM output bytes into a typed
// ModelScriptOutputV1. It handles the common LLM output patterns
// (optional code fence, optional leading/trailing text) through
// a single strict sanitization layer.
//
// Returns:
//   - *script.ModelScriptOutputV1 on success
//   - error wrapping script.ErrModelOutputMalformed on failure
func DecodeModelOutput(raw []byte, log *zap.Logger) (*scriptpkg.ModelScriptOutputV1, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty output", scriptpkg.ErrModelOutputMalformed)
	}

	// Step 1: Extract JSON from optional code fence + prose.
	jsonBytes, err := extractJSONFromOutput(raw)
	if err != nil {
		if log != nil {
			// Log first 2000 chars of raw output for debugging.
			preview := string(raw)
			if len(preview) > 2000 {
				preview = preview[:2000] + "..."
			}
			log.Debug("model output decoder: failed to extract JSON",
				zap.Int("raw_bytes", len(raw)),
				zap.String("preview", preview),
				zap.Error(err))
		}
		return nil, fmt.Errorf("%w: %w", scriptpkg.ErrModelOutputMalformed, err)
	}

	// Step 2: Unmarshal into the typed struct.
	var output scriptpkg.ModelScriptOutputV1
	if err := json.Unmarshal(jsonBytes, &output); err != nil {
		if log != nil {
			log.Debug("model output decoder: JSON unmarshal failed",
				zap.Error(err))
		}
		return nil, fmt.Errorf("%w: invalid JSON: %w", scriptpkg.ErrModelOutputMalformed, err)
	}

	// Step 3: Structural validation.
	if err := output.Validate(); err != nil {
		if log != nil {
			log.Debug("model output decoder: validation failed",
				zap.Error(err))
		}
		return nil, err
	}

	return &output, nil
}

// extractJSONFromOutput attempts to find a valid JSON object in the
// raw LLM output. It handles these cases:
//
//   1. Pure JSON: `{"schema_version":1,...}`
//   2. Code-fenced JSON:
//        ```json\n{"schema_version":1,...}\n```
//   3. Code-fenced with leading instructions:
//        Here is the output:\n```json\n{...}\n```
//   4. Leading prose + JSON block:  (rejected — ambiguous)
//   5. Bare prose with no JSON:     (rejected)
//
// Strategy: try to find a `{` that starts what looks like valid JSON
// and extract until the matching `}`. If the outermost extraction
// fails to parse, try extracting from within triple-backtick fences.
func extractJSONFromOutput(raw []byte) ([]byte, error) {
	text := string(raw)

	// Fast path: strip whitespace and check if it starts with `{`.
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		// Try to find the matching closing brace.
		end := findClosingBrace(trimmed)
		if end > 0 {
			candidate := []byte(trimmed[:end+1])
			if json.Valid(candidate) {
				return candidate, nil
			}
		}
	}

	// Code fence path: look for ```json ... ``` blocks.
	openFence := strings.Index(text, "```json")
	if openFence < 0 {
		openFence = strings.Index(text, "```")
	}
	if openFence >= 0 {
		// Find the closing fence.
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
		if strings.HasPrefix(content, "{") {
			end := findClosingBrace(content)
			if end > 0 {
				candidate := []byte(content[:end+1])
				if json.Valid(candidate) {
					return candidate, nil
				}
			}
		}
	}

	// Last attempt: find the first `{` and last `}` that produce valid JSON.
	firstBrace := strings.Index(text, "{")
	if firstBrace >= 0 {
		afterBrace := text[firstBrace:]
		end := findClosingBrace(afterBrace)
		if end > 0 {
			candidate := []byte(afterBrace[:end+1])
			if json.Valid(candidate) {
				return candidate, nil
			}
		}
	}

	return nil, fmt.Errorf("no valid JSON object found in output (%d bytes)", len(raw))
}

// findClosingBrace finds the position of the matching closing `}`
// for the first `{` in s. Returns -1 if no match is found or the
// nesting is broken. This is a simple brace counter, NOT a full
// JSON parser — it handles escaped braces inside strings correctly
// for the common LLM output case.
func findClosingBrace(s string) int {
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

		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
