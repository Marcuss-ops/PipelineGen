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
//
// Issue 15a (June 2026): ScriptSectionRecord in the SQLite package is now
// a private row type (scriptSectionRow). The adapter is the single
// conversion point: write direction uses the exported SectionRows builder,
// read direction uses the exported EachSectionRow callback. No inline
// field copies outside the adapter.
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
		IdempotencyKey: rec.IdempotencyKey,
		SpecScene:      rec.SpecScene,
	}
}

// ── Section record conversion (Issue 15a — single conversion point) ──────

// buildSectionRows converts application ScriptSectionRecord values into
// the SQLite package's private row type via the exported SectionRows builder.
// This is the canonical write-direction conversion point.
func buildSectionRows(in []ScriptSectionRecord) *sqlitescripts.SectionRows {
	b := sqlitescripts.NewSectionRows(len(in))
	for _, s := range in {
		b.Add(s.ID, s.ScriptID, s.SectionType, s.SectionTitle, s.Content, s.SortOrder, s.WordCount, s.Status)
	}
	return b
}

// ── Stock match conversion (Issue 15b — single conversion point) ────────

// buildStockMatchRows converts application ScriptStockMatchRecord values
// into the SQLite package's private row type via the exported StockMatchRows builder.
func buildStockMatchRows(in []ScriptStockMatchRecord) *sqlitescripts.StockMatchRows {
	b := sqlitescripts.NewStockMatchRows(len(in))
	for _, m := range in {
		b.Add(m.ID, m.ScriptID, m.SegmentIndex, m.StockPath, m.StockSource, m.Score, m.MatchedTerms)
	}
	return b
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

// ── Outline section conversion (Issue 15b — single conversion point) ─────

// buildOutlineSectionRows converts application ScriptOutlineSectionRecord
// values into the SQLite package's private row type via the exported
// OutlineSectionRows builder.
func buildOutlineSectionRows(in []ScriptOutlineSectionRecord) *sqlitescripts.OutlineSectionRows {
	b := sqlitescripts.NewOutlineSectionRows(len(in))
	for _, s := range in {
		b.Add(s.ScriptID, s.SectionIndex, s.Title, s.Purpose, s.TargetWords, s.KeyPointsJSON, s.EmotionalRole)
	}
	return b
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
	return a.inner.SaveScript(ctx, toSQLiteScriptRecord(rec), buildSectionRows(sections).Slice(), buildStockMatchRows(matches).Slice())
}

func (a *sqliteRepoAdapter) UpdateScriptFinalContent(ctx context.Context, scriptID int64, outputText string, wordCount int, status, metadata, model, ollamaBaseURL string, version int) error {
	return a.inner.UpdateScriptFinalContent(ctx, scriptID, outputText, wordCount, status, metadata, model, ollamaBaseURL, version)
}

func (a *sqliteRepoAdapter) SaveGenerationLog(ctx context.Context, log ScriptGenerationLog) error {
	return a.inner.SaveGenerationLog(ctx, toSQLiteGenerationLog(log))
}

func (a *sqliteRepoAdapter) SaveOutlineSections(ctx context.Context, scriptID int64, sections []ScriptOutlineSectionRecord) error {
	return a.inner.SaveOutlineSections(ctx, scriptID, buildOutlineSectionRows(sections).Slice())
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
	// sec is *scriptSectionRow (private type, inferred via :=).
	// The adapter accesses exported fields without naming the type.
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
	// sections and matches are []scriptSectionRow / []scriptStockMatchRow
	// (private types, inferred via :=). Convert via canonical callbacks.
	outSections := make([]ScriptSectionRecord, 0, len(sections))
	sqlitescripts.EachSectionRow(sections, func(id, scriptID int64, sectionType, sectionTitle, content string, sortOrder, wordCount int, status string) {
		outSections = append(outSections, ScriptSectionRecord{
			ID: id, ScriptID: scriptID, Index: sortOrder,
			SectionType: sectionType, SectionTitle: sectionTitle,
			Content: content, SortOrder: sortOrder,
			WordCount: wordCount, Status: status,
		})
	})
	outMatches := make([]ScriptStockMatchRecord, 0, len(matches))
	sqlitescripts.EachStockMatchRow(matches, func(id, scriptID int64, segmentIndex int, stockPath, stockSource string, score float64, matchedTerms string) {
		outMatches = append(outMatches, ScriptStockMatchRecord{
			ID: id, ScriptID: scriptID, SegmentIndex: segmentIndex,
			StockPath: stockPath, StockSource: stockSource,
			Score: score, MatchedTerms: matchedTerms,
		})
	})
	return fromSQLiteScriptRecord(rec), outSections, outMatches, nil
}

func (a *sqliteRepoAdapter) GetAdjacentSections(ctx context.Context, scriptID int64, sortOrder int) (prev, next *ScriptSectionRecord, err error) {
	sp, sn, err := a.inner.GetAdjacentSections(ctx, scriptID, sortOrder)
	if err != nil {
		return nil, nil, err
	}
	// sp/sn are *scriptSectionRow (private type, inferred via :=).
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
