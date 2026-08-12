package enrichment

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"
)

type clipTracks struct {
	ready  *asset.TextTrack
	cues   []asset.TimedCue
	writes [][]asset.TextTrack
}

func (r *clipTracks) FindReady(context.Context, string, string, asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return r.ready, r.cues, nil
}
func (r *clipTracks) UpsertBatch(_ context.Context, t []asset.TextTrack) error {
	r.writes = append(r.writes, t)
	return nil
}

type clipWhisper struct{ calls int }

func (w *clipWhisper) TranscribeAudioWithDetection(context.Context, string) (asset.TranscriptResult, error) {
	w.calls++
	return asset.TranscriptResult{Text: "hello", DetectedLanguage: "en", Cues: []asset.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hello"}}}, nil
}

type clipSubs struct{ calls int }

func (s *clipSubs) Write(context.Context, SubtitleInput) error { s.calls++; return nil }

type clipDesc struct{ calls int }

func (d *clipDesc) Describe(context.Context, DescriptionInput) (DescriptionOutput, error) {
	d.calls++
	return DescriptionOutput{Description: "A speaker addresses the audience in a comedy interview, with a second person reacting in the background.", Summary: "Comedy interview exchange."}, nil
}

type clipReindex struct{ calls int }

func (r *clipReindex) RequestAsset(context.Context, string) error { r.calls++; return nil }
func TestClipServiceOrderAndReuse(t *testing.T) {
	w, s, d, r := &clipWhisper{}, &clipSubs{}, &clipDesc{}, &clipReindex{}
	tr := &clipTracks{}
	svc, err := NewClipService(w, s, d, tr, r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Process(context.Background(), ClipInput{AssetID: "a", LocalPath: "/clip.mp4", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || s.calls != 1 || d.calls != 1 || r.calls != 1 || !got.TranscriptCreated || !got.DescriptionCreated || !got.SummaryCreated {
		t.Fatalf("unexpected result %+v calls %d %d %d %d", got, w.calls, s.calls, d.calls, r.calls)
	}
	tr.ready = &asset.TextTrack{AssetID: "a", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "existing", Status: asset.TextTrackReady}
	tr.cues = []asset.TimedCue{{StartMs: 0, EndMs: 1000, Text: "existing"}}
	_, err = svc.Process(context.Background(), ClipInput{AssetID: "a", LocalPath: "/clip.mp4", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 {
		t.Fatal("Whisper reran for an existing transcript")
	}
}
