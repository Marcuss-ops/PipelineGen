package scripts

import (
	"context"
	"time"
)

// ScriptRepository is the canonical persistence contract for scripts.
// The concrete implementation lives in
// internal/infrastructure/database/sqlite (path TBD post-66c646b5).
// Declaring it here decouples engine.go from the concrete repository
// and makes engine_test.go cheap to write.
//
// Pre-66c646b5, the contract was inline in engine.go but reproduced
// here with a minimal 5-method surface so the build closure of the
// application package doesn't pull in the full infra layer.
type ScriptRepository interface {
	SaveScript(ctx context.Context, rec *ScriptRecord, sections []ScriptSectionRecord, matches []ScriptStockMatchRecord) (int64, error)
	SaveGenerationLog(ctx context.Context, log ScriptGenerationLog) error
	SaveOutlineSections(ctx context.Context, scriptID int64, sections []ScriptOutlineSectionRecord) error
	SaveResearchSources(ctx context.Context, scriptID int64, sources []ScriptResearchSource) error
	NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error)
}

// ScriptRecord is the canonical row of the scripts table — identity +
// final output. Sections/matches live in their own child tables (see
// ScriptSectionRecord + ScriptStockMatchRecord); they are not embedded
// in this struct to avoid JSON-array columns on the SQL side.
//
// Note: this struct was previously declared inline in engine.go and
// in types.go; the closure of Stage 2D consolidates it here as a stable
// contract for engine_test.go and the future concrete repository.
type ScriptRecord struct {
	ID         int64
	Title      string
	Topic      string
	Language   string
	Tone       string
	Model      string
	Status     string
	WordCount  int
	OutputText string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ScriptSectionRecord is one row of the script_sections child table.
// Kind discriminates ("preamble", "scene_narration", "cta", ...).
type ScriptSectionRecord struct {
	ScriptID    int64
	Index       int
	Kind        string
	ContentText string
	WordCount   int
}

// ScriptStockMatchRecord maps a script to a stock clip picked from the
// stock pipeline's picker. Score is the relevance signal from the
// matcher; Reason is a short human-readable justification.
type ScriptStockMatchRecord struct {
	ScriptID int64
	ClipID   string
	Score    float64
	Reason   string
}

// ScriptResearchSource is one row of the script_research_sources child
// table — every external source the writer LLM referenced so QA can
// reproduce the research path.
type ScriptResearchSource struct {
	ScriptID int64
	Source   string
	URL      string
	Excerpt  string
}

// ScriptOutlineSectionRecord is one row of the outline_sections child
// table — the pre-write structural plan the editor LLM produces, matched
// 1-1 with ScriptSectionRecord on Index after generation.
type ScriptOutlineSectionRecord struct {
	ScriptID int64
	Index    int
	Title    string
	Summary  string
	Actor    string
}

// ScriptGenerationLog is one row of the script_generation_logs audit
// table — every pipeline phase emits a row so operators can correlate
// retries / cache hits / errors.
type ScriptGenerationLog struct {
	ScriptID    int64
	Phase       string
	PromptHash  string
	Model       string
	InputWords  int
	OutputWords int
	DurationMs  int64
	RetryCount  int
	CacheStatus string
	Error       string
}

// ScriptGenerationPlan is the pre-write structured outline emitted by
// the planner LLM. The writer consumes it scene-by-scene.
type ScriptGenerationPlan struct {
	Topic      string
	Language   string
	Tone       string
	Scenes     []PlannedScene
	TotalWords int
	Version    int
}

// PlannedScene is one entry of the ScriptGenerationPlan.Scenes slice.
type PlannedScene struct {
	Index       int
	Kind        string
	Title       string
	Summary     string
	TargetWords int
}

// ResearchPack is the structured research summary emitted by the
// research LLM pass before the writer LLM is invoked. research.go's
// ParseResearchPack function unmarshals the agent output into this
// shape; FormatResearchContext renders it back to text for the writer.
type ResearchPack struct {
	RawText   string
	Topic     string
	Summary   string
	Sources   []ScriptResearchSource
	WordCount int
	CreatedAt time.Time
}
