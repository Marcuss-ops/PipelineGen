package enrichment

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"testing"
)

type clipTracks struct {
	ready  *detail.TextTrack
	cues   []detail.TimedCue
	writes [][]detail.TextTrack
}

func (r *clipTracks) FindReady(context.Context, string, string, detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	return r.ready, r.cues, nil
}
func (r *clipTracks) UpsertBatch(_ context.Context, t []detail.TextTrack) error {
	r.writes = append(r.writes, t)
	return nil
}

type clipWhisper struct{ calls int }

func (w *clipWhisper) TranscribeAudioWithDetection(context.Context, string) (detail.TranscriptResult, error) {
	w.calls++
	return detail.TranscriptResult{Text: "hello", DetectedLanguage: "en", Cues: []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hello"}}}, nil
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
	tr.ready = &detail.TextTrack{AssetID: "a", LanguageCode: "en", TextKind: detail.TextTrackTranscript, TextContent: "existing", Status: detail.TextTrackReady}
	tr.cues = []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "existing"}}
	_, err = svc.Process(context.Background(), ClipInput{AssetID: "a", LocalPath: "/clip.mp4", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 {
		t.Fatal("Whisper reran for an existing transcript")
	}
}
