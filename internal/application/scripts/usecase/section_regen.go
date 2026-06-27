// Package scripts: section_regen is the "regenerate a single section" use
// case. Pre-PR4.F (June 2026) the logic lived inline in
// api/script/handler_flow_ops.go::RegenerateSection, mixing HTTP parsing,
// prompt construction, Ollama invocation, persistence, doc re-upload,
// and serialization all in one 120-line Gin handler.
package usecase

import (
	"context"
	"errors"
)

// ── Repository contract types (PR 4 — scriptado Section regen) ────────────

// ScriptRecord is the canonical scripts row used by SectionRegenerator.
type ScriptRecord struct {
	ID           int64
	Topic        string
	Language     string
	Template     string
	ModelUsed    string
	MetadataJSON string
	Duration     int
}

// ScriptSectionRecord is the canonical scripts_sections row used by
// SectionRegenerator.
type ScriptSectionRecord struct {
	ID           int64
	ScriptID     int64
	SectionTitle string
	Content      string
	SortOrder    int
}

// SectionRegenerator is the use-case for regenerating a single script section.
// Phase 1b stub: real implementation was in the old mega-package.
type SectionRegenerator struct {
	repo ScriptRepository
	gen  any
	doc  any
	cfg  any
	log  any
}

// NewSectionRegenerator constructs a SectionRegenerator.
// Phase 1b stub.
func NewSectionRegenerator(repo ScriptRepository, gen any, doc any, cfg any, log any) *SectionRegenerator {
	return &SectionRegenerator{repo: repo, gen: gen, doc: doc, cfg: cfg, log: log}
}

// SectionRegenRequest is the request type for section regeneration.
type SectionRegenRequest struct {
	SectionID   int64  `json:"section_id"`
	ScriptID    int64  `json:"script_id"`
	RegenPrompt string `json:"regen_prompt,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Model       string `json:"model,omitempty"`
}

// ErrSectionNotFound is returned when a section is not found.
var ErrSectionNotFound = errors.New("section not found")

// ErrScriptNotFound is returned when a script is not found.
var ErrScriptNotFound = errors.New("script not found")

// ErrSectionScriptMismatch is returned when a section does not belong to the script.
var ErrSectionScriptMismatch = errors.New("section does not belong to the specified script")

// ErrEmptyGeneratorOutput is returned when the generator produces empty output.
var ErrEmptyGeneratorOutput = errors.New("generator produced empty output")

// SectionRegenResult is the result of a section regeneration.
type SectionRegenResult struct {
	SectionID int64  `json:"section_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

// Regenerate is a stub (Phase 1b).
func (s *SectionRegenerator) Regenerate(ctx context.Context, req SectionRegenRequest) (*SectionRegenResult, error) {
	return nil, errors.New("section regeneration not implemented (Phase 1b stub)")
}
