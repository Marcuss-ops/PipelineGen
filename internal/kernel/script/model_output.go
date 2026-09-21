// Package script — model_output.go defines the canonical structured
// model output envelope for script generation AND its failure
// contract: the ErrModelOutputMalformed sentinel plus the
// ModelOutputError detail carrier.
//
// The envelope and its error shape are ONE contract — an output is
// never simply valid-or-invalid, it is valid or it is a
// ModelOutputError with structured details — so they live in one
// file. They were previously split across model_output.go and
// model_output_errors.go; the package sat at 66 production files
// against max_files_per_package=65, and this cohesive pair is the
// natural merge rather than a cosmetic one.
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

// ErrModelOutputMalformed is the sentinel for any model-output
// decode or validation failure (malformed JSON, missing fields,
// unsupported schema version).
var ErrModelOutputMalformed = fmt.Errorf("script: model output malformed")

// ModelOutputError carries the structured details behind
// ErrModelOutputMalformed.
type ModelOutputError struct {
	Details []string
}

func (e *ModelOutputError) Error() string {
	if e == nil || len(e.Details) == 0 {
		return ErrModelOutputMalformed.Error()
	}
	return fmt.Sprintf("%s: %s", ErrModelOutputMalformed.Error(), strings.Join(e.Details, "; "))
}

func (e *ModelOutputError) Unwrap() error { return ErrModelOutputMalformed }
