// Package voiceover — usecase/process_segment_execution_test.go
//
// Execution-phase tests for the SHARED per-item pipeline runner
// (usecase/process_segment.go). These tests pin the runtime
// contracts — destination resolution, per-stage success and
// failure propagation, metadata injection — WITHOUT exercising
// idempotency or transactional outbox paths (those live in the
// idempotency and e2e files respectively).
//
// godlike/06 SSOT (one canonical owner per fact): each test owns
// exactly one capability concern at the usecase boundary:
//
//  3. TestResolveDestinationWithFallback — 3-rule precedence
//     contract for the SHARED destination resolver
//     (destination_helpers.go, P0 #5 DRY pair).
//
//  4. TestProcessSegmentUseCase_Execute_SuccessFull4Stages —
//     canonical 4-stage happy-path pin (TTS → Publish → Finalize).
//
//  5. TestProcessSegmentUseCase_Execute_StyleGroupMetadataInjection
//     — metaBuf["style_group"] injection block (reviewer-fix #3).
//
//  6. TestProcessSegmentUseCase_Execute_Stage0MissingFolderGuard —
//     pre-TTS Stage 0 short-circuit (P0.1 Fase 1b contract).
//
//  7. TestProcessSegmentUseCase_Execute_Stage1_TTS_GeneratesNonEmptyOutput
//     — FASE 2 contract #1 (TTS success).
//
//  8. TestProcessSegmentUseCase_Execute_Stage1_TTS_Fails_PropagatesError
//     — FASE 2 contract #2 (TTS failure).
//
//  9. TestProcessSegmentUseCase_Execute_Stage3_Publisher_ForwardsLanguageAndProject
//     — FASE 2 contract #3 (Publisher Language + Project).
//
//  10. TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyLanguage_PropagatesSentinel
//     — FASE 2 contract #4 (Publisher empty Language sentinel).
//
// godlike/07 minimum-blast-radius: zero production code changes.
// Inline stubFailingPublisher (used only by Test 10) is kept in this
// file rather than the helpers file because it has a single-test
// blast radius — moving it to the shared helpers surface would
// amplify the public-of-shared-stubs surface for one test, which
// is the wrong side of the godlike/07 / SSOT tradeoff.
package voiceover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"go.uber.org/zap"
)

func TestProcessSegmentUseCase_Execute_Stage3_Publisher_ForwardsLanguageAndProject(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/stage3-lang-proj.mp3",
			Voice:         "it-IT-ElsaNeural",
			LegacyFileMD5: "hash-stage3-lp-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-stage3-lang-proj"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-stage3-lang-proj", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-stage3"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-stage3"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-stage3-lang-proj",
		Language: "it-IT",
		Text:     "Testo per la verifica della propagazione semantica.",
		Voice:    "it-IT-ElsaNeural",
		Filename: "stage3-lang-proj.mp3",
		Project:  "storia-boxe",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, out.Status)

	// FASE 2 contract #3 assertion: Language + Project MUST be
	// forwarded to the Publisher's VoiceoverPublishCommand.
	require.Len(t, pub.published, 1,
		"Publisher.Publish must be invoked exactly once")
	got := pub.published[0]
	assert.Equal(t, "it-IT", got.Language,
		"PR-VO-LANGUAGE-PROJECT-PROPAGATION: cmd.Language MUST be forwarded as VoiceoverPublishCommand.Language")
	assert.Equal(t, "storia-boxe", got.Project,
		"PR-VO-LANGUAGE-PROJECT-PROPAGATION: cmd.Project MUST be forwarded as VoiceoverPublishCommand.Project")
	assert.Equal(t, "vo-stage3-lang-proj", got.ID,
		"VoiceoverPublishCommand.ID must equal cmd.ID")
	assert.Equal(t, "/tmp/vo/stage3-lang-proj.mp3", got.LocalPath,
		"VoiceoverPublishCommand.LocalPath must be the TTS output path")
	assert.Equal(t, "stage3-lang-proj.mp3", got.Filename,
		"VoiceoverPublishCommand.Filename must equal cmd.Filename")
	assert.Equal(t, "dest-stage3", got.FolderID,
		"VoiceoverPublishCommand.FolderID must equal resolvedDest.FolderID")
}

// TestProcessSegmentUseCase_Execute_DestinationParentMismatchFailsClosed
// pins the hard post-upload integrity gate. A resolved destination mismatch
// is not a retryable generic upload failure and must not reach finalization.
func TestProcessSegmentUseCase_Execute_DestinationParentMismatchFailsClosed(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/vo/mismatch.mp3"}}
	pub := &stubFailingPublisher{err: delivery.ErrDestinationParentMismatch}
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "must-not-finalize"}}
	resolved := &ResolvedDestination{FolderID: "resolved-folder", FolderPath: "/tmp/vo"}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})
	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID: "vo-parent-mismatch", Language: "en", Text: "integrity gate",
		Filename: "mismatch.mp3", Dest: resolved,
	})

	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Equal(t, VoiceoverDestinationMismatchCode, out.ErrorCode)
	assert.Contains(t, out.Error, VoiceoverDestinationMismatchCode)
	assert.Empty(t, finalizer.calls, "destination mismatch must fail before DB finalization")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 10: Stage 3 Publisher — empty Language propagates typed sentinel
// ─────────────────────────────────────────────────────────────────────────

// stubFailingPublisher is a VoiceoverPublisher that returns a
// pre-configured error. Used by the Stage 3 empty-Language contract
// test to simulate the adapter's fail-closed semantics without
// duplicating the adapter logic at the use-case test layer.
//
// godlike/07 single-test blast-radius: this stub is defined inline
// (rather than promoted to process_segment_test_helpers_test.go) because
// it has exactly one consumer (Test 10). Promoting it to the shared
// helpers surface would inflate the helpers' surface that
// FASE-bucketed test files must CD-import, breaking SSOT for a stub
// with one test of blast-radius. Whitespace-tolerant note: production
// has no concept of `stubFailingPublisher` — this stub is hermetic.
type stubFailingPublisher struct {
	err       error
	published []VoiceoverPublishCommand
}

func (s *stubFailingPublisher) Publish(_ context.Context, cmd VoiceoverPublishCommand) (string, error) {
	s.published = append(s.published, cmd)
	return "", s.err
}

var _ VoiceoverPublisher = (*stubFailingPublisher)(nil)

func TestProcessSegmentUseCase_Execute_MetadataSerializationFailureStopsBeforePublish(t *testing.T) {
	db := openProcessTestDB(t)
	pub := &stubProcessPublisher{fileID: "must-not-publish"}
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "must-not-finalize"}}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/metadata-failure.mp3"}},
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-metadata-failure",
		Language: "en",
		Text:     "metadata serialization failure",
		Filename: "metadata-failure.mp3",
		Metadata: map[string]any{"unsupported": make(chan int)},
		Dest:     &ResolvedDestination{FolderID: "folder", FolderPath: "/tmp"},
	})

	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Equal(t, FailureMetadataSerialization, FailureCode(out.ErrorCode))
	var pipelineErr *PipelineError
	require.ErrorAs(t, err, &pipelineErr)
	assert.Equal(t, StageMetadata, pipelineErr.Stage)
	assert.False(t, pipelineErr.Retryable)
	assert.Empty(t, pub.published, "metadata serialization must fail before Drive publish")
	assert.Empty(t, finalizer.calls, "metadata serialization must fail before DB finalization")
}

// TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyLanguage_PropagatesSentinel
// pins the FASE 2 contract #4: when the Publisher returns
// ErrVoiceoverPublishLanguageRequired (the adapter's fail-closed
// sentinel for empty Language), the use case MUST propagate the error
// and surface a StatusFailed result. The test uses a stub that returns
// the sentinel unconditionally — it does NOT replicate the adapter's
// empty-Language check (that contract lives at
// adapters_voiceover_publisher_test.go). The use-case contract is:
// "if the Publisher fails, surface StatusFailed + upload_failed prefix".
func TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyLanguage_PropagatesSentinel(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/stage3-no-lang.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-stage3-nolang",
		},
	}
	pub := &stubFailingPublisher{
		err: fmt.Errorf(
			"useCasePublisherAdapter.Publish: empty Language (voiceover publish requires Language for canonical semantic routing per PR-P12-VOICEOVER-SEMANTIC-FIELDS): %w",
			ErrVoiceoverPublishLanguageRequired,
		),
	}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "should-never-be-used"},
	}

	dest := &stubProcessDestResolver{folderID: "dest-stage3-nolang"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-stage3-nolang"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-stage3-no-lang",
		Language: "", // empty — adapter would reject; stub simulates this
		Text:     "Text without a language",
		Filename: "stage3-no-lang.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.Error(t, err,
		"FASE 2 contract #4: Publisher failure MUST return error")
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status,
		"Publisher failure MUST set Status=StatusFailed")
	assert.Contains(t, out.Error, "upload_failed:",
		"error prefix must be 'upload_failed:' (canonical per-item surface)")

	// godlike/07 typed-error contract: the sentinel is errors.Is-probable
	// through the %w chain preserved by fmt.Errorf in the stub.
	assert.True(t, errors.Is(err, ErrVoiceoverPublishLanguageRequired),
		"errors.Is must recover ErrVoiceoverPublishLanguageRequired through the dual-%%w chain")

	// Publisher MUST have been invoked (the stub records the call
	// even when returning an error — the use case reaches Stage 3,
	// the adapter validates Language, the error surfaces).
	require.Len(t, pub.published, 1,
		"Publisher.Publish must be invoked (the use case reaches Stage 3 before the adapter fails)")
	assert.Equal(t, "", pub.published[0].Language,
		"VoiceoverPublishCommand.Language must be empty (the use case forwards cmd.Language verbatim)")

	// Finalizer MUST NOT be invoked after Publisher failure.
	assert.Len(t, finalizer.calls, 0,
		"Finalizer.Finalize MUST NOT be invoked after Publisher failure (short-circuit at Stage 3)")
}

// TestProcessSegmentUseCase_ForwardsTimingPolicyToTTS pins the timing
// policy pass-through: the per-segment command's Timing must reach the
// TTS provider input verbatim (the provider only returns raw boundaries;
// the canonical artifact is built later from the final audio). The stub
// returns REAL word boundaries + a real audio file so the required
// policy reaches a completed timing bundle (fail-closed contract: a
// required policy with no boundaries would fail the segment).
func TestProcessSegmentUseCase_ForwardsTimingPolicyToTTS(t *testing.T) {
	db := openProcessTestDB(t)
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "timing-forward.mp3")
	require.NoError(t, os.WriteFile(audioPath, []byte("fake-mp3-bytes"), 0o644))

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     audioPath,
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "timing-forward-hash",
			Provider:      "edge_tts",
			BoundaryMode:  audio.BoundaryWord,
			Duration:      2500 * time.Millisecond,
			WordBoundaries: []RawSpeechBoundary{
				{Text: "Text", StartUS: 0, EndUS: 200_000},
				{Text: "with", StartUS: 200_000, EndUS: 600_000},
				{Text: "timing", StartUS: 600_000, EndUS: 900_000},
			},
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-timing-forward"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-timing-forward", Reused: false},
	}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	resolvedDest := &ResolvedDestination{FolderID: "dest-timing-forward", FolderPath: dir}

	timing := &audio.TimingRequest{
		Mode:         audio.TimingRequired,
		BoundaryMode: audio.BoundaryWord,
		Formats:      []audio.TimingFormat{audio.TimingJSON, audio.TimingSRT, audio.TimingVTT},
	}

	cmd := &ProcessSegmentCommand{
		ID:       "vo-timing-forward",
		Text:     "Text with an explicit timing policy",
		Language: "en",
		Voice:    "en-US-RogerNeural",
		Filename: "timing-forward.mp3",
		Timing:   timing,
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusCompleted, out.Status)

	require.Len(t, tts.synthesized, 1)
	require.NotNil(t, tts.synthesized[0].Timing, "TTSInput.Timing must not be nil when cmd.Timing is set")
	assert.Equal(t, audio.TimingRequired, tts.synthesized[0].Timing.Mode)
	assert.Equal(t, audio.BoundaryWord, tts.synthesized[0].Timing.BoundaryMode)
	assert.Len(t, tts.synthesized[0].Timing.Formats, 3)

	// The required timing policy must reach a completed bundle: all
	// three projections published, real links, and hashes populated.
	require.NotNil(t, out.Timing, "required timing must produce a timing result")
	assert.Equal(t, TimingStatusCompleted, out.Timing.Status)
	assert.NotEmpty(t, out.Timing.JSONLink)
	assert.NotEmpty(t, out.Timing.SRTLink)
	assert.NotEmpty(t, out.Timing.VTTLink)
	assert.Equal(t, 3, out.Timing.WordCount)
	assert.NotEmpty(t, out.Timing.TextSHA256)
	assert.NotEmpty(t, out.Timing.AudioSHA256)
	// Audio publish + 3 timing projections = 4 Publisher calls. The audio
	// is always published first; the three projection formats are
	// published in a non-deterministic order, so assert the set rather
	// than a positional slice.
	require.Len(t, pub.published, 4, "audio + json + srt + vtt must all be published")
	assert.Equal(t, cmd.Filename, pub.published[0].Filename)
	require.ElementsMatch(t,
		[]string{"timing-forward-timing.json", "timing-forward.srt", "timing-forward.vtt"},
		[]string{pub.published[1].Filename, pub.published[2].Filename, pub.published[3].Filename},
		"the three timing projections must all be published (order is not part of the contract)")

	// nil cmd.Timing stays nil on the input (defaults applied by the
	// provider); the default best-effort policy still completes when the
	// provider returns boundaries.
	cmdNil := &ProcessSegmentCommand{
		ID:       "vo-timing-forward-nil",
		Text:     "Text without an explicit timing policy",
		Language: "en",
		Filename: "timing-forward-nil.mp3",
		Dest:     resolvedDest,
	}
	outNil, errNil := uc.Execute(context.Background(), cmdNil)
	require.NoError(t, errNil)
	require.NotNil(t, outNil)
	require.Len(t, tts.synthesized, 2)
	assert.Nil(t, tts.synthesized[1].Timing, "TTSInput.Timing must stay nil when cmd.Timing is nil")
	require.NotNil(t, outNil.Timing, "best-effort default with boundaries must still produce a timing bundle")
	assert.Equal(t, TimingStatusCompleted, outNil.Timing.Status)
}
