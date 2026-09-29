// Package texttracks — fanout_commit_test.go: hermetic probes for the
// canonical post-COMMIT mapping (EnqueueCommittedClip).
//
// WHY THESE PROBES EXIST: the mapping used to be inlined in the single
// producer that had it (the YouTube per-segment extraction pipeline), so the
// Register commit route shipped without it and produced clips with one
// transcript, zero translations and zero `.ass` artifacts (September 2026).
// The decision now lives here, so the rules below are what EVERY commit route
// is guaranteed to get — one implementation, one suite.
package texttracks

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// compile-time pin: the production concrete satisfies the seam both commit
// routes consume (structurally — neither capability imports this one just to
// declare its port).
var _ CommittedClipEnqueuer = (*MaterializeFanOut)(nil)

// materializePayload asserts the single recorded job is a materialize request
// for the given (asset, language, hash) tuple and returns its payload.
func materializePayload(t *testing.T, stub *stubEnqueuer, assetID, wantLang, wantHash string) MaterializeJobPayload {
	t.Helper()
	if stub.calls != 1 {
		t.Fatalf("expected 1 Enqueue call, got %d", stub.calls)
	}
	last := stub.last()
	if last.Type != job.TypeAssetTextMaterialize {
		t.Fatalf("Type = %q, want %q", last.Type, job.TypeAssetTextMaterialize)
	}
	wantActiveKey := "asset.text.materialize:" + assetID + ":" + wantHash
	if last.ActiveKey != wantActiveKey {
		t.Fatalf("ActiveKey = %q, want %q", last.ActiveKey, wantActiveKey)
	}
	ps, ok := last.Payload.(MaterializeJobPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want MaterializeJobPayload", last.Payload)
	}
	if ps.AssetID != assetID {
		t.Fatalf("AssetID = %q, want %q", ps.AssetID, assetID)
	}
	if ps.SourceLanguage != wantLang {
		t.Fatalf("SourceLanguage = %q, want %q", ps.SourceLanguage, wantLang)
	}
	if ps.SourceTextHash != wantHash {
		t.Fatalf("SourceTextHash = %q, want %q (the materializer re-reads the READY row and fails closed on a mismatch)", ps.SourceTextHash, wantHash)
	}
	if len(ps.TextKinds) != 1 || ps.TextKinds[0] != string(detail.TextTrackTranscript) {
		t.Fatalf("TextKinds = %v, want [%q]", ps.TextKinds, detail.TextTrackTranscript)
	}
	return ps
}

// TestEnqueueCommittedClip_MaterializesWithCanonicalHash pins the happy path:
// a committed transcript schedules ONE materialize job whose source hash is
// exactly detail.TextHash(text, lang, transcript) — the value the persistence
// layer writes onto the READY track, so the materializer's contract check
// accepts it.
func TestEnqueueCommittedClip_MaterializesWithCanonicalHash(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil)

	const (
		clipID = "yt_9Q6T-bzF4Vs_122_151_v1"
		lang   = "en"
		text   = "the canonical transcript"
	)
	f.EnqueueCommittedClip(context.Background(), clipID, lang, text)

	wantHash := string(detail.TextHash(text, lang, detail.TextTrackTranscript))
	materializePayload(t, stub, clipID, lang, wantHash)
}

// TestEnqueueCommittedClip_EmptyTranscriptSchedulesAcquireWithDefaultLanguage
// pins the no-transcript branch: the ACQUIRE chain runs (its own active key,
// no source hash) using the configured default source language, so a clip
// committed without text is repaired instead of silently staying monolingual.
func TestEnqueueCommittedClip_EmptyTranscriptSchedulesAcquireWithDefaultLanguage(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil)
	f.SetDefaultSourceLanguage("en")

	const clipID = "yt_no_transcript_0_10_v1"
	f.EnqueueCommittedClip(context.Background(), clipID, "", "")

	if stub.calls != 1 {
		t.Fatalf("expected 1 Enqueue call, got %d", stub.calls)
	}
	last := stub.last()
	wantActiveKey := "asset.text.acquire:" + clipID + ":en"
	if last.ActiveKey != wantActiveKey {
		t.Fatalf("ActiveKey = %q, want %q (the acquire path must keep its own key so a repair can start)", last.ActiveKey, wantActiveKey)
	}
	ps, ok := last.Payload.(MaterializeJobPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want MaterializeJobPayload", last.Payload)
	}
	if ps.SourceLanguage != "en" {
		t.Fatalf("SourceLanguage = %q, want the configured default %q", ps.SourceLanguage, "en")
	}
	if ps.SourceTextHash != "" {
		t.Fatalf("SourceTextHash = %q, want empty (there is no committed text to hash)", ps.SourceTextHash)
	}
}

// TestEnqueueCommittedClip_UnknownLanguageFallsBackToUnd pins the language
// fallback: a transcript persisted under "und" must be enqueued under the
// SAME code, or the materializer's hash check would reject the job the
// persistence layer just made valid.
func TestEnqueueCommittedClip_UnknownLanguageFallsBackToUnd(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil)

	const (
		clipID = "yt_unknown_lang_0_10_v1"
		text   = "unidentified language transcript"
	)
	f.EnqueueCommittedClip(context.Background(), clipID, "", text)

	wantHash := string(detail.TextHash(text, "und", detail.TextTrackTranscript))
	materializePayload(t, stub, clipID, "und", wantHash)
}

// TestEnqueueCommittedClip_NoDefaultLanguageSkipsAcquire pins the fail-closed
// posture of the no-transcript branch: with no resolvable source language and
// no configured default there is nothing to acquire against, so nothing is
// enqueued (an invalid acquire job would only dead-letter).
func TestEnqueueCommittedClip_NoDefaultLanguageSkipsAcquire(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil)

	f.EnqueueCommittedClip(context.Background(), "yt_no_lang_0_10_v1", "", "")

	if stub.calls != 0 {
		t.Fatalf("expected no Enqueue call when no source language is resolvable, got %d", stub.calls)
	}
}

// TestEnqueueCommittedClip_NilReceiverAndEmptyClipIDAreNoOps pins the
// defensive posture every producer relies on: an unwired seam or a missing
// identity is a silent no-op, never a panic (composition sites without a
// broker must behave exactly as before the seam existed).
func TestEnqueueCommittedClip_NilReceiverAndEmptyClipIDAreNoOps(t *testing.T) {
	stub := &stubEnqueuer{}

	var nilFanOut *MaterializeFanOut
	nilFanOut.EnqueueCommittedClip(context.Background(), "yt_x_0_10_v1", "en", "text")

	f := NewMaterializeFanOut(stub, nil)
	f.EnqueueCommittedClip(context.Background(), "", "en", "text")

	if stub.calls != 0 {
		t.Fatalf("expected no Enqueue call for nil receiver / empty clip id, got %d", stub.calls)
	}
}

// TestEnqueueCommittedClip_BrokerErrorIsLoggedNotPropagated pins the void
// contract: the clip is already durably committed, so a broker failure must
// not become a failed extraction or a failed registration — it is swallowed
// after logging and stays recoverable via the operator backfill CLI.
func TestEnqueueCommittedClip_BrokerErrorIsLoggedNotPropagated(t *testing.T) {
	stub := &stubEnqueuer{hookErr: errors.New("broker unavailable")}
	f := NewMaterializeFanOut(stub, nil)
	f.SetDefaultSourceLanguage("en")

	// No return value to propagate; the call must simply not panic.
	f.EnqueueCommittedClip(context.Background(), "yt_broker_down_0_10_v1", "en", "text")
	f.EnqueueCommittedClip(context.Background(), "yt_broker_down_10_20_v1", "", "")

	if stub.calls != 2 {
		t.Fatalf("expected both enqueue attempts to reach the broker, got %d", stub.calls)
	}
}
