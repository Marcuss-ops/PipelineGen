// Package script — model_output.go defines the canonical structured
// model output envelope for script generation.
package script

import (
	"fmt"
	"strings"
)

// ModelScriptOutputV1 is the canonical structured output the LLM
// must return for every script generation. SchemaVersion is always 1
// for this contract; the decoder validates and rejects unknown
// versions.
//
// JSON shape (model-emitted):
//
//	{
//	  "schema_version": 1,
//	  "text": "Complete generated script...",
//	  "specscene": { "version": 1, "scenes": [...] }
//	}
//
// PR 3 (June 2026): WordCount / ModelUsed / CacheStatus are
// engine-stamped provenance fields, NOT part of the model-emitted
// JSON shape. The decoder ignores them on read; the engine sets them
// in-place after decoding so that processors (which receive the
// canonical typed MSOV1) can read WordCount / ModelUsed / CacheStatus
// uniformly without extra wrapping.
type ModelScriptOutputV1 struct {
	// SchemaVersion is the version of this output contract.
	// Currently always 1.
	SchemaVersion int `json:"schema_version"`

	// Text is the complete generated script prose. Must be non-empty.
	Text string `json:"text"`

	// SpecScene is the structured scene breakdown. Always present;
	// may contain zero scenes for pure prose generation.
	SpecScene SpecSceneOutput `json:"specscene"`

	// WordCount is the model's reported token count, stamped by
	// the engine post-decode. The pre-PR-3 ProcessInput envelope
	// carried this as a separate field; the PR 3 typed walk
	// surfaces it on the model directly. omitempty so the
	// model-emitted JSON shape is unaffected.
	WordCount int `json:"word_count,omitempty"`

	// ModelUsed is the engine's provenance stamp for which
	// model produced this output ("llama3:8b", "qwen2.5:14b",
	// ""). omitempty.
	ModelUsed string `json:"model_used,omitempty"`

	// CacheStatus is "exact_hit" (memory gate hit) or
	// "generated". omitempty.
	CacheStatus string `json:"cache_status,omitempty"`
}

// Validate checks structural invariants. Returns a ModelOutputError
// with structured details on failure, or nil when the output is
// structurally valid.
//
// Rules:
//   - SchemaVersion must be 1.
//   - Text must be non-empty (after trimming whitespace).
//   - SpecScene must be valid (specscene version, scenes, indexes).
func (m *ModelScriptOutputV1) Validate() error {
	var details []string

	if m.SchemaVersion != 1 {
		details = append(details,
			fmt.Sprintf("unsupported schema_version %d (expected 1)", m.SchemaVersion))
	}
	if strings.TrimSpace(m.Text) == "" {
		details = append(details, "text is required and must be non-empty")
	}
	if err := m.SpecScene.Validate(); err != nil {
		if me, ok := err.(*ModelOutputError); ok {
			details = append(details, "specscene: "+strings.Join(me.Details, "; "))
		} else {
			details = append(details, "specscene: "+err.Error())
		}
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}
