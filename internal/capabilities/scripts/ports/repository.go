// Package ports — repository.go holds the canonical ScriptRepository
// port interface and all related record types consumed by every script-
// layer consumer (engine, usecase, processor, handler, test fixture).
//
// P1.6 (June 2026): extracted from adapters/repository.go so the port
// lives in the canonical ports/ sub-package (ports are the seam —
// circular deps would defeat the abstraction). The previous adapters/
// home now re-exports these types via type aliases for back-compat.
//
// Import discipline (per doc.go):
//   - MUST NOT import other scripts/ sub-packages.
//   - MAY import internal/kernel/* and stdlib-only.
//   - Infrastructure implementations satisfy these ports via
//     implicit-interface patterns, never via direct import.
package ports

import (
	"context"
	"errors"
	"time"
)

// ScriptRepository is the canonical persistence contract for scripts.
// The concrete implementation lives in
// internal/platform/sqlite/scripts. Declaring it here
// decouples the persistence consumer from the concrete repository and
// makes engine_test.go / persistence_test.go cheap to write.
//
// PR 5 (June 2026): added FindScriptByIdempotencyKey — the single
// persistence owner (PersistenceProcessor) computes an idempotency
// key from (item_id, cache_key, prompt_version, target_words, language)
// and looks up an existing row by it; on hit, the insert is skipped
// and the existing ScriptID is returned. The Engine no longer
// touches this interface — the only writer is PersistenceProcessor.
//
// P1.6 (June 2026): moved from adapters/repository.go to the canonical
// ports/ sub-package. The interfaces-only rule (ports doc.go) remains
// in force — this interface and its record types are the public
// contract; concrete implementations live in infrastructure.
type ScriptRepository interface {
	SaveScript(ctx context.Context, rec *ScriptRecord, sections []ScriptSectionRecord, matches []ScriptStockMatchRecord) (int64, error)
	UpdateScriptFinalContent(ctx context.Context, scriptID int64, outputText string, wordCount int, status, metadata, model, ollamaBaseURL string, version int) error
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
	// single-writer contract.
	FindScriptByIdempotencyKey(ctx context.Context, itemID, cacheKey, promptVersion string, targetWords int, language string) (*ScriptRecord, bool, error)

	// SaveManifestV2 persists the canonical ManifestV2 envelope
	// (NEW-mode downstream fan-out manifest) bound to the given
	// scriptID. PR 1 (July 2026, SCRIPT-DOWNSTREAM-CUTOVER wave):
	// the canonical typed surface for the Step 11A downstream
	// fan-out; replaces the legacy inline voice/image collection
	// on the script manifest. Caller is the SOLE writer
	// (PersistenceProcessor) per godlike/06 one-canonical-owner-
	// per-fact — no other port consumer writes the manifest_v2
	// column.
	//
	// The manifest is JSON-marshalled into the dedicated
	// `manifest_v2 TEXT` column. NoInlineAssets=true is the
	// canonical NEW-mode marker. The port is fail-closed: an
	// empty manifest returns the canonical typed sentinel
	// ErrSaveManifestV2NilManifest (godlike/07 no-fake-availability
	// — silently writing `''` to the column would mask a
	// caller-side bug as a "successful empty write").
	//
	// Idempotency: the call is treated as an UPSERT keyed on
	// scriptID — a re-publish of the same manifest overwrites the
	// column. The UPDATE statement preserves the canonical
	// write-seam without violating godlike/07 fail-fast.
	//
	// The `manifest` argument is the pre-marshalled JSON bytes of
	// script.ManifestV2 (port alias ScriptManifestJSON). The
	// caller (PersistenceProcessor) marshals the typed
	// ManifestV2 to JSON via json.Marshal and passes the bytes
	// verbatim. The port stays decoupled from the
	// DownstreamRequest type tree; the concrete impl writes the
	// bytes to the `manifest_v2 TEXT` column directly.
	SaveManifestV2(ctx context.Context, scriptID int64, manifest ScriptManifestJSON) error
}

// ErrSaveManifestV2NilManifest is the canonical godlike/07 typed
// sentinel for the empty-manifest fail-closed path. The port is the
// SOLE canonical owner (godlike/06 one-canonical-owner-per-fact);
// the concrete's ErrSaveManifestV2Empty is an alias for
// defense-in-depth. Callers (PersistenceProcessor) probe with
// errors.Is to surface a typed diagnostic at composition time
// rather than masking the failure as a generic sql error.
var ErrSaveManifestV2NilManifest = errors.New("scripts repository: nil manifest_v2 (empty bytes would silently overwrite the column with an empty string — caller bug)")

// ScriptManifestJSON is the port-level alias for the
// pre-marshalled JSON payload of script.ManifestV2. Persistence
// Processor marshals the typed ManifestV2 to JSON, then passes the
// bytes verbatim. The port stays decoupled from the domain type
// tree (the canonical ManifestV2 + DownstreamRequest + nested
// AssetRequirements live in internal/kernel/script and are NOT
// imported by the port).
type ScriptManifestJSON = []byte

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
// VoiceoverLink is the Drive URL of the per-scene voiceover audio.
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

	// VoiceoverLink is the Google Drive URL of the per-scene voiceover
	// audio file, set by the voiceover postprocessor and stored in the
	// script_sections.voiceover_link column.
	VoiceoverLink string
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
// preservation).
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
	// stored on the dedicated `idempotency_key TEXT` column.
	IdempotencyKey string

	// SpecScene is the JSON-serialised SpecSceneOutput emitted by
	// the engine. PR 6: stored on the dedicated `specscene TEXT`
	// column.
	SpecScene string
}
