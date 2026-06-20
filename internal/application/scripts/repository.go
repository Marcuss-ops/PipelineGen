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
// here with a minimal method surface so the build closure of the
// application package doesn't pull in the full infra layer.
type ScriptRepository interface {
	SaveScript(ctx context.Context, rec *ScriptRecord, sections []ScriptSectionRecord, matches []ScriptStockMatchRecord) (int64, error)
	UpdateScriptFinalContent(ctx context.Context, scriptID int64, outputText string, wordCount int, status, metadata, model, ollamaBaseURL string, version int) error
	SaveGenerationLog(ctx context.Context, log ScriptGenerationLog) error
	SaveOutlineSections(ctx context.Context, scriptID int64, sections []ScriptOutlineSectionRecord) error
	SaveResearchSources(ctx context.Context, scriptID int64, sources []ScriptResearchSource) error
	NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error)
	GetSectionByID(ctx context.Context, sectionID int64) (*ScriptSectionRecord, error)
	GetScriptByID(id int64) (*ScriptRecord, []ScriptSectionRecord, []ScriptStockMatchRecord, error)
	GetAdjacentSections(ctx context.Context, scriptID int64, sortOrder int) (prev, next *ScriptSectionRecord, err error)
	UpdateSectionContent(ctx context.Context, sectionID int64, content string) error
	ListScripts(ctx context.Context, filter ScriptListFilter) ([]*ScriptRecord, error)
}

// ScriptListFilter is the filter for listing scripts.
type ScriptListFilter struct {
	Topic    string
	Language string
	Status   string
	Limit    int
	Offset   int
}

// ScriptSectionRecord is one row of the script_sections child table.
// Kind discriminates ("preamble", "scene_narration", "cta", ...).
type ScriptSectionRecord struct {
	ID           int64
	ScriptID     int64
	Index        int
	SectionType  string
	SectionTitle string
	Kind         string
	Content      string
	ContentText  string
	WordCount    int
	SortOrder    int
	Status       string
}

// ScriptStockMatchRecord maps a script to a stock clip picked from the
// stock pipeline's picker. Score is the relevance signal from the
// matcher; Reason is a short human-readable justification.
type ScriptStockMatchRecord struct {
	ID           int64
	ScriptID     int64
	ClipID       string
	Score        float64
	Reason       string
	SegmentIndex int
	StockPath    string
	StockSource  string
	MatchedTerms string
}

// ScriptResearchSource is one row of the script_research_sources child
// table — every external source the writer LLM referenced so QA can
// reproduce the research path.
type ScriptResearchSource struct {
	ScriptID       int64
	Source         string
	Query          string
	URL            string
	Title          string
	Snippet        string
	Excerpt        string
	SourceType     string
	UsedInSections string
	RelevanceScore float64
}

// ScriptOutlineSectionRecord is one row of the outline_sections child
// table — the pre-write structural plan the editor LLM produces, matched
// 1-1 with ScriptSectionRecord on Index after generation.
type ScriptOutlineSectionRecord struct {
	ScriptID      int64
	SectionIndex  int
	Index         int
	Title         string
	Summary       string
	Actor         string
	Purpose       string
	TargetWords   int
	KeyPointsJSON string
	EmotionalRole string
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
	Title              string
	Topic              string
	Language           string
	Tone               string
	Model              string
	Mode               string
	ChannelID          string
	UseMemory          bool
	ForceRefresh        bool
	SaveToDB           bool
	TargetWords        int
	Duration           int
	DurationMin        int
	NumPredict         int
	Temperature        float64
	Prompt             string
	SourceText         string
	WebContext         string
	Guidelines         string
	PromptVersion      string
	EditorPromptVersion string
	QAPromptVersion    string
	Scenes             []PlannedScene
	TotalWords         int
	Version            int
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
	ID             int64
	Title          string
	Topic          string
	Language       string
	Tone           string
	Model          string
	ModelUsed      string
	Template       string
	Mode           string
	Status         string
	WordCount      int
	TargetWords    int
	FinalWordCount int
	OutputText     string
	NarrativeText  string
	FullDocument   string
	MetadataJSON   string
	TimelineJSON   string
	EntitiesJSON   string
	ParentScriptID int64
	Duration       int
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
	RawText        string
	Topic          string
	Summary        string
	KeyFacts       []string           `json:"key_facts,omitempty"`
	Timeline       []TimelineEntry    `json:"timeline,omitempty"`
	Controversies  []string           `json:"controversies,omitempty"`
	ImportantQuotes []string          `json:"important_quotes,omitempty"`
	SuggestedAngles []string          `json:"suggested_angles,omitempty"`
	Warnings       []string           `json:"warnings,omitempty"`
	Sources        []ScriptResearchSource
	WordCount      int
	CreatedAt      time.Time
}

// TimelineEntry is a single item in the research timeline.
type TimelineEntry struct {
	Date   string `json:"date,omitempty"`
	Event  string `json:"event"`
	Source string `json:"source,omitempty"`
}
