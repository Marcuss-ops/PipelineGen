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
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 3: ResolveDestinationWithFallback 3-rule precedence
// ─────────────────────────────────────────────────────────────────────────

// TestResolveDestinationWithFallback is a table-driven test pinning
// the 3-rule precedence contract for the SHARED destination resolver
// (destination_helpers.go):
//
//  1. destReq != nil → destResolver.Resolve(ctx, destReq).
//     Caller's explicit destination always wins; defaultResolver is
//     NOT consulted even if destReq would have failed.
//  2. destReq == nil AND defaultResolver != nil AND Resolve returns
//     ok=true AND folderID != "" → synthesise a minimal DestinationRequest
//     from the resolved default and call destResolver Resolve.
//  3. Otherwise → return a typed unavailable error. Callers must
//     fail closed rather than inventing a destination.

// godlike/07 fail-closed: the function NEVER silently invents a
// destination. If both rules fail, it returns a typed unavailable
// error so the permanent missing-destination failure is explicit.
func TestResolveDestinationWithFallback(t *testing.T) {
	// recordingDestResolverWith allows per-row config of the destination
	// stub's FolderID/FolderPath returned to caller; the stub ALSO records
	// the synthesised *DestinationRequest (so Rule 2 can assert the
	// SHARED destination_helpers.go synthesises the expected shape).
	newRecordingDest := func(folderID, folderPath string) *recordingDestResolver {
		return &recordingDestResolver{folderID: folderID, folderPath: folderPath}
	}

	type want struct {
		folderID         string
		folderPath       string
		wantNil          bool
		wantDefaultCalls int // defaultResolver.Resolve call count
		wantDestCalls    int // destResolver.Resolve call count
		// For Rule 2 only: assert the synthesised request shape.
		wantSynthesised     bool
		wantSynthFolderID   string
		wantSynthFolderPath string
	}

	tests := []struct {
		name    string
		destReq *DestinationRequest
		// defaultRes is typed as VoiceoverDefaultFolderResolver (interface)
		// rather than *stubDefaultFolderResolver (concrete) to avoid the
		// classic Go typed-nil-in-interface trap: a nil concrete pointer
		// boxed into an interface yields a NON-nil interface, defeating
		// production's `if defaultResolver != nil` guard and panicking
		// inside the stub's receiver method.
		defaultRes VoiceoverDefaultFolderResolver
		destStub   *recordingDestResolver
		want       want
	}{
		{
			name:     "Rule 1: explicit destination wins, default NOT consulted",
			destReq:  &DestinationRequest{FolderID: "explicit-folder"},
			destStub: newRecordingDest("explicit-folder", "/tmp/vo"),
			want: want{
				folderID:         "explicit-folder",
				folderPath:       "/tmp/vo",
				wantDefaultCalls: 0,
				wantDestCalls:    1,
			},
		},
		{
			name:    "Rule 2: nil destReq + ok default -> synthesise + call destResolver",
			destReq: nil,
			defaultRes: &stubDefaultFolderResolver{
				folderID:  "default-folder",
				outputDir: "/tmp/vo-default",
				ok:        true,
			},
			destStub: newRecordingDest("resolved-folder", "/tmp/resolved"),
			want: want{
				// The synthesised request is built from the DEFAULT's return
				// values; the dest stub's stub-return is irrelevant for the
				// synthesisation assertion (Rule 2 contract).
				folderID:            "resolved-folder",
				folderPath:          "/tmp/resolved",
				wantDefaultCalls:    1,
				wantDestCalls:       1,
				wantSynthesised:     true,
				wantSynthFolderID:   "default-folder",
				wantSynthFolderPath: "/tmp/vo-default",
			},
		},
		{
			name:    "Rule 2b: nil destReq + default returns ok=false -> no destination",
			destReq: nil,
			defaultRes: &stubDefaultFolderResolver{
				folderID:  "default-folder",
				outputDir: "/tmp/vo-default",
				ok:        false,
			},
			destStub: newRecordingDest("", ""),
			want: want{
				wantNil:          true,
				wantDefaultCalls: 1,
				wantDestCalls:    0,
			},
		},
		{
			name:    "Rule 2c: nil destReq + default returns empty folderID -> no destination",
			destReq: nil,
			defaultRes: &stubDefaultFolderResolver{
				folderID:  "",
				outputDir: "/tmp/vo-default",
				ok:        true,
			},
			destStub: newRecordingDest("", ""),
			want: want{
				wantNil:          true,
				wantDefaultCalls: 1,
				wantDestCalls:    0,
			},
		},
		{
			name:       "Rule 3: nil destReq + nil defaultResolver -> no destination",
			destReq:    nil,
			defaultRes: nil,
			destStub:   newRecordingDest("", ""),
			want: want{
				wantNil:          true,
				wantDefaultCalls: 0,
				wantDestCalls:    0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDestinationWithFallback(
				context.Background(),
				tt.destReq,
				tt.destStub,
				tt.defaultRes,
				zap.NewNop(),
			)
			if tt.want.wantNil {
				require.Error(t, err, "no available destination must fail closed")
				assert.ErrorIs(t, err, ErrVoiceoverDestinationUnavailable)
				assert.Nil(t, got, "failed destination resolution must return no destination")
			} else {
				require.NoError(t, err)

				require.NotNil(t, got, "Rule 1 or Rule 2 must return a non-nil ResolvedDestination")
				assert.Equal(t, tt.want.folderID, got.FolderID)
				assert.Equal(t, tt.want.folderPath, got.FolderPath)
			}
			if tt.defaultRes != nil {
				// Type-assert back to the concrete stub to read resolveCalls
				// (the Stub recorder field is canonical here per godlike/06).
				// godlike/07 fail-closed: a non-stub concrete would silently
				// lose call-count coverage; require.True surfaces the regression.
				stub, ok := tt.defaultRes.(*stubDefaultFolderResolver)
				require.True(t, ok,
					"call-count assertion requires stubDefaultFolderResolver concrete (got %T)", tt.defaultRes)
				assert.Equal(t, tt.want.wantDefaultCalls, stub.resolveCalls,
					"defaultResolver.Resolve call count mismatch")
			}
			// Rule 2 ONLY: assert the synthesised destination_request shape.
			// This catches the silent-success failure mode where production
			// passes nil OR a wrong-shape synthesised request to
			// destResolver.Resolve -- both would still return a successful
			// dest from the stub's stub-return, hiding the divergence.
			if tt.want.wantSynthesised {
				require.NotNil(t, tt.destStub.lastRequest,
					"Rule 2 must call destResolver.Resolve with the synthesised request")
				assert.Equal(t, tt.want.wantSynthFolderID, tt.destStub.lastRequest.FolderID,
					"synthesised DestinationRequest.FolderID must mirror the default resolver's return value")
				assert.Equal(t, tt.want.wantSynthFolderPath, tt.destStub.lastRequest.FolderPath,
					"synthesised DestinationRequest.FolderPath must mirror the default resolver's outputDir return value")
			}
		})
	}
}

// Explicit destination failures are terminal: the configured default must
// never be consulted after an explicit request has been supplied.
type failingDestinationResolver struct {
	err error
}

func (r failingDestinationResolver) Resolve(context.Context, *DestinationRequest) (*ResolvedDestination, error) {
	return nil, r.err
}

func TestResolveDestinationWithFallback_ExplicitFolderIDIsForwardedWithoutFallback(t *testing.T) {
	resolver := &recordingDestResolver{folderID: "resolved-folder", folderPath: "/tmp/vo"}
	defaultResolver := &stubDefaultFolderResolver{folderID: "historical-root", ok: true}
	req := &DestinationRequest{
		Kind:     string(KindExplicit),
		FolderID: "payload-folder-id",
		Project:  "payload-project",
	}

	resolved, err := ResolveDestinationWithFallback(
		context.Background(), req, resolver,
		defaultResolver, zap.NewNop(),
	)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, "resolved-folder", resolved.FolderID)
	assert.Equal(t, 1, resolver.resolveCalls)
	require.NotNil(t, resolver.lastRequest)
	assert.Equal(t, "payload-folder-id", resolver.lastRequest.FolderID)
	assert.Equal(t, string(KindExplicit), resolver.lastRequest.Kind)
	assert.Equal(t, "payload-project", resolver.lastRequest.Project)

	// The default resolver is intentionally not consulted when the
	// payload carries an explicit folder.
	assert.Zero(t, defaultResolver.resolveCalls)
}

func TestResolveDestinationWithFallback_ExplicitFailureDoesNotUseDefault(t *testing.T) {
	defaultResolver := &stubDefaultFolderResolver{folderID: "historical-root", ok: true}
	resolverErr := errors.New("explicit Drive folder is unavailable")

	resolved, err := ResolveDestinationWithFallback(
		context.Background(),
		&DestinationRequest{Kind: string(KindExplicit), FolderID: "requested-folder"},
		failingDestinationResolver{err: resolverErr},
		defaultResolver,
		zap.NewNop(),
	)

	assert.Contains(t, err.Error(), resolverErr.Error())
	assert.ErrorIs(t, err, ErrVoiceoverDestinationUnavailable)
	assert.Nil(t, resolved)
	assert.Zero(t, defaultResolver.resolveCalls,
		"an explicit destination failure must not consult the configured default")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 4: Execute full 4-stage success (TTS → AudioPost → Publish → Finalize)
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_SuccessFull4Stages asserts the
// canonical 4-stage per-item pipeline runs the gates end-to-end with
// hermetic stub ports. Mirrors the happy-path setup in
// process_voiceover_item_test.go::TestRemoveSilenceRunsExactlyOnce
// so the canonical seam is consistent across both use cases.
//
// Asserted invariants:
//  1. TTSProvider.Synthesize invoked exactly once with the canonical
//     input shape (Text/Language/Voice/Filename/OutputDir/RemoveSilence=false).
//  2. AudioPostProcessor NOT invoked when RemoveSilence is false
//     (P0.2 Fase 2c: silence removal is post-TTS, not inline).
//  3. Publisher.Publish invoked exactly once with (LocalPath=cleaned
//     path from TTSOutput, Filename, FolderID).
//  4. Finalizer.Finalize invoked exactly once with the canonical
//     FinalizeCommand shape, and tx.Commit completes successfully
//     (verified by result.Status=StatusCompleted).
func TestProcessSegmentUseCase_Execute_SuccessFull4Stages(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:   "/tmp/vo/run-pipeline-test.mp3",
			CleanedPath: "",
			Voice:       "en-US-RogerNeural",
			FileHash:    "vp-run-pipeline-test-aabbcc",
		},
	}
	dest := &stubProcessDestResolver{folderID: "dest-folder-1"}
	pub := &stubProcessPublisher{fileID: "drive-published-id-123"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-id-finalizer-returned", Reused: false},
	}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-folder-1"})
	require.NoError(t, err)

	cmd := &ProcessSegmentCommand{
		ID:            "vo-id-canonical",
		RequestID:     "req-run-pipeline-test",
		TextHash:      "hash-run-pipeline-001",
		Text:          "Hello from the per-item pipeline test",
		Language:      "en",
		Voice:         "en-US-RogerNeural",
		Filename:      "run-pipeline-test.mp3",
		Strategy:      "replace",
		RemoveSilence: false,
		Dest:          resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.NoError(t, err, "Execute must succeed in the happy path")
	require.NotNil(t, out, "Execute must return a non-nil VoiceoverItemResult on success")
	assert.Equal(t, StatusCompleted, out.Status, "happy-path status must be StatusCompleted")
	// Production invariant (process_segment.go): when FinalizerResult.Reused=false,
	// out.ID is NOT overwritten — it stays at cmd.ID (the canonical caller-computed ID).
	// The Reused=true adoption path is pinned by the Finalizer.Finalize contract;
	// this test exercises the non-Reused path explicitly.
	assert.Equal(t, "vo-id-canonical", out.ID,
		"non-Reused FinalizeResult MUST preserve cmd.ID (ProcessSegmentUseCase only adopts matched ID when FinalizeResult.Reused=true)")
	assert.Equal(t, "drive-published-id-123", out.DriveFileID, "result.DriveFileID mirrors Publisher return")

	// Stage 1 (TTS) assertion
	require.Len(t, tts.synthesized, 1, "TTSProvider.Synthesize must be called exactly once")
	assert.Equal(t, cmd.Text, tts.synthesized[0].Text)
	assert.Equal(t, cmd.Language, tts.synthesized[0].Language)
	assert.Equal(t, cmd.Voice, tts.synthesized[0].Voice)
	assert.Equal(t, cmd.Filename, tts.synthesized[0].Filename)
	assert.Equal(t, resolvedDest.FolderPath, tts.synthesized[0].OutputDir, "TTS OutputDir must equal resolvedDest.FolderPath")
	assert.False(t, tts.synthesized[0].RemoveSilence,
		"P0.2 Fase 2c invariant: TTSProvider.Synthesize ALWAYS receives RemoveSilence=false (silence removal is post-TTS only)")

	// Stage 3 (Publish) assertion
	require.Len(t, pub.published, 1, "Publisher.Publish must be called exactly once")
	assert.Equal(t, cmd.ID, pub.published[0].ID)
	assert.Equal(t, tts.cannedOut.LocalPath, pub.published[0].LocalPath, "Publisher LocalPath = TTS LocalPath when no audio post")
	assert.Equal(t, cmd.Filename, pub.published[0].Filename)
	assert.Equal(t, resolvedDest.FolderID, pub.published[0].FolderID)

	// Stage 4 (Finalize) assertion
	require.Len(t, finalizer.calls, 1, "Finalizer.Finalize must be called exactly once")
	finCmd := finalizer.calls[0]
	assert.Equal(t, cmd.ID, finCmd.ID)
	assert.Equal(t, cmd.RequestID, finCmd.RequestID)
	assert.Equal(t, "drive-published-id-123", finCmd.DriveFileID,
		"FinalizeCommand.DriveFileID must be the Publisher return value")
	assert.Equal(t, tts.cannedOut.LocalPath, finCmd.LocalPath)
	assert.Equal(t, cmd.Filename, finCmd.Filename)
	require.NotEmpty(t, finCmd.MetaJSON, "FinalizeCommand.MetaJSON must be populated from metaBuf")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 5: meta style_group injection (reviewer-fix #3)
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_StyleGroupMetadataInjection pins
// the metaBuf["style_group"] injection block — the canvas added in
// the PR-VO-USECASE-PROCESS-DRY review-fix #3 to keep the per-item
// path's meta-merge logic equivalent to the pre-DRY per-item path's
// behavior. Without this block, downstream consumers reading
// meta["style_group"] from the per-item path would receive an
// empty value, silently regressing the wire shape.
//
// Asserted invariants:
//  1. When dest.StyleGroup is non-empty AND !StyleGroup.IsEmpty(),
//     the FinalizeCommand.MetaJSON MUST contain a "style_group"
//     key equal to the dest.StyleGroup value.
//  2. When dest.StyleGroup is empty (IsEmpty true), the
//     MetaJSON MUST NOT contain a "style_group" key (the block
//     is conditional).
func TestProcessSegmentUseCase_Execute_StyleGroupMetadataInjection(t *testing.T) {
	// Sub-test 5a: StyleGroup non-empty → meta["style_group"] populated.
	t.Run("style_group injected when dest.StyleGroup non-empty", func(t *testing.T) {
		db := openProcessTestDB(t)
		finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-sg1"}}

		destWithStyle := &ResolvedDestination{
			FolderID:   "dest-sg",
			FolderPath: "/tmp/vo-sg",
			StyleGroup: StyleGroup("cinematic-2026"),
		}

		uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
			TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/sg.mp3"}},
			Publisher:           &stubProcessPublisher{},
			VoiceoverRepository: &stubProcessVoRepo{db: db},
			Finalizer:           finalizer,
			Logger:              zap.NewNop(),
		})

		cmd := &ProcessSegmentCommand{
			ID:       "vo-sg1",
			Language: "en",
			Filename: "sg.mp3",
			Dest:     destWithStyle,
		}
		_, err := uc.Execute(context.Background(), cmd)
		require.NoError(t, err)
		require.Len(t, finalizer.calls, 1)

		var meta map[string]any
		require.NoError(t, json.Unmarshal(finalizer.calls[0].MetaJSON, &meta),
			"MetaJSON must be valid JSON parseable into a map")
		assert.Equal(t, "cinematic-2026", meta["style_group"],
			"PR-VO-USECASE-PROCESS-DRY review-fix #3: metaBuf[\"style_group\"] MUST be injected when dest.StyleGroup is non-empty")
	})

	// Sub-test 5b: StyleGroup empty → meta["style_group"] ABSENT.
	t.Run("style_group absent when dest.StyleGroup empty", func(t *testing.T) {
		db := openProcessTestDB(t)
		finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-sg2"}}

		destNoStyle := &ResolvedDestination{
			FolderID:   "dest-no-sg",
			FolderPath: "/tmp/vo-no-sg",
			// StyleGroup zero value should be IsEmpty.
			StyleGroup: StyleGroup(""),
		}

		uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
			TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/nosg.mp3"}},
			Publisher:           &stubProcessPublisher{},
			VoiceoverRepository: &stubProcessVoRepo{db: db},
			Finalizer:           finalizer,
			Logger:              zap.NewNop(),
		})

		cmd := &ProcessSegmentCommand{
			ID:       "vo-sg2",
			Language: "en",
			Filename: "nosg.mp3",
			Dest:     destNoStyle,
		}
		_, err := uc.Execute(context.Background(), cmd)
		require.NoError(t, err)
		require.Len(t, finalizer.calls, 1)

		var meta map[string]any
		require.NoError(t, json.Unmarshal(finalizer.calls[0].MetaJSON, &meta))
		_, present := meta["style_group"]
		assert.False(t, present,
			"PR-VO-USECASE-PROCESS-DRY: when dest.StyleGroup is empty, meta JSON MUST NOT include a 'style_group' key (the injection block is conditional)")
	})
}

// ─────────────────────────────────────────────────────────────────────────
// Test 6: Stage 0 missing-folder guard (pre-TTS short-circuit)
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_Stage0MissingFolderGuard asserts
// the pre-TTS Stage 0 short-circuit: when cmd.Dest is nil OR
// cmd.Dest.FolderID is empty, Execute MUST surface the canonical
// missing-folder failure WITHOUT invoking TTSProvider. This
// preserves the pre-DRY per-item path's failure-mode contract
// (P0.1 Fase 1b) so the audit pin on Stage 0 surface layer remains
// byte-stable across the DRY refactor.
//
// Asserted invariants:
//  1. Execute returns (out, err) with Status=StatusFailed AND
//     out.Error containing the canonical "missing_folder_id:" prefix.
//  2. TTSProvider.Synthesize is NOT invoked (zero calls in the stub
//     recorder) — the guard fires before Stage 1.
//  3. Publisher.Publish is NOT invoked — same reason.
//  4. Finalizer.Finalize is NOT invoked — same reason.
func TestProcessSegmentUseCase_Execute_Stage0MissingFolderGuard(t *testing.T) {
	tests := []struct {
		name string
		dest *ResolvedDestination
	}{
		{"nil Dest", nil},
		{"Dest with empty FolderID", &ResolvedDestination{FolderID: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Per-subtest stubs (godlike/07 hermetic discipline: fresh
			// recorders per subtest, no manual reset -- safer against
			// future stub field additions).
			db := openProcessTestDB(t)
			tts := &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/should-never-be-used.mp3"}}
			pub := &stubProcessPublisher{fileID: "should-never-be-set"}
			finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "should-never-be-set"}}

			uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
				TTSProvider:         tts,
				Publisher:           pub,
				VoiceoverRepository: &stubProcessVoRepo{db: db},
				Finalizer:           finalizer,
				Logger:              zap.NewNop(),
			})

			cmd := &ProcessSegmentCommand{
				ID:       "vo-missing-folder",
				Language: "en",
				Filename: "missing.mp3",
				Dest:     tt.dest,
			}

			out, err := uc.Execute(context.Background(), cmd)
			require.Error(t, err, "Stage 0 missing-folder MUST return non-nil error")
			require.NotNil(t, out, "Stage 0 missing-folder MUST return non-nil VoiceoverItemResult (per-item error envelope contract)")
			assert.Equal(t, StatusFailed, out.Status)
			assert.Contains(t, out.Error, "missing_folder_id",
				"error envelope must surface the canonical missing_folder_id prefix")
			assert.Len(t, tts.synthesized, 0,
				"Stage 0 missing-folder MUST short-circuit BEFORE TTSProvider.Synthesize invocation")
			assert.Len(t, pub.published, 0,
				"Stage 0 missing-folder MUST short-circuit BEFORE Publisher.Publish invocation")
			assert.Len(t, finalizer.calls, 0,
				"Stage 0 missing-folder MUST short-circuit BEFORE Finalizer.Finalize invocation")
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 7: Stage 1 TTS success — valid input generates non-empty output
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_Stage1_TTS_GeneratesNonEmptyOutput
// pins the FASE 2 contract #1: when TTSProvider.Synthesize succeeds
// with valid input (non-empty Text + supported Language), the result
// carries a non-empty LocalPath and the pipeline proceeds to Stage 3
// (Publisher invoked) and Stage 4 (Finalizer invoked). The test
// asserts the output envelope fields are populated end-to-end.
//
// godlike/07 NO-FAKE-AVAILABILITY: the TTS stub returns a concrete
// LocalPath, not an empty string — a broken TTS bridge that silently
// produces empty output would fail this assertion at the contract layer.
func TestProcessSegmentUseCase_Execute_Stage1_TTS_GeneratesNonEmptyOutput(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath: "/tmp/vo/stage1-valid.mp3",
			Voice:     "it-IT-ElsaNeural",
			FileHash:  "hash-stage1-valid-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-stage1-valid"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-stage1-valid", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-stage1"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-stage1"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-stage1-valid",
		Language: "it-IT",
		Text:     "Questo è un test del sintetizzatore vocale.",
		Voice:    "it-IT-ElsaNeural",
		Filename: "stage1-valid.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.NoError(t, err, "TTS success must not return error")
	require.NotNil(t, out)
	assert.Equal(t, StatusCompleted, out.Status,
		"successful 4-stage pipeline must end with StatusCompleted")
	assert.NotEmpty(t, out.LocalPath,
		"FASE 2 contract #1: TTS must produce a non-empty LocalPath")
	assert.Equal(t, "/tmp/vo/stage1-valid.mp3", out.LocalPath)
	assert.Equal(t, "it-IT-ElsaNeural", out.Voice)
	assert.Equal(t, "hash-stage1-valid-001", out.FileHash)

	// Stage 3 (Publish) must have been invoked exactly once.
	require.Len(t, pub.published, 1,
		"TTS success → Publisher.Publish must be invoked exactly once")

	// Stage 4 (Finalize) must have been invoked exactly once.
	require.Len(t, finalizer.calls, 1,
		"TTS success → Finalizer.Finalize must be invoked exactly once")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 8: Stage 1 TTS failure — error propagated, pipeline short-circuits
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_Stage1_TTS_Fails_PropagatesError
// pins the FASE 2 contract #2: when TTSProvider.Synthesize fails
// (returning a non-nil error), the use case MUST surface a
// StatusFailed result with a "tts_failed:" error prefix, and Stages
// 3–4 MUST NOT be invoked (short-circuit at the first failure).
//
// The canonical failure triggers (empty text, unsupported language,
// missing Python bridge, ffmpeg crash) are the TTS provider's
// responsibility — the use case's contract is to propagate the error
// and stop the pipeline, not to re-classify the failure mode.
func TestProcessSegmentUseCase_Execute_Stage1_TTS_Fails_PropagatesError(t *testing.T) {
	tests := []struct {
		name     string
		ttsErr   error
		wantText string
	}{
		{
			name:     "empty text rejected by TTS bridge",
			ttsErr:   fmt.Errorf("tts: empty text not allowed"),
			wantText: "",
		},
		{
			name:     "unsupported language rejected by TTS bridge",
			ttsErr:   fmt.Errorf("tts: unsupported language 'xx-XX'"),
			wantText: "Hello in unsupported locale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openProcessTestDB(t)
			tts := &stubProcessTTS{
				cannedErr: tt.ttsErr,
				cannedOut: TTSOutput{}, // zero-value; never reached
			}
			pub := &stubProcessPublisher{fileID: "should-never-be-used"}
			finalizer := &stubProcessFinalizer{
				cannedRes: &FinalizeResult{ID: "should-never-be-used"},
			}

			dest := &stubProcessDestResolver{folderID: "dest-stage1-fail"}
			resolvedDest, err := dest.Resolve(context.Background(),
				&DestinationRequest{FolderID: "dest-stage1-fail"})
			require.NoError(t, err)

			uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
				TTSProvider:         tts,
				Publisher:           pub,
				VoiceoverRepository: &stubProcessVoRepo{db: db},
				Finalizer:           finalizer,
				Logger:              zap.NewNop(),
			})

			cmd := &ProcessSegmentCommand{
				ID:       "vo-stage1-fail",
				Language: "en",
				Text:     tt.wantText,
				Filename: "stage1-fail.mp3",
				Dest:     resolvedDest,
			}

			out, err := uc.Execute(context.Background(), cmd)

			require.Error(t, err,
				"FASE 2 contract #2: TTS failure MUST return error")
			require.NotNil(t, out, "error envelope must be non-nil")
			assert.Equal(t, StatusFailed, out.Status,
				"TTS failure MUST set Status=StatusFailed")
			assert.Contains(t, out.Error, "tts_failed:",
				"error prefix must be 'tts_failed:' (canonical per-item surface)")

			// godlike/07 NO-FAKE-AVAILABILITY: Publisher + Finalizer
			// MUST NOT be invoked after TTS failure.
			assert.Len(t, pub.published, 0,
				"Publisher.Publish MUST NOT be invoked after TTS failure (short-circuit at Stage 1)")
			assert.Len(t, finalizer.calls, 0,
				"Finalizer.Finalize MUST NOT be invoked after TTS failure")
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 9: Stage 3 Publisher — Language + Project forwarded correctly
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_Stage3_Publisher_ForwardsLanguageAndProject
// pins the FASE 2 contract #3: when ProcessSegmentCommand carries a
// non-empty Language and Project, the VoiceoverPublishCommand forwarded
// to the Publisher MUST carry those exact values. This is the regression
// guard for the PR-VO-LANGUAGE-PROJECT-PROPAGATION fix (July 2026) —
// pre-fix the fields were silently dropped.
//
// The adapter (useCasePublisherAdapter) then routes Language→req.Language
// and Project→req.ProjectID (semantic-first path). This test does NOT
// assert the adapter-side translation (that contract lives at
// adapters_voiceover_publisher_test.go) — it ONLY asserts the use case
// correctly threads the fields into Port publish command.
func TestProcessSegmentUseCase_Execute_Stage3_Publisher_ForwardsLanguageAndProject(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath: "/tmp/vo/stage3-lang-proj.mp3",
			Voice:     "it-IT-ElsaNeural",
			FileHash:  "hash-stage3-lp-001",
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
			LocalPath: "/tmp/vo/stage3-no-lang.mp3",
			Voice:     "en-US-RogerNeural",
			FileHash:  "hash-stage3-nolang",
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
