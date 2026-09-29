// Package youtube — register_fanout_test.go pins the September 2026
// register-path gap closure: a clip registered through
// POST /api/media/register-batch must schedule the canonical post-commit
// multilingual fan-out, exactly like the per-segment extraction path does.
//
// Contract under test: when the fan-out port is wired, Register
//
//  1. hands it EXACTLY ONCE the clip id + language + transcript the atomic
//     super-tx persisted (so the materializer's source-hash contract holds),
//  2. does so strictly AFTER the commit (scheduling work for an unpersisted
//     asset is the failure mode the atomic super-tx exists to prevent), and
//  3. keeps working when the port is NOT wired (fixture/minimal composition
//     sites behave exactly as before the seam existed).
//
// A failure here means the gap is back: every register-batch clip would land
// with a single Whisper transcript, no translations, no `.ass` artifacts and
// no multilingual search — silently, with no error anywhere.
package youtube

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// committedHandoff records one EnqueueCommittedClip call together with the
// number of atomic commits that had happened when it was made — that is what
// proves the scheduling happens strictly after the durable write.
type committedHandoff struct {
	clipID  string
	lang    string
	text    string
	commits int
}

type recordingFanOut struct {
	writer   *recordingAtomicWriter
	handoffs []committedHandoff
}

func (f *recordingFanOut) EnqueueCommittedClip(_ context.Context, clipID, sourceLanguage, plainText string) {
	commits := 0
	if f.writer != nil {
		commits = f.writer.calls
	}
	f.handoffs = append(f.handoffs, committedHandoff{
		clipID:  clipID,
		lang:    sourceLanguage,
		text:    plainText,
		commits: commits,
	})
}

// compile-time assertion: the recording fake satisfies the port the Register
// pipeline consumes (the production concrete *texttracks.MaterializeFanOut
// satisfies it structurally too).
var _ MaterializeFanOutPort = (*recordingFanOut)(nil)

// TestRegister_SchedulesMaterializeFanOutAfterCommit is the primary
// acceptance test for the gap closure: one hand-off, carrying exactly what
// was committed, after the commit.
func TestRegister_SchedulesMaterializeFanOutAfterCommit(t *testing.T) {
	writer := &recordingAtomicWriter{}
	svc, _, _ := atomicTestService(t, writer)
	fanOut := &recordingFanOut{writer: writer}
	svc = svc.WithMaterializeFanOut(fanOut)

	res, err := svc.Register(context.Background(), sourcing.RegisterClipCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name:     "Fanout Clip",
		Category: "documentary",
	})
	if err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatal("expected OK=true")
	}

	if len(fanOut.handoffs) != 1 {
		t.Fatalf("expected exactly 1 fan-out hand-off after the commit, got %d (0 = the register path ships monolingual clips again; >1 = duplicate materialize jobs)", len(fanOut.handoffs))
	}
	got := fanOut.handoffs[0]

	if writer.calls != 1 {
		t.Fatalf("expected 1 atomic commit, got %d", writer.calls)
	}
	track := writer.last.TextTracks[0]
	if got.clipID != track.AssetID || got.clipID != res.ClipID {
		t.Errorf("clip id drift: fan-out got %q, committed track %q, result %q", got.clipID, track.AssetID, res.ClipID)
	}
	if got.lang != track.LanguageCode {
		t.Errorf("language drift: fan-out got %q, committed track %q (the materializer would fail its source-hash contract)", got.lang, track.LanguageCode)
	}
	if got.text != track.TextContent {
		t.Errorf("transcript drift: fan-out got %q, committed track %q", got.text, track.TextContent)
	}
	if got.commits != 1 {
		t.Errorf("fan-out must be scheduled AFTER the durable commit: it ran with %d commits already visible", got.commits)
	}
}

// TestRegister_OutboxTerminalConflictStillSchedulesFanOut pins the BLOCKER #4
// branch: the writer returns ErrOutboxTerminalConflict only when the asset +
// transcript ARE committed and just the index event was suppressed — so the
// multilingual fan-out is still owed, even though Register surfaces an error.
//
// This is the branch a forced re-registration of an already-published window
// takes in production, and it was found by a live run: without the hand-off
// the clip keeps its single source transcript forever.
func TestRegister_OutboxTerminalConflictStillSchedulesFanOut(t *testing.T) {
	writer := &recordingAtomicWriter{err: youtubeports.ErrOutboxTerminalConflict}
	svc, _, _ := atomicTestService(t, writer)
	fanOut := &recordingFanOut{writer: writer}
	svc = svc.WithMaterializeFanOut(fanOut)

	_, err := svc.Register(context.Background(), sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Blocked Index Clip",
	})
	if err == nil {
		t.Fatal("BLOCKER #4 must still surface to the caller (the job reports it), got nil error")
	}
	if !errors.Is(err, youtubeports.ErrOutboxTerminalConflict) {
		t.Fatalf("error must keep the typed sentinel for the caller to classify, got %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("expected the durable commit to have happened exactly once, got %d", writer.calls)
	}
	if len(fanOut.handoffs) != 1 {
		t.Fatalf("the fan-out is still owed when only the index event was suppressed: got %d hand-offs, want 1", len(fanOut.handoffs))
	}

	track := writer.last.TextTracks[0]
	got := fanOut.handoffs[0]
	if got.clipID != track.AssetID || got.lang != track.LanguageCode || got.text != track.TextContent {
		t.Errorf("hand-off drifted from the committed transcript: got (%q,%q,%d bytes), committed (%q,%q,%d bytes)",
			got.clipID, got.lang, len(got.text), track.AssetID, track.LanguageCode, len(track.TextContent))
	}
	if got.commits != 1 {
		t.Errorf("the fan-out must be scheduled AFTER the durable commit, ran with %d commits visible", got.commits)
	}
}

// TestRegister_WithoutFanOutPortIsANoOp pins the back-compatible posture: a
// composition that does not wire the seam registers clips exactly as before
// (no panic, no behaviour change beyond the missing translations).
func TestRegister_WithoutFanOutPortIsANoOp(t *testing.T) {
	writer := &recordingAtomicWriter{}
	svc, _, _ := atomicTestService(t, writer)

	res, err := svc.Register(context.Background(), sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "No Fanout Clip",
	})
	if err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatal("expected OK=true")
	}
	if writer.calls != 1 {
		t.Fatalf("expected 1 atomic commit, got %d", writer.calls)
	}
}
