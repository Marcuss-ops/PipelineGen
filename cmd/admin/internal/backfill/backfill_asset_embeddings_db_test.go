package backfill

import (
	"context"
	"reflect"
	"sort"
	"testing"

	indexing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/backfill"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

type embeddingCandidateSourceStub struct {
	rows []pgmedia.EmbeddingCandidate
}

func (s *embeddingCandidateSourceStub) ListEmbeddingBackfillCandidates(_ context.Context, q pgmedia.EmbeddingCandidateQuery) ([]pgmedia.EmbeddingCandidate, error) {
	rows := make([]pgmedia.EmbeddingCandidate, 0, len(s.rows))
	for _, row := range s.rows {
		if q.Source != "" && row.Source != q.Source {
			continue
		}
		if q.AfterID != "" && row.ID <= q.AfterID {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

func (s *embeddingCandidateSourceStub) ListEmbeddingCandidatesByID(_ context.Context, ids []string) ([]pgmedia.EmbeddingCandidate, error) {
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	rows := make([]pgmedia.EmbeddingCandidate, 0, len(ids))
	for _, row := range s.rows {
		if _, ok := wanted[row.ID]; ok {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func seedEmbeddingCandidate(s *embeddingCandidateSourceStub, id, source string, hasText, hasTranscript, hasVisual, hasAudio bool) {
	s.rows = append(s.rows, pgmedia.EmbeddingCandidate{
		ID:            id,
		Source:        source,
		MediaType:     "video",
		HasText:       hasText,
		HasTranscript: hasTranscript,
		HasVisual:     hasVisual,
		HasAudio:      hasAudio,
	})
}

// TestFetchEmbeddingCandidates_OnlyMissing verifies that the CLI helper keeps
// only rows with at least one missing embedding when --only-missing is set.
// Eligibility itself belongs to the PostgreSQL BackfillReader contract.
func TestFetchEmbeddingCandidates_OnlyMissing(t *testing.T) {
	source := &embeddingCandidateSourceStub{}
	seedEmbeddingCandidate(source, "a1", "stock", false, true, true, true)
	seedEmbeddingCandidate(source, "a2", "stock", true, true, true, true)

	cands, err := fetchEmbeddingCandidates(context.Background(), source, indexing.Deps{OnlyMissing: true}, nil)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates: %v", err)
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	if want := []string{"a1"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("candidates = %v, want %v", ids, want)
	}
}

// TestFetchEmbeddingCandidates_AllModeIncludesComplete pins that --all
// (OnlyMissing=false) also returns fully-embedded rows.
func TestFetchEmbeddingCandidates_AllModeIncludesComplete(t *testing.T) {
	source := &embeddingCandidateSourceStub{}
	seedEmbeddingCandidate(source, "a1", "stock", true, true, true, true)
	seedEmbeddingCandidate(source, "a2", "stock", false, false, false, false)

	cands, err := fetchEmbeddingCandidates(context.Background(), source, indexing.Deps{OnlyMissing: false}, nil)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates: %v", err)
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("candidates = %v, want %v", ids, want)
	}
}

// TestFetchEmbeddingCandidates_SourceFilterAndResume pins the source filter
// and the resume anchor (id > last processed).
func TestFetchEmbeddingCandidates_SourceFilterAndResume(t *testing.T) {
	source := &embeddingCandidateSourceStub{}
	seedEmbeddingCandidate(source, "b1", "stock", false, false, false, false)
	seedEmbeddingCandidate(source, "b2", "youtube", false, false, false, false)

	cands, err := fetchEmbeddingCandidates(context.Background(), source, indexing.Deps{OnlyMissing: true, Source: "stock"}, nil)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].ID != "b1" {
		t.Fatalf("source-filtered candidates = %+v, want [b1]", cands)
	}

	cp := &indexing.Checkpoint{LastProcessedID: "b1"}
	cands, err = fetchEmbeddingCandidates(context.Background(), source, indexing.Deps{OnlyMissing: true, Resume: true}, cp)
	if err != nil {
		t.Fatalf("fetchEmbeddingCandidates resume: %v", err)
	}
	if len(cands) != 1 || cands[0].ID != "b2" {
		t.Fatalf("resume candidates = %+v, want [b2]", cands)
	}
}
