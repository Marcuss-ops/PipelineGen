// Package adapters — repository.go holds the canonical ScriptRepository
// port re-exports (from ports/) plus the canonical adapter implementation
// (sqliteRepoAdapter) bridging ports.ScriptRepository to the concrete
// *sqlitescripts.ScriptRepository.
//
// P1.6 (June 2026): the canonical interface + record types moved to
// ports/repository.go. This file re-exports them as type aliases for
// back-compat and implements the adapter (the sqliteRepoAdapter struct
// could not live in internal/infrastructure/database/sqlite/assets/
// because of a pre-existing import cycle through resolver.go →
// adapters → ports → voiceover → ... → sqlite/assets).
package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// ── Canonical port re-exports (P1.6) ─────────────────────────────────

type ScriptRepository = ports.ScriptRepository
type ScriptListFilter = ports.ScriptListFilter
type ScriptSectionRecord = ports.ScriptSectionRecord
type ScriptStockMatchRecord = ports.ScriptStockMatchRecord
type ScriptResearchSource = ports.ScriptResearchSource
type ScriptOutlineSectionRecord = ports.ScriptOutlineSectionRecord
type ScriptGenerationLog = ports.ScriptGenerationLog
type ScriptRecord = ports.ScriptRecord

// ── Adapter: sqliteRepoAdapter ───────────────────────────────────────

// sqliteRepoAdapter bridges the concrete *sqlitescripts.ScriptRepository
// to the ports.ScriptRepository interface. P1.6 (June 2026): moved
// from the deleted repository_adapter.go into this file; imports
// ports types + sqlitescripts concrete.
type sqliteRepoAdapter struct {
	inner *sqlitescripts.ScriptRepository
}

// Compile-time assertion: sqliteRepoAdapter satisfies ports.ScriptRepository.
var _ ports.ScriptRepository = (*sqliteRepoAdapter)(nil)

// NewRepositoryAdapter wraps a concrete sqlitescripts.ScriptRepository
// and returns a ports.ScriptRepository.
func NewRepositoryAdapter(inner *sqlitescripts.ScriptRepository) ScriptRepository {
	if inner == nil {
		return nil
	}
	return &sqliteRepoAdapter{inner: inner}
}

// ── Conversion helpers ─────────────────────────────────────────────

func toSQLiteScriptRecord(rec *ports.ScriptRecord) *sqlitescripts.ScriptRecord {
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
		ID: rec.ID, Topic: rec.Topic, Title: rec.Title, Duration: rec.Duration,
		Language: rec.Language, Template: rec.Template, Mode: rec.Mode, Tone: rec.Tone,
		TargetWords: rec.TargetWords, FinalWordCount: rec.FinalWordCount,
		Status: rec.Status, NarrativeText: rec.NarrativeText,
		TimelineJSON: rec.TimelineJSON, EntitiesJSON: rec.EntitiesJSON,
		MetadataJSON: rec.MetadataJSON, FullDocument: rec.FullDocument,
		ModelUsed: rec.ModelUsed, CreatedAt: createdAt, UpdatedAt: updatedAt,
		Version: rec.Version, ParentScriptID: parentID, IsDeleted: false,
		IdempotencyKey: rec.IdempotencyKey, SpecScene: rec.SpecScene,
	}
}

func fromSQLiteScriptRecord(rec *sqlitescripts.ScriptRecord) *ports.ScriptRecord {
	if rec == nil {
		return nil
	}
	var parentID int64
	if rec.ParentScriptID != nil {
		parentID = *rec.ParentScriptID
	}
	createdAt, _ := time.Parse(time.RFC3339, rec.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, rec.UpdatedAt)
	return &ports.ScriptRecord{
		ID: rec.ID, Topic: rec.Topic, Title: rec.Title, Duration: rec.Duration,
		Language: rec.Language, Template: rec.Template, Mode: rec.Mode, Tone: rec.Tone,
		TargetWords: rec.TargetWords, FinalWordCount: rec.FinalWordCount,
		Status: rec.Status, NarrativeText: rec.NarrativeText,
		OutputText: rec.NarrativeText, FullDocument: rec.FullDocument,
		MetadataJSON: rec.MetadataJSON, TimelineJSON: rec.TimelineJSON,
		EntitiesJSON: rec.EntitiesJSON, ModelUsed: rec.ModelUsed, Model: rec.ModelUsed,
		WordCount: rec.FinalWordCount, ParentScriptID: parentID, Version: rec.Version,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
		IdempotencyKey: rec.IdempotencyKey, SpecScene: rec.SpecScene,
	}
}

func buildSectionRows(in []ports.ScriptSectionRecord) *sqlitescripts.SectionRows {
	b := sqlitescripts.NewSectionRows(len(in))
	for _, s := range in {
		b.Add(s.ID, s.ScriptID, s.SectionType, s.SectionTitle, s.Content, s.SortOrder, s.WordCount, s.Status)
	}
	return b
}

func buildStockMatchRows(in []ports.ScriptStockMatchRecord) *sqlitescripts.StockMatchRows {
	b := sqlitescripts.NewStockMatchRows(len(in))
	for _, m := range in {
		b.Add(m.ID, m.ScriptID, m.SegmentIndex, m.StockPath, m.StockSource, m.Score, m.MatchedTerms)
	}
	return b
}

func toSQLiteResearchSources(in []ports.ScriptResearchSource) []sqlitescripts.ScriptResearchSource {
	out := make([]sqlitescripts.ScriptResearchSource, len(in))
	for i, s := range in {
		out[i] = sqlitescripts.ScriptResearchSource{
			ScriptID: s.ScriptID, Query: s.Query, URL: s.URL,
			Title: s.Title, Snippet: s.Snippet,
			SourceType: s.SourceType, UsedInSections: s.UsedInSections,
			RelevanceScore: s.RelevanceScore,
		}
	}
	return out
}

func buildOutlineSectionRows(in []ports.ScriptOutlineSectionRecord) *sqlitescripts.OutlineSectionRows {
	b := sqlitescripts.NewOutlineSectionRows(len(in))
	for _, s := range in {
		b.Add(s.ScriptID, s.SectionIndex, s.Title, s.Purpose, s.TargetWords, s.KeyPointsJSON, s.EmotionalRole)
	}
	return b
}

func toSQLiteGenerationLog(in ports.ScriptGenerationLog) sqlitescripts.ScriptGenerationLog {
	return sqlitescripts.ScriptGenerationLog{
		ScriptID: in.ScriptID, Phase: in.Phase, PromptHash: in.PromptHash,
		Model: in.Model, InputWords: in.InputWords, OutputWords: in.OutputWords,
		DurationMs: in.DurationMs, RetryCount: in.RetryCount,
		CacheStatus: in.CacheStatus, Error: in.Error,
	}
}

// ── Interface methods ───────────────────────────────────────────────

func (a *sqliteRepoAdapter) SaveScript(ctx context.Context, rec *ports.ScriptRecord, sections []ports.ScriptSectionRecord, matches []ports.ScriptStockMatchRecord) (int64, error) {
	return a.inner.SaveScript(ctx, toSQLiteScriptRecord(rec), buildSectionRows(sections).Slice(), buildStockMatchRows(matches).Slice())
}

func (a *sqliteRepoAdapter) UpdateScriptFinalContent(ctx context.Context, scriptID int64, outputText string, wordCount int, status, metadata, model, ollamaBaseURL string, version int) error {
	return a.inner.UpdateScriptFinalContent(ctx, scriptID, outputText, wordCount, status, metadata, model, ollamaBaseURL, version)
}

func (a *sqliteRepoAdapter) SaveGenerationLog(ctx context.Context, log ports.ScriptGenerationLog) error {
	return a.inner.SaveGenerationLog(ctx, toSQLiteGenerationLog(log))
}

func (a *sqliteRepoAdapter) SaveOutlineSections(ctx context.Context, scriptID int64, sections []ports.ScriptOutlineSectionRecord) error {
	return a.inner.SaveOutlineSections(ctx, scriptID, buildOutlineSectionRows(sections).Slice())
}

func (a *sqliteRepoAdapter) SaveResearchSources(ctx context.Context, scriptID int64, sources []ports.ScriptResearchSource) error {
	return a.inner.SaveResearchSources(ctx, scriptID, toSQLiteResearchSources(sources))
}

func (a *sqliteRepoAdapter) NextVersionForTopic(ctx context.Context, topic, language, mode string) (int, error) {
	return a.inner.NextVersionForTopic(ctx, topic, language, mode)
}

func (a *sqliteRepoAdapter) GetSectionByID(ctx context.Context, sectionID int64) (*ports.ScriptSectionRecord, error) {
	sec, err := a.inner.GetSectionByID(ctx, sectionID)
	if err != nil || sec == nil {
		return nil, err
	}
	return &ports.ScriptSectionRecord{
		ID: sec.ID, ScriptID: sec.ScriptID, SectionType: sec.SectionType,
		SectionTitle: sec.SectionTitle, Content: sec.Content,
		SortOrder: sec.SortOrder, WordCount: sec.WordCount, Status: sec.Status,
	}, nil
}

func (a *sqliteRepoAdapter) GetScriptByID(id int64) (*ports.ScriptRecord, []ports.ScriptSectionRecord, []ports.ScriptStockMatchRecord, error) {
	rec, sections, matches, err := a.inner.GetScriptByID(id)
	if err != nil {
		return nil, nil, nil, err
	}
	outSections := make([]ports.ScriptSectionRecord, 0, len(sections))
	sqlitescripts.EachSectionRow(sections, func(id, scriptID int64, sectionType, sectionTitle, content string, sortOrder, wordCount int, status string) {
		outSections = append(outSections, ports.ScriptSectionRecord{
			ID: id, ScriptID: scriptID, Index: sortOrder,
			SectionType: sectionType, SectionTitle: sectionTitle,
			Content: content, SortOrder: sortOrder,
			WordCount: wordCount, Status: status,
		})
	})
	outMatches := make([]ports.ScriptStockMatchRecord, 0, len(matches))
	sqlitescripts.EachStockMatchRow(matches, func(id, scriptID int64, segmentIndex int, stockPath, stockSource string, score float64, matchedTerms string) {
		outMatches = append(outMatches, ports.ScriptStockMatchRecord{
			ID: id, ScriptID: scriptID, SegmentIndex: segmentIndex,
			StockPath: stockPath, StockSource: stockSource,
			Score: score, MatchedTerms: matchedTerms,
		})
	})
	return fromSQLiteScriptRecord(rec), outSections, outMatches, nil
}

func (a *sqliteRepoAdapter) GetAdjacentSections(ctx context.Context, scriptID int64, sortOrder int) (prev, next *ports.ScriptSectionRecord, err error) {
	sp, sn, err := a.inner.GetAdjacentSections(ctx, scriptID, sortOrder)
	if err != nil {
		return nil, nil, err
	}
	if sp != nil {
		r := ports.ScriptSectionRecord{
			ID: sp.ID, ScriptID: sp.ScriptID, SectionType: sp.SectionType,
			SectionTitle: sp.SectionTitle, Content: sp.Content,
			SortOrder: sp.SortOrder, WordCount: sp.WordCount, Status: sp.Status,
		}
		prev = &r
	}
	if sn != nil {
		r := ports.ScriptSectionRecord{
			ID: sn.ID, ScriptID: sn.ScriptID, SectionType: sn.SectionType,
			SectionTitle: sn.SectionTitle, Content: sn.Content,
			SortOrder: sn.SortOrder, WordCount: sn.WordCount, Status: sn.Status,
		}
		next = &r
	}
	return prev, next, nil
}

func (a *sqliteRepoAdapter) UpdateSectionContent(ctx context.Context, sectionID int64, content string) error {
	return a.inner.UpdateSectionContent(ctx, sectionID, content)
}

func (a *sqliteRepoAdapter) ListScripts(ctx context.Context, filter ports.ScriptListFilter) ([]*ports.ScriptRecord, error) {
	recs, _, err := a.inner.ListScripts(filter.Limit, filter.Offset, filter.Language, filter.Topic)
	if err != nil {
		return nil, err
	}
	out := make([]*ports.ScriptRecord, len(recs))
	for i := range recs {
		out[i] = fromSQLiteScriptRecord(&recs[i])
	}
	return out, nil
}

func (a *sqliteRepoAdapter) FindScriptByIdempotencyKey(ctx context.Context, itemID, cacheKey, promptVersion string, targetWords int, language string) (*ports.ScriptRecord, bool, error) {
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

func computeAdapterIdempotencyKey(itemID, cacheKey, promptVersion string, targetWords int, language string) string {
	tuple := fmt.Sprintf("%s|%s|%s|%d|%s", itemID, cacheKey, promptVersion, targetWords, language)
	sum := sha256.Sum256([]byte(tuple))
	return hex.EncodeToString(sum[:])[:16]
}
