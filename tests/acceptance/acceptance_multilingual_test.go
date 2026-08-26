// Package acceptance_test — acceptance_multilingual_test.go
//
// PR-CLIPINGEST-PIPELINE step 11 (July 2026): category (b).
//
// User spec — "multilingua — per ogni lingua configurata
// transcript+description current con source_text_hash e
// segmenti/timestamp invariati".
//
// SourceTextHash byte-equality invariant: every fan-out target
// row's SourceTextHash MUST equal the source row's SHA-256 of
// the source TextContent. This is enforced at production by
// materializer.go::materializeOne (`SourceTextHash: report.SourceTextHash`).
// The acceptance tests below pin BYTE-EQUALITY (not just
// shape) — the "segments/timestamp invariati" half of the
// user spec maps to the fan-out carrier never drifting from
// the source's content fingerprint.
package acceptance_test

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"testing"
)

func TestMultilingual_AllConfiguredLanguagesReachReady(t *testing.T) {
	ctx := context.Background()
	repo := newInMemRepo()
	ob := newRecordingOutbox()
	tr := newMockTranslator()

	const (
		assetID = "asset-multi-001"
		srcLang = "en"
	)

	targetLangs := []string{"it", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "id"}
	srcTranscript := "the quick brown fox jumps over the lazy dog"
	srcDesc := "a brown fox clip"
	srcTranscriptHash := sha256Hex(srcTranscript)
	srcDescHash := sha256Hex(srcDesc)

	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), srcTranscript),
		newSourceTrack(assetID, srcLang, string(detail.TextTrackDescription), srcDesc),
	}); err != nil {
		t.Fatalf("seed source tracks: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)

	repT, err := m.Materialize(ctx, assetID, srcLang, srcTranscriptHash, detail.TextTrackTranscript, targetLangs)
	if err != nil {
		t.Fatalf("Materialize(Transcript): %v", err)
	}
	if len(repT.CreatedLanguages) != len(targetLangs) {
		t.Fatalf("CreatedLanguages(transcript) = %d, want %d", len(repT.CreatedLanguages), len(targetLangs))
	}

	repD, err := m.Materialize(ctx, assetID, srcLang, srcDescHash, detail.TextTrackDescription, targetLangs)
	if err != nil {
		t.Fatalf("Materialize(Description): %v", err)
	}
	if len(repD.CreatedLanguages) != len(targetLangs) {
		t.Fatalf("CreatedLanguages(description) = %d, want %d", len(repD.CreatedLanguages), len(targetLangs))
	}

	wantTranscriptTracks := len(targetLangs) + 1
	wantDescTracks := len(targetLangs) + 1

	allT, err := repo.ListByAsset(ctx, assetID)
	if err != nil {
		t.Fatalf("ListByAsset: %v", err)
	}
	tCount := 0
	dCount := 0
	for _, trk := range allT {
		switch trk.TextKind {
		case detail.TextTrackTranscript:
			tCount++
			if trk.LanguageCode != srcLang {
				if trk.SourceTextHash != srcTranscriptHash {
					t.Errorf("transcript[%s].source_text_hash = %q, want %q (byte-equality invariant)",
						trk.LanguageCode, trk.SourceTextHash, srcTranscriptHash)
				}
				if !trk.IsCurrent {
					t.Errorf("transcript[%s].is_current = false, want true", trk.LanguageCode)
				}
				if trk.Status != detail.TextTrackReady {
					t.Errorf("transcript[%s].status = %q, want READY", trk.LanguageCode, trk.Status)
				}
			}
		case detail.TextTrackDescription:
			dCount++
			if trk.LanguageCode != srcLang {
				if trk.SourceTextHash != srcDescHash {
					t.Errorf("description[%s].source_text_hash = %q, want %q (byte-equality invariant)",
						trk.LanguageCode, trk.SourceTextHash, srcDescHash)
				}
				if !trk.IsCurrent {
					t.Errorf("description[%s].is_current = false, want true", trk.LanguageCode)
				}
			}
		}
	}
	if tCount != wantTranscriptTracks {
		t.Errorf("transcript track count = %d, want %d", tCount, wantTranscriptTracks)
	}
	if dCount != wantDescTracks {
		t.Errorf("description track count = %d, want %d", dCount, wantDescTracks)
	}
}

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

	if err := repo.UpsertBatch(ctx, []detail.TextTrack{
		newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript,
		[]string{"it", "es", "fr", "de", "pt", "ru", "ja", "zh", "ko", "ar"}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if got := ob.EnqueuedCount(); got != 1 {
		t.Errorf("EnqueuedCount = %d, want 1 (10 fan-out → 1 event)", got)
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
	if err := repo.UpsertBatch(ctx, []detail.TextTrack{newSourceTrack(assetID, srcLang, string(detail.TextTrackTranscript), src)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m := newMaterializer(t, repo, tr, ob)
	if _, err := m.Materialize(ctx, assetID, srcLang, srcHash, detail.TextTrackTranscript,
		[]string{"it", "es", "fr", "de"}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	sourceAfter, _, _ := repo.FindReady(ctx, assetID, srcLang, detail.TextTrackTranscript)
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
		t.Errorf("source IsCurrent flipped to false post-fan-out")
	}
}
