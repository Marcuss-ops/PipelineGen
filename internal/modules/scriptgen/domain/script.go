// Package domain contains the canonical domain types for the scriptgen
// module.
//
// These types describe WHAT a script is (a generated narrative) and NEVER
// import concrete adapters (SQLite, Gin, Google Drive, Qdrant, Ollama).
// The package is leaf-only with respect to other modules — only stdlib.
// Conversion to/from the legacy internal/domain/script types happens
// in the application layer, never here.
package domain

import "time"

// Script is the central artifact produced by the scriptgen pipeline.
//
// It mirrors the canonical Script type in internal/domain/script but is
// intentionally kept independent to avoid import cycles; Agent 1 will
// place explicit conversion helpers at the boundary.
type Script struct {
	ID             int64     `json:"id"`
	Topic          string    `json:"topic"`
	Title          string    `json:"title"`
	Language       string    `json:"language"`
	Tone           string    `json:"tone"`
	Style          string    `json:"style"`
	Template       string    `json:"template"`
	Mode           string    `json:"mode"`
	Status         string    `json:"status"`
	WordCount      int       `json:"word_count"`
	TargetWords    int       `json:"target_words"`
	Duration       int       `json:"duration"`
	Version        int       `json:"version"`
	ParentScriptID *int64    `json:"parent_script_id,omitempty"`
	IsDeleted      bool      `json:"is_deleted"`
	OllamaBaseURL  string    `json:"ollama_base_url,omitempty"`
	ModelUsed      string    `json:"model_used,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Status values stored in Script.Status. Producers and consumers should
// use the constants below rather than bare string literals.
const (
	ScriptStatusDraft     = "draft"
	ScriptStatusPlanned   = "planned"
	ScriptStatusWritten   = "written"
	ScriptStatusEdited    = "edited"
	ScriptStatusQA        = "qa"
	ScriptStatusCompleted = "completed"
	ScriptStatusFailed    = "failed"
)
