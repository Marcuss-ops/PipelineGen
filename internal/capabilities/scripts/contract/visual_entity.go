package contract

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// VisualEntity is the common source-grounded entity returned by every NER
// backend and projected through the scene pipeline.
//
// It is part of the shared vocabulary: the scripts root, the vidrush package
// and the semantics projections all speak it, and BrainRust's VisualNER returns
// it over the FFI boundary. The pure helpers that operate on it live in
// scripts/semantics.
type VisualEntity struct {
	Text              string               `json:"text"`
	Type              scriptpkg.EntityType `json:"type"`
	Score             float32              `json:"score"`
	Start             int                  `json:"start"`
	End               int                  `json:"end"`
	Evidence          string               `json:"evidence,omitempty"`
	IdentityID        string               `json:"identity_id,omitempty"`
	CanonicalID       string               `json:"canonical_id,omitempty"`
	Role              string               `json:"role,omitempty"`
	SearchIntents     []string             `json:"search_intents,omitempty"`
	EditorialDecision bool                 `json:"editorial_decision,omitempty"`
	EditorialSurface  string               `json:"editorial_surface,omitempty"`
	EditorialType     string               `json:"editorial_type,omitempty"`
	PromptEcho        bool                 `json:"prompt_echo,omitempty"`
	VisualAdmission   bool                 `json:"visual_admission,omitempty"`
	// EntityIndex is the packaged analyzer index that produced the linked ids.
	EntityIndex string `json:"entity_index,omitempty"`
}
