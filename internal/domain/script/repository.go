package script

import "context"

// Repository is the canonical domain contract for script persistence.
type Repository interface {
	SaveScript(ctx context.Context, s *Script, sections []Section, stockMatches []StockMatch) (int64, error)
	GetScriptByID(ctx context.Context, id int64) (*Script, []Section, []StockMatch, error)
	ListScripts(ctx context.Context, limit, offset int, language, template string) ([]Script, int, error)
	FindByTopic(ctx context.Context, topic, language string) (*Script, []Section, []StockMatch, error)
	SoftDeleteScript(ctx context.Context, id int64) error
	UpdateScriptFinalContent(ctx context.Context, id int64, text string, wordCount int, status string, metadataJSON string, model string, baseURL string, version int) error

	CreateNewVersion(ctx context.Context, parentID int64, s *Script, sections []Section, stockMatches []StockMatch) (int64, error)
	NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error)

	SaveResearchSources(ctx context.Context, scriptID int64, sources []ResearchSource) error
	GetResearchSources(ctx context.Context, scriptID int64) ([]ResearchSource, error)
	SaveResearchCache(ctx context.Context, key, topic, language string, maxSteps int, sourceText string) error
	GetResearchCache(ctx context.Context, key string) (string, error)
	TouchResearchCache(ctx context.Context, key string) (int64, error)
	SweepStaleResearchCache(ctx context.Context, maxAgeDays int) (int64, error)

	SaveGenerationLog(ctx context.Context, logEntry GenerationLog) error
	GetGenerationLogs(ctx context.Context, scriptID int64) ([]GenerationLog, error)

	GetSectionByID(ctx context.Context, id int64) (*Section, error)
	UpdateSectionContent(ctx context.Context, id int64, content string) error
	GetAdjacentSections(ctx context.Context, scriptID int64, currentSortOrder int) (prev *Section, next *Section, err error)
	SaveOutlineSections(ctx context.Context, scriptID int64, sections []OutlineSection) error
	GetOutlineSections(ctx context.Context, scriptID int64) ([]OutlineSection, error)

	SaveScriptVersion(ctx context.Context, v *VersionRecord) (int64, error)
	GetScriptVersions(ctx context.Context, scriptID int64) ([]VersionRecord, error)

	HardDeleteOldDeletedScripts(ctx context.Context, daysOld int) (int64, error)
	VacuumDatabase(ctx context.Context) error
	AnalyzeDatabase(ctx context.Context) error
}
