// Package scripts — source_resolver_search_semantics_test.go pins
// the semantic-correctness regression from the review: a query that
// names a single referent ("Jackie Chan") MUST NOT accept a clip
// whose evidence only partially overlaps the name. The clip sampler
// topic_relevance gate is the deterministic guardrail (ALL meaningful
// tokens required); this resolver-level test proves the invariant
// end-to-end through the canonical SearchSourceResolver + sampler
// registry, with fixture clips known to the test.
package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// TestSourceSearch_JackieChan_DoesNotAcceptTomHollandClip is the
// review case: query "Jackie Chan" returns two semantic hits — a
// genuine Jackie Chan clip and a Tom Holland interview whose
// transcript mentions "the Chan era" (only the surname token).
// The old any-token gate accepted the Tom Holland clip; the
// ALL-token rule must reject it while accepting the Jackie clip.
func TestSourceSearch_JackieChan_DoesNotAcceptTomHollandClip(t *testing.T) {
	resolver := newFakeClipResolver()
	resolver.AddClip(makeTestClip("clip-jackie", "Jackie Chan interview", 6*time.Second))
	resolver.AddClip(makeTestClip("clip-tom", "Tom Holland interview", 6*time.Second))

	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	search := &recordingSearchPort{
		results: []SemanticSearchResult{
			{
				ClipID:              "clip-jackie",
				Name:                "Jackie Chan interview",
				Score:               0.90,
				Transcript:          "Jackie Chan demonstrates martial arts and shares career stories in this interview.",
				VisualSummary:       "Jackie Chan demonstrates martial arts on camera.",
				MediaType:           "video",
				DriveLink:           "https://drive.google.com/file/d/drive-clip-jackie/view",
				AnchorCoverageRatio: 1.0,
			},
			{
				ClipID:              "clip-tom",
				Name:                "Tom Holland interview",
				Score:               0.85,
				Transcript:          "Tom Holland talks about his latest interview and the Chan era of martial arts cinema.",
				VisualSummary:       "Tom Holland in a relaxed interview.",
				MediaType:           "video",
				DriveLink:           "https://drive.google.com/file/d/drive-clip-tom/view",
				AnchorCoverageRatio: 1.0,
			},
		},
	}

	r := NewSearchSourceResolver(search, builder, NewClipSamplerRegistry(), zap.NewNop())
	resolved, err := r.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:     scriptpkg.SourceSearch,
		Query:    "Jackie Chan",
		MaxClips: 10,
	}, makeTestResCtx())
	if err != nil {
		t.Fatalf("search resolve failed: %v", err)
	}
	if resolved.ClipEvidence == nil {
		t.Fatal("expected ClipEvidence on the resolved source")
	}

	accepted := resolved.ClipEvidence.AcceptedClipIDs
	if !slicesEqual(accepted, []string{"clip-jackie"}) {
		t.Fatalf("accepted clip IDs = %v, want exactly [clip-jackie] (Tom Holland partial-overlap must be rejected)", accepted)
	}
	for _, id := range accepted {
		if id == "clip-tom" {
			t.Fatalf("Tom Holland clip accepted for query %q: partial surname overlap must fail the topic_relevance gate", "Jackie Chan")
		}
	}
	if !strings.Contains(search.lastQuery, "Jackie") || !strings.Contains(search.lastQuery, "Chan") {
		t.Fatalf("semantic search received query %q, want the full identity query", search.lastQuery)
	}
	if len(resolved.SearchResults) != 1 || resolved.SearchResults[0].ClipID != "clip-jackie" {
		t.Fatalf("SearchResults = %+v, want only the accepted Jackie Chan clip", resolved.SearchResults)
	}
}
