package scripts

import "context"

// Repository is the canonical domain contract for script persistence.
// Implementations live in internal/repository/scripts.
//
// Services that need script access (Engine, script flow handlers,
// script history handler) MUST depend on this interface, NOT on the
// concrete ScriptRepository. This enables test doubles and
// keeps the domain layer decoupled from SQLite.
type Repository interface {
	// ── Core CRUD ──────────────────────────────────────────────────────

	// SaveScript creates a new script with its sections and stock matches
	// in a single transaction. Returns the new script ID.
	SaveScript(ctx context.Context, s *Script, sections []Section, stockMatches []StockMatch) (int64, error)

	// GetScriptByID returns a script with its sections and stock matches,
	// or nil if not found or soft-deleted.
	GetScriptByID(ctx context.Context, id int64) (*Script, []Section, []StockMatch, error)

	// ListScripts returns scripts matching the given filters, with total count.
	ListScripts(ctx context.Context, limit, offset int, language, template string) ([]Script, int, error)

	// FindByTopic returns the most recent script for a topic+language pair.
	FindByTopic(ctx context.Context, topic, language string) (*Script, []Section, []StockMatch, error)

	// SoftDeleteScript marks a script as deleted without removing it.
	SoftDeleteScript(ctx context.Context, id int64) error

	// UpdateScriptFinalContent updates the narrative text, word count, status,
	// metadata, model, and version for a completed script.
	UpdateScriptFinalContent(ctx context.Context, id int64, text string, wordCount int, status string, metadataJSON string, model string, baseURL string, version int) error

	// ── Versioning ─────────────────────────────────────────────────────

	// CreateNewVersion creates a new versioned copy of a script.
	CreateNewVersion(ctx context.Context, parentID int64, s *Script, sections []Section, stockMatches []StockMatch) (int64, error)

	// NextVersionForTopic returns the next version number for a topic+language+mode tuple.
	NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error)

	// ── Research ───────────────────────────────────────────────────────

	// SaveResearchSources persists research sources for a script.
	SaveResearchSources(ctx context.Context, scriptID int64, sources []ResearchSource) error

	// GetResearchSources returns research sources for a script.
	GetResearchSources(ctx context.Context, scriptID int64) ([]ResearchSource, error)

	// SaveResearchCache caches a research result by key.
	SaveResearchCache(ctx context.Context, key, topic, language string, maxSteps int, sourceText string) error

	// GetResearchCache returns a cached research result by key.
	GetResearchCache(ctx context.Context, key string) (string, error)

	// TouchResearchCache updates the last-access timestamp for a cache key.
	TouchResearchCache(ctx context.Context, key string) (int64, error)

	// SweepStaleResearchCache removes cache entries older than maxAgeDays.
	SweepStaleResearchCache(ctx context.Context, maxAgeDays int) (int64, error)

	// ── Generation Logs ────────────────────────────────────────────────

	// SaveGenerationLog records a generation phase event.
	SaveGenerationLog(ctx context.Context, logEntry GenerationLog) error

	// GetGenerationLogs returns all generation logs for a script.
	GetGenerationLogs(ctx context.Context, scriptID int64) ([]GenerationLog, error)

	// ── Sections ───────────────────────────────────────────────────────

	// GetSectionByID returns a single section.
	GetSectionByID(ctx context.Context, id int64) (*Section, error)

	// UpdateSectionContent updates the content of a section.
	UpdateSectionContent(ctx context.Context, id int64, content string) error

	// GetAdjacentSections returns the previous and next sections relative
	// to the given sort_order, for editor navigation.
	GetAdjacentSections(ctx context.Context, scriptID int64, currentSortOrder int) (prev *Section, next *Section, err error)

	// SaveOutlineSections persists outline sections for a script.
	SaveOutlineSections(ctx context.Context, scriptID int64, sections []OutlineSection) error

	// GetOutlineSections returns outline sections for a script.
	GetOutlineSections(ctx context.Context, scriptID int64) ([]OutlineSection, error)

	// ── Versions ───────────────────────────────────────────────────────

	// SaveScriptVersion records an explicit version of a script output.
	SaveScriptVersion(ctx context.Context, v *VersionRecord) (int64, error)

	// GetScriptVersions returns all version records for a script.
	GetScriptVersions(ctx context.Context, scriptID int64) ([]VersionRecord, error)

	// ── Maintenance ────────────────────────────────────────────────────

	// HardDeleteOldDeletedScripts permanently removes scripts soft-deleted
	// more than daysOld ago.
	HardDeleteOldDeletedScripts(ctx context.Context, daysOld int) (int64, error)

	// VacuumDatabase reclaims unused space from the database.
	VacuumDatabase(ctx context.Context) error

	// AnalyzeDatabase updates SQLite query planner statistics.
	// Called by background maintenance jobs to keep query plans efficient.
	AnalyzeDatabase(ctx context.Context) error
}
