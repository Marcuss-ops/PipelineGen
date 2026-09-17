// Package youtube — register_atomic_test.go pins the 2026-09-17
// identity/atomicity unification of the YouTube registration path.
//
// Contract under test: when the canonical atomic writer is wired, Register
//
//  1. calls CommitClipTextAndIndexEvent EXACTLY ONCE (asset + text tracks +
//     cue segments + outbox index event as one transaction), and
//  2. does NOT call the legacy split pair (IndexDispatcherPort.EnqueueAndIndex
//     and TextTrackRepository.UpsertBatch), because doing both would emit a
//     second index event for the same clip, and
//  3. hands the writer the SAME deterministic window-derived identity that the
//     extraction path mints (`yt_<videoID>_<start>_<end>_<policy>`), with the
//     transcript in the SAME command — so the index event can never become
//     visible before the transcript.
//
// A failure here means the "asset indexed before its transcript exists" window
// has been reopened, or the two ingest routes disagree on the clip's primary
// key.
package youtube

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// recordingAtomicWriter captures the single super-transaction command.
type recordingAtomicWriter struct {
	calls int
	last  localized.CommitLocalizedClipCommand
	err   error
}

func (w *recordingAtomicWriter) CommitClipTextAndIndexEvent(_ context.Context, cmd localized.CommitLocalizedClipCommand) error {
	w.calls++
	w.last = cmd
	return w.err
}

// poisonIndexDispatcher fails the assertion if the legacy split write runs
// while the atomic writer is wired.
type poisonIndexDispatcher struct {
	called bool
}

func (p *poisonIndexDispatcher) EnqueueAndIndex(_ context.Context, _ *sourcing.ExistingClip, _ string) error {
	p.called = true
	return nil
}

// poisonTextTrackRepo overrides UpsertBatch to record the legacy split call
// while inheriting the remaining read surface from the shared stub.
type poisonTextTrackRepo struct {
	stubTextTrackRepo
	called bool
}

func (p *poisonTextTrackRepo) UpsertBatch(_ context.Context, _ []detail.TextTrack) error {
	p.called = true
	return nil
}

// atomicTestService builds a Service through the canonical constructor so the
// ServiceDeps plumbing of AtomicWriter is exercised too.
func atomicTestService(t *testing.T, writer AtomicClipWriterPort) (*Service, *poisonIndexDispatcher, *poisonTextTrackRepo) {
	t.Helper()
	idx := &poisonIndexDispatcher{}
	tracks := &poisonTextTrackRepo{}
	svc := NewService(ServiceDeps{
		Fetcher:       &stubFetcher{},
		Publisher:     &stubPublisher{},
		Transcriber:   &stubTranscriber{},
		IndexDisp:     idx,
		Enrichment:    &stubEnrichment{indexingEnabled: false},
		Log:           &stubLogger{},
		TextTrackRepo: tracks,
	}).WithAtomicClipWriter(writer)
	return svc, idx, tracks
}

// TestRegister_AtomicWriter_CommitsAssetAndTranscriptInOneCall is the primary
// acceptance test for the unification: one transaction, one identity, one
// transcript.
func TestRegister_AtomicWriter_CommitsAssetAndTranscriptInOneCall(t *testing.T) {
	writer := &recordingAtomicWriter{}
	svc, idx, tracks := atomicTestService(t, writer)

	res, err := svc.Register(context.Background(), sourcing.RegisterClipCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name:     "Atomic Clip",
		Category: "Boxe",
		Tags:     []string{"dolly", "interview"},
	})
	if err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatal("expected OK=true")
	}

	if writer.calls != 1 {
		t.Fatalf("expected exactly 1 atomic commit, got %d (a second commit means two index events for one clip)", writer.calls)
	}
	if idx.called {
		t.Error("legacy IndexDispatcherPort.EnqueueAndIndex MUST NOT run when the atomic writer is wired (it would emit a second index event)")
	}
	if tracks.called {
		t.Error("legacy TextTrackRepository.UpsertBatch MUST NOT run when the atomic writer is wired (the transcript belongs in the atomic command)")
	}

	cmd := writer.last
	if cmd.Clip.ID != res.ClipID {
		t.Errorf("clip identity drift: writer got %q, result reported %q", cmd.Clip.ID, res.ClipID)
	}
	// Window-derived identity, same format the extraction path mints.
	wantID := "yt_dQw4w9WgXcQ_0_0_v1"
	if cmd.Clip.ID != wantID {
		t.Errorf("expected canonical window-derived clip id %q, got %q", wantID, cmd.Clip.ID)
	}
	if cmd.Clip.Metadata.AssetID != cmd.Clip.ID || cmd.Clip.Metadata.ClipID != cmd.Clip.ID {
		t.Errorf("metadata identity must match the asset id (got asset=%q clip=%q)", cmd.Clip.Metadata.AssetID, cmd.Clip.Metadata.ClipID)
	}
	if cmd.Clip.Metadata.SourceProvider != "youtube" || cmd.Clip.Metadata.VideoID != "dQw4w9WgXcQ" {
		t.Errorf("source provenance missing: provider=%q video_id=%q", cmd.Clip.Metadata.SourceProvider, cmd.Clip.Metadata.VideoID)
	}

	// The transcript MUST travel IN the same command as the index event.
	if len(cmd.TextTracks) != 1 {
		t.Fatalf("expected exactly 1 text track in the atomic command, got %d", len(cmd.TextTracks))
	}
	track := cmd.TextTracks[0]
	if track.TextKind != detail.TextTrackTranscript {
		t.Errorf("expected a transcript track, got kind=%q", track.TextKind)
	}
	if track.TextContent != "test transcript text" {
		t.Errorf("transcript content not carried into the atomic write: %q", track.TextContent)
	}
	if track.AssetID != cmd.Clip.ID {
		t.Errorf("transcript must be keyed to the same asset id: track=%q asset=%q", track.AssetID, cmd.Clip.ID)
	}
	if !track.IsCurrent || track.Status != detail.TextTrackReady {
		t.Errorf("transcript must land as the CURRENT READY track (is_current=%v status=%q)", track.IsCurrent, track.Status)
	}

	// The index event must name the same aggregate; that is what makes the
	// event and the transcript visible in the same commit.
	if cmd.IndexEvent.AggregateID != cmd.Clip.ID {
		t.Errorf("index event aggregate drift: event=%q asset=%q", cmd.IndexEvent.AggregateID, cmd.Clip.ID)
	}
	if cmd.IndexEvent.CreatedAt.IsZero() {
		t.Error("index event must carry a CreatedAt")
	}
	if cmd.Clip.LocalPath == "" {
		t.Error("clip must carry the local path of the cut bytes")
	}
	if cmd.Clip.PolicyVersion != detail.DefaultYouTubeClipPolicyVersion {
		t.Errorf("expected policy version %q, got %q", detail.DefaultYouTubeClipPolicyVersion, cmd.Clip.PolicyVersion)
	}
}

// TestRegister_AtomicWriter_ErrorFailsClosed proves the atomic path does not
// silently degrade into the legacy split write when the transaction fails: the
// caller sees the error and NOTHING is written twice.
func TestRegister_AtomicWriter_ErrorFailsClosed(t *testing.T) {
	writer := &recordingAtomicWriter{err: errors.New("pg: connection reset")}
	svc, idx, tracks := atomicTestService(t, writer)

	_, err := svc.Register(context.Background(), sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Atomic Clip",
	})
	if err == nil {
		t.Fatal("expected an error when the atomic commit fails, got nil")
	}
	if writer.calls != 1 {
		t.Errorf("expected 1 attempted atomic commit, got %d", writer.calls)
	}
	if idx.called || tracks.called {
		t.Error("a failed atomic commit MUST NOT fall back to the legacy split write (that would be a second, unguarded write)")
	}
}

// TestRegister_AtomicWriter_IdempotentReplaySameIdentity proves a replay of the
// same request mints the SAME identity, so the transactional UPSERT updates one
// row instead of inserting a second asset for the same clip window.
func TestRegister_AtomicWriter_IdempotentReplaySameIdentity(t *testing.T) {
	writer := &recordingAtomicWriter{}
	svc, _, _ := atomicTestService(t, writer)

	cmd := sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Atomic Clip",
	}
	first, err := svc.Register(context.Background(), cmd)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	firstID := writer.last.Clip.ID

	second, err := svc.Register(context.Background(), cmd)
	if err != nil {
		t.Fatalf("replay Register: %v", err)
	}

	if writer.calls != 2 {
		t.Fatalf("expected 2 commits (both replays), got %d", writer.calls)
	}
	if writer.last.Clip.ID != firstID {
		t.Errorf("replay minted a different identity: first=%q second=%q (this is the duplicate-asset failure mode)", firstID, writer.last.Clip.ID)
	}
	if first.ClipID != second.ClipID {
		t.Errorf("reported clip ids differ across replays: %q vs %q", first.ClipID, second.ClipID)
	}
}

// TestRegister_AtomicWriter_SeparatesIdentityFromContentHash pins the identity
// rule the whole unification rests on: the window decides WHICH asset this is,
// the file bytes only decide WHETHER the indexed snapshot is stale. The
// extraction path asserts the same rule from the other side (two byte digests,
// one identity), so this test observes it through the register route: the
// content hash travels on the command but never inside the asset id.
func TestRegister_AtomicWriter_ContentHashIsNotIdentity(t *testing.T) {
	writer := &recordingAtomicWriter{}
	svc, _, _ := atomicTestService(t, writer)

	if _, err := svc.Register(context.Background(), sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Atomic Clip",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cmd := writer.last
	if cmd.Clip.ID != "yt_dQw4w9WgXcQ_0_0_v1" {
		t.Errorf("asset id must be the window-derived identity, got %q", cmd.Clip.ID)
	}
	// LegacyFileMD5 is the CONTENT fingerprint: it may be empty when the bytes
	// cannot be hashed, and it must never leak into the identity.
	if len(cmd.Clip.ID) > 0 && cmd.Clip.ID == cmd.Clip.LegacyFileMD5 {
		t.Error("the content hash must never be the asset identity")
	}
}
