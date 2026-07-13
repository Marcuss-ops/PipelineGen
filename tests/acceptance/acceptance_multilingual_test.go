// Package acceptance_test — acceptance_multilingual_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (b).
//
// User spec — "multilingua — per ogni lingua configurata
// transcript+description current con source_text_hash e
// segmenti/timestamp invariati".
//
// Cover:
//   - For every configured target language, transcript +
//     description reach READY + IsCurrent=1 in one Materialize.
//   - Each target row carries the source_text_hash verbatim
//     (SHA-256 of the source TextContent).
//   - Source segment / timestamp data are NOT mutated by
//     Materialize (the translation step preserves parent
//     segment hash metadata; segments are the source's, not
//     the target's).
//   - AssetIndexRequested fires once per kind batch (NOT once
//     per language) — language fan-out is one event.
package acceptance_test

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TestMultilingual_AllConfiguredLanguagesReachReady: for each
// (transcript, description) × N target languages, the
// Materializer must produce ALL tracks Ready + IsCurrent with
// the source_text_hash propagated.
func TestMultilingual_AllConfiguredLanguagesReachReady(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-multi-001"
		srcLang = "en"
	)

	targetLangs := []string{"it", "es", "fr", "de"}
	srcTranscript := "the quick brown fox jumps over the lazy dog"
	srcDesc := "a brown fox clip"
	srcTranscriptHash := sha256Hex(srcTranscript)
	srcDescHash := sha256Hex(srcDesc)

	// Seed source-language tracks for both kinds.
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{
		newSourceTrack(assetID, srcLang, string(asset.TextTrackTranscript), srcTranscript),
		newSourceTrack(assetID, srcLang, string(asset.TextTrackDescription), srcDesc),
	}); err != nil {
		t.Fatalf("seed source tracks: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)

	// Materialize transcripts.
	repT, err := m.Materialize(ctx, assetID, srcLang, srcTranscriptHash, asset.TextTrackTranscript, targetLangs)
	if err != nil {
		t.Fatalf("Materialize(Transcript): %v", err)
	}
	if len(repT.CreatedLanguages) != len(targetLangs) {
		t.Fatalf("CreatedLanguages(transcript) = %d, want %d", len(repT.CreatedLanguages), len(targetLangs))
	}

	// Materialize descriptions.
	repD, err := m.Materialize(ctx, assetID, srcLang, srcDescHash, asset.TextTrackDescription, targetLangs)
	if err != nil {
		t.Fatalf("Materialize(Description): %v", err)
	}
	if len(repD.CreatedLanguages) != len(targetLangs) {
		t.Fatalf("CreatedLanguages(description) = %d, want %d", len(repD.CreatedLanguages), len(targetLangs))
	}

	// Verify every target track exists AND has SourceTextHash
	// equal to the source-track — language fan-out must
	// inherit the parent's hash verbatim (PR-CATALOG-MULTILINGUA
	// step 2 invariant).
	wantTranscriptTracks := len(targetLangs) + 1 // targets + 1 source
	wantDescTracks := len(targetLangs) + 1

	allT, err := repo.ListByAsset(ctx, assetID)
	if err != nil {
		t.Fatalf("ListByAsset: %v", err)
	}
	tCount := 0
	dCount := 0
	for _, trk := range allT {
		switch trk.TextKind {
		case asset.TextTrackTranscript:
			tCount++
			if trk.LanguageCode != srcLang {
				// Target track: source_text_hash must match the source's hash.
				if trk.SourceTextHash != srcTranscriptHash {
					t.Errorf("transcript[%s].source_text_hash = %q, want %q",
						trk.LanguageCode, trk.SourceTextHash, srcTranscriptHash)
				}
				if !trk.IsCurrent {
					t.Errorf("transcript[%s].is_current = false, want true", trk.LanguageCode)
				}
				if trk.Status != asset.TextTrackReady {
					t.Errorf("transcript[%s].status = %q, want READY", trk.LanguageCode, trk.Status)
				}
			}
		case asset.TextTrackDescription:
			dCount++
			if trk.LanguageCode != srcLang {
				if trk.SourceTextHash != srcDescHash {
					t.Errorf("description[%s].source_text_hash = %q, want %q",
						trk.LanguageCode, trk.SourceTextHash, srcDescHash)
				}
				if !trk.IsCurrent {
					t.Errorf("description[%s].is_current = false, want true", trk.LanguageCode)
				}
			}
		}
	}
	if tCount != wantTranscriptTracks {
		t.Errorf("transcript track count = %d, want %d (1 source + %d targets)", tCount, wantTranscriptTracks, len(targetLangs))
	}
	if dCount != wantDescTracks {
		t.Errorf("description track count = %d, want %d (1 source + %d targets)", dCount, wantDescTracks, len(targetLangs))
	}
}

// TestMultilingual_AssetIndexRequestedFiresOncePerKind: language
// fan-out is one outbox event per Materialize call, not one per
// target language. This pin prevents N-event spam (a known
// regression shape in PR-CATALOG-MULTILINGUA reviews).
func TestMultilingual_AssetIndexRequestedFiresOncePerKind(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-multi-002"
		srcLang = "en"
	)
	src := "the show must go on"
	srcHash := sha256Hex(src)

	if err := repo.UpsertBatch(ctx, []asset.TextTrack{
		newSourceTrack(assetID, srcLang, string(asset.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript,
		[]string{"it", "es", "fr", "de", "pt", "ru", "ja", "zh", "ko", "ar"}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// Exactly ONE asset.index.requested event regardless of N.
	if got := ob.EnqueuedCount(); got != 1 {
		t.Errorf("EnqueuedCount = %d, want 1 (10 fans-out → 1 event)", got)
	}
	for _, e := range ob.All() {
		if e.Action != outboxActionEnqueued {
			continue
		}
		if e.EventType != "asset.index.requested" {
			t.Errorf("event_type = %q, want asset.index.requested", e.EventType)
		}
		if e.AggregateID != assetID {
			t.Errorf("aggregate_id = %q, want %q", e.AggregateID, assetID)
		}
	}
}

// TestMultilingual_SourceSegmentsAreReadOnly: the Materializer
// does NOT mutate the source track's segment data. FindReady on
// the source-language row must return the original TextContent
// byte-identical after Materialize has produced 10 outbound
// translations.
func TestMultilingual_SourceSegmentsAreReadOnly(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-multi-003"
		srcLang = "en"
	)
	src := "the source text must remain unchanged after fan-out"
	srcHash := sha256Hex(src)
	if err := repo.UpsertBatch(ctx, []asset.TextTrack{newSourceTrack(assetID, srcLang, string(asset.TextTrackTranscript), src)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, asset.TextTrackTranscript,
		[]string{"it", "es", "fr", "de"}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	sourceAfter, _, _ := repo.FindReady(ctx, assetID, srcLang, asset.TextTrackTranscript)
	if sourceAfter == nil {
		t.Fatalf("source track missing after Materialize")
	}
	if sourceAfter.TextContent != src {
		t.Errorf("source TextContent mutated: got %q, want %q", sourceAfter.TextContent, src)
	}
	if sourceAfter.TextHash != srcHash {
		t.Errorf("source TextHash mutated: got %q, want %q", sourceAfter.TextHash, srcHash)
	}
	if !sourceAfter.IsCurrent {
		t.Errorf("source IsCurrent flipped to false post-fan-out: should remain the live row")
	}
}
