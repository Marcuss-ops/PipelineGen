package scripts

// ScriptRecord represents a script record in the database.
//
// PR 6 (June 2026): dedicated IdempotencyKey + SpecScene columns
// replace the pre-PR-6 dual-purpose Template / TimelineJSON slots.
// Both new fields are nullable TEXT in SQLite (defaulted to ” so the
// mat NOT NULL constraint is honored). PersistenceProcessor is the
// only writer of these fields; the engine no longer touches them.
// Semantic-history preservation: Template is still populated for
// ListScripts filters and remains filterable as `WHERE template = ?`
// (the column was repurposed for the idem key under PR 5; existing
// rows that used it as semantic-template-value are still queryable).
type ScriptRecord struct {
	ID             int64
	Topic          string
	Title          string
	Duration       int
	Language       string
	Template       string
	Mode           string
	Tone           string
	TargetWords    int
	FinalWordCount int
	Status         string
	NarrativeText  string
	TimelineJSON   string
	EntitiesJSON   string
	MetadataJSON   string
	FullDocument   string
	ModelUsed      string
	OllamaBaseURL  string
	CreatedAt      string
	UpdatedAt      string
	Version        int
	ParentScriptID *int64
	IsDeleted      bool

	// IdempotencyKey is the 16-hex-char SHA-256 prefix of
	// (item_id|cache_key|prompt_version|target_words|language) for the
	// canonical reconciliation tuple. PR 6 stored in a dedicated
	// `idempotency_key TEXT NOT NULL DEFAULT ''` column (see
	// migrations/sqlite/100_add_idempotency_key_and_specscene_columns.sql).
	IdempotencyKey string

	// SpecScene is the JSON-serialised SpecSceneOutput emitted by the
	// engine (canonical MSOV1 contract). PR 6 stored in a dedicated
	// `specscene TEXT NOT NULL DEFAULT ''` column; the pre-PR-6 slot
	// was `timeline_json` which has overlapping but distinct semantics.
	SpecScene string
}

// scriptSectionRow is a private row type for the script_sections table.
// Issue 15a (June 2026): made private — the adapter layer is the single
// conversion point. Use SectionRows builder + EachSectionRow callback
// to construct/iterate values from outside the package.
type scriptSectionRow struct {
	ID           int64
	ScriptID     int64
	SectionType  string
	SectionTitle string
	Content      string
	SortOrder    int
	WordCount    int
	Status       string

	// VoiceoverLink is the Drive URL of the per-scene voiceover audio.
	VoiceoverLink string
}

// SectionRows is the canonical builder for constructing []scriptSectionRow
// values from the adapter layer. Exported so external packages can
// construct row slices without naming the private row type.
type SectionRows struct {
	rows []scriptSectionRow
}

// NewSectionRows returns a SectionRows builder pre-allocated to cap.
func NewSectionRows(cap int) *SectionRows {
	return &SectionRows{rows: make([]scriptSectionRow, 0, cap)}
}

// Add appends a row with the given field values.
func (sr *SectionRows) Add(id, scriptID int64, sectionType, sectionTitle, content string, sortOrder, wordCount int, status string, voiceoverLink string) {
	sr.rows = append(sr.rows, scriptSectionRow{
		ID: id, ScriptID: scriptID, SectionType: sectionType,
		SectionTitle: sectionTitle, Content: content,
		SortOrder: sortOrder, WordCount: wordCount, Status: status,
		VoiceoverLink: voiceoverLink,
	})
}

// Slice returns the accumulated rows. Ownership transfers to the caller.
func (sr *SectionRows) Slice() []scriptSectionRow {
	return sr.rows
}

// EachSectionRow calls fn for every row in the slice, passing individual
// field values. Exported so the adapter can iterate without naming the
// private row type.
func EachSectionRow(rows []scriptSectionRow, fn func(id, scriptID int64, sectionType, sectionTitle, content string, sortOrder, wordCount int, status string, voiceoverLink string)) {
	for _, r := range rows {
		fn(r.ID, r.ScriptID, r.SectionType, r.SectionTitle, r.Content, r.SortOrder, r.WordCount, r.Status, r.VoiceoverLink)
	}
}

// scriptStockMatchRow is a private row type for the script_stock_matches table.
// Issue 15b (June 2026): made private — the adapter layer is the single
// conversion point. Use StockMatchRows builder + EachStockMatchRow callback
// to construct/iterate values from outside the package.
type scriptStockMatchRow struct {
	ID           int64
	ScriptID     int64
	SegmentIndex int
	StockPath    string
	StockSource  string
	Score        float64
	MatchedTerms string
}

// StockMatchRows is the canonical builder for constructing []scriptStockMatchRow
// values from the adapter layer.
type StockMatchRows struct {
	rows []scriptStockMatchRow
}

// NewStockMatchRows returns a StockMatchRows builder pre-allocated to cap.
func NewStockMatchRows(cap int) *StockMatchRows {
	return &StockMatchRows{rows: make([]scriptStockMatchRow, 0, cap)}
}

// Add appends a row with the given field values.
func (sm *StockMatchRows) Add(id, scriptID int64, segmentIndex int, stockPath, stockSource string, score float64, matchedTerms string) {
	sm.rows = append(sm.rows, scriptStockMatchRow{
		ID: id, ScriptID: scriptID, SegmentIndex: segmentIndex,
		StockPath: stockPath, StockSource: stockSource,
		Score: score, MatchedTerms: matchedTerms,
	})
}

// Slice returns the accumulated rows.
func (sm *StockMatchRows) Slice() []scriptStockMatchRow {
	return sm.rows
}

// EachStockMatchRow calls fn for every row in the slice, passing individual
// field values. Exported so the adapter can iterate without naming the
// private row type.
func EachStockMatchRow(rows []scriptStockMatchRow, fn func(id, scriptID int64, segmentIndex int, stockPath, stockSource string, score float64, matchedTerms string)) {
	for _, r := range rows {
		fn(r.ID, r.ScriptID, r.SegmentIndex, r.StockPath, r.StockSource, r.Score, r.MatchedTerms)
	}
}

// ScriptResearchSource represents a web/youtube/transcript source used during script generation.
type ScriptResearchSource struct {
	ID             int64
	ScriptID       int64
	Query          string
	URL            string
	Title          string
	Snippet        string
	SourceType     string // "web", "youtube", "transcript", "inline"
	UsedInSections string // JSON array of section indices
	RelevanceScore float64
	CreatedAt      string
}

// ScriptGenerationLog records a single phase of script generation for debugging.
type ScriptGenerationLog struct {
	ID          int64
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
	CreatedAt   string
}

// scriptOutlineSectionRow is a private row type for the script_outline_sections table.
// Issue 15b (June 2026): made private — the adapter layer is the single
// conversion point. Use OutlineSectionRows builder + EachOutlineSectionRow
// callback to construct/iterate values from outside the package.
type scriptOutlineSectionRow struct {
	ID            int64
	ScriptID      int64
	SectionIndex  int
	Title         string
	Purpose       string
	TargetWords   int
	KeyPointsJSON string
	EmotionalRole string
	CreatedAt     string
}

// OutlineSectionRows is the canonical builder for constructing []scriptOutlineSectionRow
// values from the adapter layer.
type OutlineSectionRows struct {
	rows []scriptOutlineSectionRow
}

// NewOutlineSectionRows returns an OutlineSectionRows builder pre-allocated to cap.
func NewOutlineSectionRows(cap int) *OutlineSectionRows {
	return &OutlineSectionRows{rows: make([]scriptOutlineSectionRow, 0, cap)}
}

// Add appends a row with the given field values.
func (os *OutlineSectionRows) Add(scriptID int64, sectionIndex int, title, purpose string, targetWords int, keyPointsJSON, emotionalRole string) {
	os.rows = append(os.rows, scriptOutlineSectionRow{
		ScriptID: scriptID, SectionIndex: sectionIndex,
		Title: title, Purpose: purpose, TargetWords: targetWords,
		KeyPointsJSON: keyPointsJSON, EmotionalRole: emotionalRole,
	})
}

// Slice returns the accumulated rows.
func (os *OutlineSectionRows) Slice() []scriptOutlineSectionRow {
	return os.rows
}

// EachOutlineSectionRow calls fn for every row in the slice, passing individual
// field values. Exported so the adapter can iterate without naming the
// private row type.
func EachOutlineSectionRow(rows []scriptOutlineSectionRow, fn func(id, scriptID int64, sectionIndex int, title, purpose string, targetWords int, keyPointsJSON, emotionalRole, createdAt string)) {
	for _, r := range rows {
		fn(r.ID, r.ScriptID, r.SectionIndex, r.Title, r.Purpose, r.TargetWords, r.KeyPointsJSON, r.EmotionalRole, r.CreatedAt)
	}
}

// ScriptVersionRecord represents an explicit version of a script output.
type ScriptVersionRecord struct {
	ID           int64
	ScriptID     int64
	Version      int
	FinalText    string
	MetadataJSON string
	CreatedAt    string
}
