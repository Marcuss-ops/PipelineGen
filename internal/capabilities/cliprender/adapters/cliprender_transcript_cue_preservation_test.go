package adapters

import (
	"context"
	"errors"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type cuePreservationRepo struct {
	detail.TextTrackRepository
	tracks  []detail.TextTrack
	cues    map[string][]detail.TimedCue
	listErr error
	findErr error
}

func (r *cuePreservationRepo) UpsertBatch(context.Context, []detail.TextTrack) error { return nil }

func (r *cuePreservationRepo) ListByAsset(context.Context, string) ([]detail.TextTrack, error) {
	return r.tracks, r.listErr
}

func (r *cuePreservationRepo) FindReady(_ context.Context, _ string, languageCode string, _ detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	if r.findErr != nil {
		return nil, nil, r.findErr
	}
	for i := range r.tracks {
		if r.tracks[i].LanguageCode == languageCode {
			return &r.tracks[i], r.cues[languageCode], nil
		}
	}
	return nil, nil, nil
}

type cuePreservationWriter struct {
	got map[string][]detail.TimedCue
}

func (w *cuePreservationWriter) ReplaceTranscriptCues(_ context.Context, _ string, byLang map[string][]detail.TimedCue) error {
	w.got = byLang
	return nil
}

func TestPersistResult_PreservesOtherLanguageCues(t *testing.T) {
	itCues := []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "ciao"}}
	repo := &cuePreservationRepo{
		tracks: []detail.TextTrack{
			{LanguageCode: "it", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady},
			{LanguageCode: "en", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady},
		},
		cues: map[string][]detail.TimedCue{"it": itCues},
	}
	writer := &cuePreservationWriter{}
	resolver := &ClipRenderTranscriptResolver{repo: repo, cueWriter: writer, log: zap.NewNop()}

	err := resolver.persistResult(context.Background(), "asset-1", &cliprender.MaterializedAsset{AssetID: "asset-1"},
		&cliprender.TranscriptResult{
			Language:         "en",
			DetectedLanguage: "en",
			Text:             "hello",
			TextSHA256:       "hash-en",
			StreamSourceType: string(detail.TextSourceWhisper),
			Cues:             []cliprender.Cue{{StartMs: 0, EndMs: 900, Text: "hello"}},
		})
	if err != nil {
		t.Fatalf("persistResult: %v", err)
	}
	if len(writer.got["it"]) != 1 || writer.got["it"][0].Text != "ciao" {
		t.Fatalf("italian cues not preserved: %+v", writer.got["it"])
	}
	if len(writer.got["en"]) != 1 || writer.got["en"][0].Text != "hello" {
		t.Fatalf("new english cues missing: %+v", writer.got["en"])
	}
}

func TestPersistResult_CueLookupFailureDoesNotWipeOtherLanguages(t *testing.T) {
	repo := &cuePreservationRepo{
		tracks: []detail.TextTrack{
			{LanguageCode: "it", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady},
			{LanguageCode: "en", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady},
		},
		listErr: errors.New("db down"),
	}
	writer := &cuePreservationWriter{}
	resolver := &ClipRenderTranscriptResolver{repo: repo, cueWriter: writer, log: zap.NewNop()}

	err := resolver.persistResult(context.Background(), "asset-1", &cliprender.MaterializedAsset{AssetID: "asset-1"},
		&cliprender.TranscriptResult{
			Language: "en",
			Text:     "hello",
			Cues:     []cliprender.Cue{{StartMs: 0, EndMs: 900, Text: "hello"}},
		})
	if err == nil {
		t.Fatal("persistResult should fail closed when existing cues cannot be read")
	}
	if writer.got != nil {
		t.Fatalf("ReplaceTranscriptCues must not run after a cue-read failure: %+v", writer.got)
	}
}
