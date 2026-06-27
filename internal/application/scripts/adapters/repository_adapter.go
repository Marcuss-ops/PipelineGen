package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// sqliteRepoAdapter bridges the concrete *sqlitescripts.ScriptRepository
// to the scripts.ScriptRepository interface by converting between the
// two type systems (same-named structs in different packages).
type sqliteRepoAdapter struct {
	inner *sqlitescripts.ScriptRepository
}

// NewRepositoryAdapter wraps a concrete sqlitescripts.ScriptRepository
// and returns a scripts.ScriptRepository that can be injected into all
// consumers (engine, script flow handler, batch service, history handler).
func NewRepositoryAdapter(inner *sqlitescripts.ScriptRepository) ScriptRepository {
	if inner == nil {
		return nil
	}
	return &sqliteRepoAdapter{inner: inner}
}

// ── ScriptRecord conversion ──────────────────────────────────────────────

func toSQLiteScriptRecord(rec *ScriptRecord) *sqlitescripts.ScriptRecord {
	if rec == nil {
		return nil
	}
	var parentID *int64
	if rec.ParentScriptID != 0 {
		id := rec.ParentScriptID
		parentID = &id
	}
	createdAt := ""
	if !rec.CreatedAt.IsZero() {
		createdAt = rec.CreatedAt.Format(time.RFC3339)
	}
	updatedAt := ""
	if !rec.UpdatedAt.IsZero() {
		updatedAt = rec.UpdatedAt.Format(time.RFC3339)
	}
	return &sqlitescripts.ScriptRecord{
		ID:             rec.ID,
		Topic:          rec.Topic,
		Title:          rec.Title,
		Duration:       rec.Duration,
		Language:       rec.Language,
		Template:       rec.Template,
		Mode:           rec.Mode,
		Tone:           rec.Tone,
		TargetWords:    rec.TargetWords,
		FinalWordCount: rec.FinalWordCount,
		Status:         rec.Status,
		NarrativeText:  rec.NarrativeText,
		TimelineJSON:   rec.TimelineJSON,
		EntitiesJSON:   rec.EntitiesJSON,
		MetadataJSON:   rec.MetadataJSON,
		FullDocument:   rec.FullDocument,
		ModelUsed:      rec.ModelUsed,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		Version:        rec.Version,
		ParentScriptID: parentID,
		IsDeleted:      false,
		// PR 6 (June 2026): dedicated idempotency_key + specscene
		// columns replace the pre-PR-6 dual-purpose
		// Template / TimelineJSON slots. PersistenceProcessor is the
		// only writer; the adapter passes the fields straight
		// through to the SQL side.
		IdempotencyKey: rec.IdempotencyKey,
		SpecScene:      rec.SpecScene,
	}
}

func fromSQLiteScriptRecord(rec *sqlitescripts.ScriptRecord) *ScriptRecord {
	if rec == nil {
		return nil
	}
	var parentID int64
	if rec.ParentScriptID != nil {
		parentID = *rec.ParentScriptID
	}
	createdAt, _ := time.Parse(time.RFC3339, rec.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, rec.UpdatedAt)
	return &ScriptRecord{
		ID:             rec.ID,
		Topic:          rec.Topic,
		Title:          rec.Title,
		Duration:       rec.Duration,
		Language:       rec.Language,
		Template:       rec.Template,
		Mode:           rec.Mode,
		Tone:           rec.Tone,
		TargetWords:    rec.TargetWords,
		FinalWordCount: rec.FinalWordCount,
		Status:         rec.Status,
		NarrativeText:  rec.NarrativeText,
		OutputText:     rec.NarrativeText,
		FullDocument:   rec.FullDocument,
		MetadataJSON:   rec.MetadataJSON,
		TimelineJSON:   rec.TimelineJSON,
		EntitiesJSON:   rec.EntitiesJSON,
		ModelUsed:      rec.ModelUsed,
		Model:          rec.ModelUsed,
		WordCount:      rec.FinalWordCount,
		ParentScriptID: parentID,
		Version:        rec.Version,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		// PR 6 (June 2026): dedicated idempotency_key + specscene
		// columns. The fields flow from the SQL side without any
		// slot-shape heuristic. The PR 5 era's "idempotency key is
		// stuffed into Template" is gone.
		IdempotencyKey: rec.IdempotencyKey,
		SpecScene:      rec.SpecScene,
	}
}

// ── Section record conversion ────────────────────────────────────────────

func toSQLiteSectionRecords(in []ScriptSectionRecord) []sqlitescripts.ScriptSectionRecord {
	out := make([]sqlitescripts.ScriptSectionRecord, len(in))
	for i, s := range in {
		out[i] = sqlitescripts.ScriptSectionRecord{
			ID:           s.ID,
			ScriptID:     s.ScriptID,
			SectionType:  s.SectionType,
			SectionTitle: s.SectionTitle,
			Content:      s.Content,
			SortOrder:    s.SortOrder,
			WordCount:    s.WordCount,
			Status:       s.Status,
		}
	}
	return out
}

func fromSQLiteSectionRecords(in []sqlitescripts.ScriptSectionRecord) []ScriptSectionRecord {
	out := make([]ScriptSectionRecord, len(in))
	for i, s := range in {
		out[i] = ScriptSectionRecord{
			ID:           s.ID,
			ScriptID:     s.ScriptID,
			Index:        s.SortOrder,
			SectionType:  s.SectionType,
			SectionTitle: s.SectionTitle,
			Content:      s.Content,
			SortOrder:    s.SortOrder,
			WordCount:    s.WordCount,
			Status:       s.Status,
		}
	}
	return out
}

// ── Stock match conversion ───────────────────────────────────────────────

func toSQLiteStockMatchRecords(in []ScriptStockMatchRecord) []sqlitescripts.ScriptStockMatchRecord {
	out := make([]sqlitescripts.ScriptStockMatchRecord, len(in))
	for i, m := range in {
		out[i] = sqlitescripts.ScriptStockMatchRecord{
			ID:           m.ID,
			ScriptID:     m.ScriptID,
			SegmentIndex: m.SegmentIndex,
			StockPath:    m.StockPath,
			StockSource:  m.StockSource,
			Score:        m.Score,
			MatchedTerms: m.MatchedTerms,
		}
	}
	return out
}

func fromSQLiteStockMatchRecords(in []sqlitescripts.ScriptStockMatchRecord) []ScriptStockMatchRecord {
	out := make([]ScriptStockMatchRecord, len(in))
	for i, m := range in {
		out[i] = ScriptStockMatchRecord{
			ID:           m.ID,
			ScriptID:     m.ScriptID,
			SegmentIndex: m.SegmentIndex,
			StockPath:    m.StockPath,
			StockSource:  m.StockSource,
			Score:        m.Score,
			MatchedTerms: m.MatchedTerms,
		}
	}
	return out
}

// ── Research source conversion ───────────────────────────────────────────

func toSQLiteResearchSources(in []ScriptResearchSource) []sqlitescripts.ScriptResearchSource {
	out := make([]sqlitescripts.ScriptResearchSource, len(in))
	for i, s := range in {
		out[i] = sqlitescripts.ScriptResearchSource{
			ScriptID:       s.ScriptID,
			Query:          s.Query,
			URL:            s.URL,
			Title:          s.Title,
			Snippet:        s.Snippet,
			SourceType:     s.SourceType,
			UsedInSections: s.UsedInSections,
			RelevanceScore: s.RelevanceScore,
		}
	}
	return out
}

// ── Outline section conversion ───────────────────────────────────────────

func toSQLiteOutlineSectionRecords(in []ScriptOutlineSectionRecord) []sqlitescripts.ScriptOutlineSectionRecord {
	out := make([]sqlitescripts.ScriptOutlineSectionRecord, len(in))
	for i, s := range in {
		out[i] = sqlitescripts.ScriptOutlineSectionRecord{
			ScriptID:      s.ScriptID,
			SectionIndex:  s.SectionIndex,
			Title:         s.Title,
			Purpose:       s.Purpose,
			TargetWords:   s.TargetWords,
			KeyPointsJSON: s.KeyPointsJSON,
			EmotionalRole: s.EmotionalRole,
		}
	}
	return out
}

// ── Generation log conversion ────────────────────────────────────────────

func toSQLiteGenerationLog(in ScriptGenerationLog) sqlitescripts.ScriptGenerationLog {
	return sqlitescripts.ScriptGenerationLog{
		ScriptID:    in.ScriptID,
		Phase:       in.Phase,
		PromptHash:  in.PromptHash,
		Model:       in.Model,
		InputWords:  in.InputWords,
		OutputWords: in.OutputWords,
		DurationMs:  in.DurationMs,
		RetryCount:  in.RetryCount,
		CacheStatus: in.CacheStatus,
		Error:       in.Error,
	}
}

// ── Interface methods ────────────────────────────────────────────────────

func (a *sqliteRepoAdapter) SaveScript(ctx context.Context, rec *ScriptRecord, sections []ScriptSectionRecord, matches []ScriptStockMatchRecord) (int64, error) {
	return a.inner.SaveScript(ctx, toSQLiteScriptRecord(rec), toSQLiteSectionRecords(sections), toSQLiteStockMatchRecords(matches))
}

func (a *sqliteRepoAdapter) UpdateScriptFinalContent(ctx context.Context, scriptID int64, outputText string, wordCount int, status, metadata, model, ollamaBaseURL string, version int) error {
	return a.inner.UpdateScriptFinalContent(ctx, scriptID, outputText, wordCount, status, metadata, model, ollamaBaseURL, version)
}

func (a *sqliteRepoAdapter) SaveGenerationLog(ctx context.Context, log ScriptGenerationLog) error {
	return a.inner.SaveGenerationLog(ctx, toSQLiteGenerationLog(log))
}

func (a *sqliteRepoAdapter) SaveOutlineSections(ctx context.Context, scriptID int64, sections []ScriptOutlineSectionRecord) error {
	return a.inner.SaveOutlineSections(ctx, scriptID, toSQLiteOutlineSectionRecords(sections))
}

func (a *sqliteRepoAdapter) SaveResearchSources(ctx context.Context, scriptID int64, sources []ScriptResearchSource) error {
	return a.inner.SaveResearchSources(ctx, scriptID, toSQLiteResearchSources(sources))
}

func (a *sqliteRepoAdapter) NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error) {
	return a.inner.NextVersionForTopic(ctx, topic, language, mode)
}

func (a *sqliteRepoAdapter) GetSectionByID(ctx context.Context, sectionID int64) (*ScriptSectionRecord, error) {
	sec, err := a.inner.GetSectionByID(ctx, sectionID)
	if err != nil || sec == nil {
		return nil, err
	}
	return &ScriptSectionRecord{
		ID:           sec.ID,
		ScriptID:     sec.ScriptID,
		SectionType:  sec.SectionType,
		SectionTitle: sec.SectionTitle,
		Content:      sec.Content,
		SortOrder:    sec.SortOrder,
		WordCount:    sec.WordCount,
		Status:       sec.Status,
	}, nil
}

func (a *sqliteRepoAdapter) GetScriptByID(id int64) (*ScriptRecord, []ScriptSectionRecord, []ScriptStockMatchRecord, error) {
	rec, sections, matches, err := a.inner.GetScriptByID(id)
	if err != nil {
		return nil, nil, nil, err
	}
	return fromSQLiteScriptRecord(rec), fromSQLiteSectionRecords(sections), fromSQLiteStockMatchRecords(matches), nil
}

func (a *sqliteRepoAdapter) GetAdjacentSections(ctx context.Context, scriptID int64, sortOrder int) (prev, next *ScriptSectionRecord, err error) {
	sp, sn, err := a.inner.GetAdjacentSections(ctx, scriptID, sortOrder)
	if err != nil {
		return nil, nil, err
	}
	if sp != nil {
		r := ScriptSectionRecord{
			ID:           sp.ID,
			ScriptID:     sp.ScriptID,
			SectionType:  sp.SectionType,
			SectionTitle: sp.SectionTitle,
			Content:      sp.Content,
			SortOrder:    sp.SortOrder,
			WordCount:    sp.WordCount,
			Status:       sp.Status,
		}
		prev = &r
	}
	if sn != nil {
		r := ScriptSectionRecord{
			ID:           sn.ID,
			ScriptID:     sn.ScriptID,
			SectionType:  sn.SectionType,
			SectionTitle: sn.SectionTitle,
			Content:      sn.Content,
			SortOrder:    sn.SortOrder,
			WordCount:    sn.WordCount,
			Status:       sn.Status,
		}
		next = &r
	}
	return prev, next, nil
}

func (a *sqliteRepoAdapter) UpdateSectionContent(ctx context.Context, sectionID int64, content string) error {
	return a.inner.UpdateSectionContent(ctx, sectionID, content)
}

func (a *sqliteRepoAdapter) ListScripts(ctx context.Context, filter ScriptListFilter) ([]*ScriptRecord, error) {
	recs, _, err := a.inner.ListScripts(filter.Limit, filter.Offset, filter.Language, filter.Topic)
	if err != nil {
		return nil, err
	}
	out := make([]*ScriptRecord, len(recs))
	for i := range recs {
		out[i] = fromSQLiteScriptRecord(&recs[i])
	}
	return out, nil
}

// FindScriptByIdempotencyKey delegates to the concrete sqlite repo.
// PR 5: the concrete repo looks up by `template = ? AND language = ?`
// ORDER BY id DESC LIMIT 1 (the idem key is currently carried on the
// existing Template slot — PR 6 introduces a dedicated
// `idempotency_key` column).
func (a *sqliteRepoAdapter) FindScriptByIdempotencyKey(ctx context.Context, itemID, cacheKey, promptVersion string, targetWords int, language string) (*ScriptRecord, bool, error) {
	if a == nil || a.inner == nil {
		return nil, false, nil
	}
	hash := computeAdapterIdempotencyKey(itemID, cacheKey, promptVersion, targetWords, language)
	rec, err := a.inner.FindByIdempotencyKey(ctx, hash, language)
	if err != nil {
		return nil, false, err
	}
	if rec == nil {
		return nil, false, nil
	}
	return fromSQLiteScriptRecord(rec), true, nil
}

// computeAdapterIdempotencyKey mirrors PersistenceProcessor.computeIdempotencyKey
// using the same 5-tuple. Kept as a private helper here (rather than
// calling back into PersistenceProcessor) so the adapter layer does
// NOT depend on the runtime processor type.
func computeAdapterIdempotencyKey(itemID, cacheKey, promptVersion string, targetWords int, language string) string {
	tuple := fmt.Sprintf("%s|%s|%s|%d|%s", itemID, cacheKey, promptVersion, targetWords, language)
	sum := sha256.Sum256([]byte(tuple))
	return hex.EncodeToString(sum[:])[:16]
}
