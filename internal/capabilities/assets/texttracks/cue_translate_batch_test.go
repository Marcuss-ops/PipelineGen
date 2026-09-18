package texttracks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// recordingBatchTranslator implements BOTH translation ports: the per-cue
// TranslationPort and the optional BatchTranslationPort. It records which path
// was used so a test can assert the call count reduction, and it can be told
// to fail / misalign the batched answer.
type recordingBatchTranslator struct {
	target string

	mu          sync.Mutex
	batchSizes  []int
	perCueCalls []string
	chunkSizes  []int

	failBatch bool
	misalign  bool
}

func (r *recordingBatchTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	r.mu.Lock()
	r.perCueCalls = append(r.perCueCalls, cmd.Text)
	r.mu.Unlock()
	return translation.TranslationResult{TranslatedText: cmd.Text + " [" + r.target + "]"}, nil
}

func (r *recordingBatchTranslator) TranslateBatch(_ context.Context, cmd translation.BatchTranslationCommand) (translation.BatchTranslationResult, error) {
	r.mu.Lock()
	r.batchSizes = append(r.batchSizes, len(cmd.Segments))
	r.chunkSizes = append(r.chunkSizes, cmd.ChunkSize)
	r.mu.Unlock()

	if r.failBatch {
		return translation.BatchTranslationResult{}, errors.New("batched contract violated")
	}
	segments := make([]translation.BatchTranslationSegment, 0, len(cmd.Segments))
	for i, segment := range cmd.Segments {
		if r.misalign && i == 0 {
			continue // drop the first id: a misaligned answer
		}
		segments = append(segments, translation.BatchTranslationSegment{
			ID:   segment.ID,
			Text: segment.Text + " [" + r.target + "]",
		})
	}
	return translation.BatchTranslationResult{Segments: segments, UsedProvider: translation.ProviderOllama}, nil
}

func (r *recordingBatchTranslator) counts() (batches, perCue int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batchSizes), len(r.perCueCalls)
}

func batchTestCues(n int) []detail.TimedCue {
	cues := make([]detail.TimedCue, n)
	for i := range cues {
		cues[i] = detail.TimedCue{
			StartMs: int64(i * 1000),
			EndMs:   int64(i*1000 + 900),
			Text:    fmt.Sprintf("cue %d", i),
		}
	}
	return cues
}

// TestCueTranslator_BatchesCuesIntoChunks pins the call-count reduction: 25
// cues at chunk size 12 cost 3 provider requests, not 25 — and the 1:1 timing
// mapping is unchanged.
func TestCueTranslator_BatchesCuesIntoChunks(t *testing.T) {
	tr := &recordingBatchTranslator{target: "it"}
	ct := NewCueTranslator(tr, "en", "gemma4:e4b", 2, nil)
	cues := batchTestCues(25)

	got, _, err := ct.Translate(context.Background(), cues, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	batches, perCue := tr.counts()
	if batches != 3 {
		t.Fatalf("batched provider calls = %d, want 3 (25 cues / chunk 12)", batches)
	}
	if perCue != 0 {
		t.Fatalf("per-cue provider calls = %d, want 0 when batching succeeds", perCue)
	}
	if len(got) != 25 {
		t.Fatalf("len(got) = %d, want 25", len(got))
	}
	for i := range cues {
		if got[i].StartMs != cues[i].StartMs || got[i].EndMs != cues[i].EndMs {
			t.Fatalf("cue %d timing drifted: got [%d,%d] want [%d,%d]",
				i, got[i].StartMs, got[i].EndMs, cues[i].StartMs, cues[i].EndMs)
		}
		if got[i].Text != cues[i].Text+" [it]" {
			t.Fatalf("cue %d text = %q, want the translated suffix", i, got[i].Text)
		}
	}
}

// TestCueTranslator_BatchFailureFallsBackToPerCue pins the fallback floor: a
// batched contract violation must not lose a cue — every cue is retried on the
// per-cue path and the language completes.
func TestCueTranslator_BatchFailureFallsBackToPerCue(t *testing.T) {
	tr := &recordingBatchTranslator{target: "it", failBatch: true}
	ct := NewCueTranslator(tr, "en", "gemma4:e4b", 2, nil)
	cues := batchTestCues(5)

	got, _, err := ct.Translate(context.Background(), cues, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	batches, perCue := tr.counts()
	if batches != 1 {
		t.Fatalf("batched provider calls = %d, want 1 attempted chunk", batches)
	}
	if perCue != 5 {
		t.Fatalf("per-cue fallback calls = %d, want 5", perCue)
	}
	for i := range got {
		if got[i].Text != cues[i].Text+" [it]" {
			t.Fatalf("cue %d text = %q, want per-cue fallback text", i, got[i].Text)
		}
	}
}

// TestCueTranslator_MisalignedBatchFallsBackToPerCue pins the id contract: a
// batched answer that drops a segment is a contract violation, never a partial
// success that leaves one cue in the source language.
func TestCueTranslator_MisalignedBatchFallsBackToPerCue(t *testing.T) {
	tr := &recordingBatchTranslator{target: "it", misalign: true}
	ct := NewCueTranslator(tr, "en", "gemma4:e4b", 1, nil)
	cues := batchTestCues(3)

	got, _, err := ct.Translate(context.Background(), cues, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if _, perCue := tr.counts(); perCue != 3 {
		t.Fatalf("per-cue fallback calls = %d, want 3", perCue)
	}
	for i := range got {
		if !strings.HasSuffix(got[i].Text, "[it]") {
			t.Fatalf("cue %d text = %q, want a translated cue", i, got[i].Text)
		}
	}
}

// TestCueTranslator_BatchingDisabledUsesPerCue pins the operator escape hatch:
// chunk size 0 forces the per-cue path (the deterministic comparison run).
func TestCueTranslator_BatchingDisabledUsesPerCue(t *testing.T) {
	tr := &recordingBatchTranslator{target: "it"}
	ct := NewCueTranslator(tr, "en", "gemma4:e4b", 2, nil)
	ct.SetBatchChunkSize(0)
	cues := batchTestCues(4)

	if _, _, err := ct.Translate(context.Background(), cues, "it"); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	batches, perCue := tr.counts()
	if batches != 0 || perCue != 4 {
		t.Fatalf("batches=%d perCue=%d, want 0/4 with batching disabled", batches, perCue)
	}
}

// TestCueTranslator_NoCuesIssuesNoRequest pins the empty-input edge: no work,
// no provider call.
func TestCueTranslator_NoCuesIssuesNoRequest(t *testing.T) {
	tr := &recordingBatchTranslator{target: "it"}
	ct := NewCueTranslator(tr, "en", "", 2, nil)
	got, _, err := ct.Translate(context.Background(), nil, "it")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
	if batches, perCue := tr.counts(); batches != 0 || perCue != 0 {
		t.Fatalf("batches=%d perCue=%d, want no provider call for no cues", batches, perCue)
	}
}

func TestChunkIndexes(t *testing.T) {
	cases := []struct {
		name  string
		count int
		size  int
		want  [][]int
	}{
		{name: "exact split", count: 4, size: 2, want: [][]int{{0, 1}, {2, 3}}},
		{name: "remainder", count: 5, size: 2, want: [][]int{{0, 1}, {2, 3}, {4}}},
		{name: "single chunk", count: 3, size: 10, want: [][]int{{0, 1, 2}}},
		{name: "empty", count: 0, size: 4, want: nil},
		{name: "invalid size", count: 3, size: 0, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chunkIndexes(tc.count, tc.size)
			if len(got) != len(tc.want) {
				t.Fatalf("chunks = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if len(got[i]) != len(tc.want[i]) {
					t.Fatalf("chunk %d = %v, want %v", i, got[i], tc.want[i])
				}
				for j := range tc.want[i] {
					if got[i][j] != tc.want[i][j] {
						t.Fatalf("chunk %d = %v, want %v", i, got[i], tc.want[i])
					}
				}
			}
		})
	}
}
