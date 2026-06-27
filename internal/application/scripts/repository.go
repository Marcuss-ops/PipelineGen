package scripts

import (
	"context"
	"time"
)

// ScriptRepository is the canonical persistence contract for scripts.
// The concrete implementation lives in
// internal/infrastructure/database/sqlite (path TBD post-66c646b5).
// Declaring it here decouples the persistence consumer from the
// concrete repository and makes engine_test.go / persistence_test.go
// cheap to write.
//
// Pre-66c646b5, the contract was inline in engine.go but reproduced
// here with a minimal method surface so the build closure of the
// application package doesn't pull in the full infra layer.
//
// PR 5 (June 2026): added FindScriptByIdempotencyKey — the single
// persistence owner (PersistenceProcessor) computes an idempotency
// key from (item_id, cache_key, prompt_version, target_words, language)
// and looks up an existing row by it; on hit, the insert is skipped
// and the existing ScriptID is returned. The Engine no longer
// touches this interface — the only writer is PersistenceProcessor.
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

	// FindScriptByIdempotencyKey returns the existing script row
	// (if any) whose idempotency key matches the reconciliation
	// tuple (itemID, cacheKey, promptVersion, targetWords, language).
	// The bool return is the existence flag — callers do not treat
	// nil record + false as an error. A nil record with non-nil err
	// indicates a real lookup failure (e.g. SQL error).
	//
	// PR 5 (June 2026): required by PersistenceProcessor for the
	// single-writer contract. Implementations may use a dedicated
	// column or an existing slot (current implementation uses the
	// `template` slot to carry the idem key — PR 6 will introduce
	// a dedicated idempotency_key column).
	FindScriptByIdempotencyKey(ctx context.Context, itemID, cacheKey, promptVersion string, targetWords int, language string) (*ScriptRecord, bool, error)
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

// ScriptRecord is the canonical row of the scripts table — identity +
// final output. Sections/matches live in their own child tables (see
// ScriptSectionRecord + ScriptStockMatchRecord); they are not embedded
// in this struct to avoid JSON-array columns on the SQL side.
//
// PR 6 (June 2026): dedicated IdempotencyKey + SpecScene fields replace
// the pre-PR-6 dual-purpose Template / TimelineJSON slots — both
// fields are written by PersistenceProcessor (the only writer) into
// the dedicated columns on the SQL side. The Template field is
// retained for downstream ListScripts filters (semantic-history
// preservation). The TimelineJSON slot is retained as legacy
// compatibility (the adapter still passes it through but does not
// populate it as SpecScene JSON any longer).
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

	// IdempotencyKey is the 16-hex-char SHA-256 prefix computed by
	// PersistenceProcessor from the reconciliation tuple
	// (item_id|cache_key|prompt_version|target_words|language). PR 6:
	// stored on the dedicated `idempotency_key TEXT` column. The
	// adapter passes it through transparently.
	IdempotencyKey string

	// SpecScene is the JSON-serialised SpecSceneOutput emitted by
	// the engine. PR 6: stored on the dedicated `specscene TEXT`
	// column; the pre-PR-6 path of stuffing SpecScene JSON into the
	// TimelineJSON slot is gone.
	SpecScene string
}
