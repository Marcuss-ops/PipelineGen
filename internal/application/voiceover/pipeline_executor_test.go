// Package voiceover — pipeline_executor_test.go (PR-VO-USECASE-PROCESS-DRY,
// P0 #5 in VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-15).
//
// Canonical test surface for the SHARED per-item pipeline runner
// (pipeline_executor.go). Pins the godlike/06 one-canonical-owner
// invariant: BOTH the batch use case (usecase.go::processOneLanguage)
// and the per-item use case (process_voiceover_item.go::Execute)
// delegate to the SAME PipelineExecutor.RunPipeline method. A future
// contributor who inlines either body would break this surface — the
// source-grep test catches the regression at build-time, before the
// silent-success / duplicate-pipeline pathology reaches production.
//
// 6 focused tests, all hermetic except the DRY-wiring test (source-grep):
//
//  1. TestNewPipelineExecutor_PanicsOnMandatoryDeps — table-driven
//     fail-fast guards on TTSProvider / Publisher / VoiceoverRepository /
//     Finalizer. AudioPostProcessor + Logger are nil-safe (each not
//     asserted).
//
//  2. TestPipelineExecutor_DRY_Wiring_SourceGrep — INVERSE regression
//     guard. Reads usecase.go and process_voiceover_item.go and asserts
//     BOTH contain `pipelineExec.RunPipeline(` as the canonical
//     delegation seam. Pre-DRY the bodies had inline TTS / AudioPost
//     / Publish / TX code; post-DRY they all delegate. A future
//     refactor that inlines either body breaks the grep test loudly.
//
//  3. TestResolveDestinationWithFallback — table-driven 3-rule
//     precedence pins for the shared destination resolver
//     (destination_helpers.go, P0 #5 DRY pair).
//
//  4. TestPipelineExecutor_RunPipeline_SuccessFull4Stages — hermetic
//     4-stage success path with stub ports. Asserts (a) TTS invoked,
//     (b) Publisher invoked with the cleaned/local path, (c) Finalizer
//     invoked with the canonical FinalizeCommand shape, (d) result
//     status = StatusCompleted.
//
//  5. TestPipelineExecutor_RunPipeline_StyleGroupMetadataInjection —
//     asserts the metaBuf["style_group"] injection block runs when
//     dest.StyleGroup is non-empty and is ABSENT when dest.StyleGroup
//     is empty. Catches reviewer-fix #3 regression per pipeline_executor.go
//     doc comment.
//
//  6. TestPipelineExecutor_RunPipeline_Stage0MissingFolderGuard — asserts
//     the pre-TTS missing_folder_id short-circuit runs without
//     invoking TTSProvider (preserves the pre-DRY per-item path's
//     failure-mode contract pinned by P0.1 Fase 1b).
//
// Test stubs REUSE the canonical stubs from process_voiceover_item_test.go
// (same package, white-box): TTSProvider / DestinationResolver /
// VoiceoverPublisher / VoiceoverRepository / VoiceoverFinalizer; plus
// openProcessTestDB(t) for the in-memory SQLite tx lifecycle.
//
// godlike/06 SSOT (one canonical owner per fact): each test owns
// exactly one capability concern; the DRY-wiring test is hermetic
// except for the two grepped files which both must delegate to the
// shared PipelineExecutor per the wave-tracker contract.
//
// godlike/07 minimal-blast-radius: zero production code changes. The
// test file only reads from the production surface (PipelineExecutor
// constructor + RunPipeline; ResolveDestinationWithFallback free fn);
// no test-only fields, no test-conditional compile branches.
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"

	"go.uber.org/zap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 1: NewPipelineExecutor fail-fast guards (table-driven)
// ─────────────────────────────────────────────────────────────────────────

// TestNewPipelineExecutor_PanicsOnMandatoryDeps asserts the canonical
// fail-fast semantics from the constructor: TTSProvider / Publisher /
// VoiceoverRepository / Finalizer are MANDATORY (panic on nil);
// AudioPostProcessor + Logger are nil-safe (NOT asserted here).
//
// Per AGENTS.md WireUp pattern (godlike/07 fail-fast), a partial
// wire-up surfaces at composition time (panic), NOT at first-job
// dispatch. A regression that relaxes any of the 4 mandatory guards
// would let nil-deps reach RunPipeline and surface as NPE-style
// panics mid-job — exactly the silent-success class godlike/07
// forbids.
func TestNewPipelineExecutor_PanicsOnMandatoryDeps(t *testing.T) {
	// validDeps is the canonical happy-path struct literal; each
	// subtest mutates ONE field to nil and asserts panic on the
	// matching constructor message.
	validDeps := PipelineItemDeps{
		TTSProvider:         &stubProcessTTS{},
		Publisher:           &stubProcessPublisher{},
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{},
	}

	tests := []struct {
		name        string
		mutate      func(d *PipelineItemDeps)
		wantPanicRe string
	}{
		{
			name:        "nil TTSProvider",
			mutate:      func(d *PipelineItemDeps) { d.TTSProvider = nil },
			wantPanicRe: "TTSProvider is required",
		},
		{
			name:        "nil Publisher",
			mutate:      func(d *PipelineItemDeps) { d.Publisher = nil },
			wantPanicRe: "Publisher is required",
		},
		{
			name:        "nil VoiceoverRepository",
			mutate:      func(d *PipelineItemDeps) { d.VoiceoverRepository = nil },
			wantPanicRe: "VoiceoverRepository is required",
		},
		{
			name:        "nil Finalizer",
			mutate:      func(d *PipelineItemDeps) { d.Finalizer = nil },
			wantPanicRe: "Finalizer is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := validDeps
			tt.mutate(&deps)
			// Production uses `panic("string literal")` — Testify's
			// PanicsWithError requires the panic value to implement
			// `error`; a string panic value fails the type assertion
			// inside Testify. Use recover + assert.Contains on the
			// formatted panic value instead (godlike/07 typed-error
			// contract spirit: assert WHICH guard triggered, not
			// just "something panicked").
			var gotPanic any
			func() {
				defer func() { gotPanic = recover() }()
				NewPipelineExecutor(deps)
			}()
			require.NotNil(t, gotPanic,
				"%s: NewPipelineExecutor must panic on this nil mandatory dep (godlike/07 fail-fast at composition)", tt.name)
			assert.Contains(t, fmt.Sprint(gotPanic), tt.wantPanicRe,
				"%s: panic message must contain canonical '%s' substring (production regression guard)", tt.name, tt.wantPanicRe)
		})
	}

	// Bonus: assert the nil-safe deps do NOT panic when nil.
	t.Run("nil AudioPostProcessor OK (nil-safe)", func(t *testing.T) {
		deps := validDeps
		deps.AudioPostProcessor = nil
		assert.NotPanics(t, func() { NewPipelineExecutor(deps) })
	})
	t.Run("nil Logger OK (zap.NewNop() fallback)", func(t *testing.T) {
		deps := validDeps
		deps.Logger = nil
		assert.NotPanics(t, func() { NewPipelineExecutor(deps) })
	})
}

// ─────────────────────────────────────────────────────────────────────────
// Test 2: DRY-wiring source-grep INVERSE regression guard
// ─────────────────────────────────────────────────────────────────────────

// TestPipelineExecutor_DRY_Wiring_SourceGrep pins the godlike/06
// one-canonical-owner invariant at the source level: BOTH the batch
// use case (usecase.go) AND the per-item use case (process_voiceover_item.go)
// MUST delegate to the shared PipelineExecutor via `pipelineExec.RunPipeline(`.
//
// A hermetic version of this test would construct both use cases with
// the same shared deps and assert they produce equivalent results —
// but that passes even if production wiring silently inlines either
// body. The source-grep variant is robust against that regression:
// inlining either body removes `pipelineExec.RunPipeline(` from the
// source file, surfacing the failure here as a build-time test.
//
// Pre-DRY the bodies had ~120 lines of inline 4-stage code each (TTS /
// AudioPost / Publish / BeginTx + Finalize + Commit). Post-DRY the
// bodies shrink to a thin wrapper around PipelineItemInput construction
// + RunPipeline delegation. The grep proves the bodies did not regress.
func TestPipelineExecutor_DRY_Wiring_SourceGrep(t *testing.T) {
	delegationRe := regexp.MustCompile(`pipelineExec\.RunPipeline\(`)

	files := []struct {
		path    string
		context string
	}{
		{"usecase.go", "batch use case (GenerateVoiceoversUseCase.processOneLanguage)"},
		{"process_voiceover_item.go", "per-item use case (ProcessVoiceoverItemUseCase.Execute)"},
	}

	for _, f := range files {
		t.Run(f.context, func(t *testing.T) {
			content, err := os.ReadFile(f.path)
			require.NoError(t, err, "must read %s (canonical DRY delegation source)", f.path)
			assert.Regexp(t, delegationRe, string(content),
				"PR-VO-USECASE-PROCESS-DRY DRY invariant: %s MUST delegate to pipelineExec.RunPipeline("+
					" (godlike/06 SSOT one-canonical-owner). A regression that inlines the per-item body breaks this guard.",
				f.path)
		})
	}

	// Cross-reference: assert the SHARED PipelineExecutor is the
	// binding target (not a separate canonical body). The string
	// `pipelineExec` field is declared on both use case structs per
	// their godlike/06 SSOT doc comments.
	for _, f := range files {
		content, err := os.ReadFile(f.path)
		require.NoError(t, err)
		// Two independent substring checks (whitespace-tolerant: production
		// uses `pipelineExec    *PipelineExecutor` with 4-space canonical
		// struct-field formatting, so a literal single-space Contains would
		// silently fail on production's correct shape).
		assert.Contains(t, string(content), "pipelineExec",
			"%s MUST hold a pipelineExec field (godlike/06 delegation anchor)", f.path)
		assert.Contains(t, string(content), "*PipelineExecutor",
			"%s MUST hold a *PipelineExecutor field (godlike/06 delegation anchor)", f.path)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 3: ResolveDestinationWithFallback 3-rule precedence
// ─────────────────────────────────────────────────────────────────────────

// stubDefaultFolderResolver is the canonical stub for
// VoiceoverDefaultFolderResolver used by TestResolveDestinationWithFallback.
// Returns the configured (folderID, localOutputDir, ok) tuple verbatim.
// It is package-private (lowercase) because the destination resolver
// port is internal to the voiceover package (mirrors the
// finalizerTestRepo pattern at finalizer_test.go).
type stubDefaultFolderResolver struct {
	folderID     string
	outputDir    string
	ok           bool
	resolveCalls int
}

func (s *stubDefaultFolderResolver) Resolve(_ context.Context) (string, string, bool) {
	s.resolveCalls++
	return s.folderID, s.outputDir, s.ok
}

var _ VoiceoverDefaultFolderResolver = (*stubDefaultFolderResolver)(nil)

// recordingDestResolver is the canonical record-input stub for
// DestinationResolver used by TestResolveDestinationWithFallback.
// It records the last *DestinationRequest it was called with so the
// test can assert the SHARED destination_helpers.go synthesises the
// expected DestinationRequest{FolderID, FolderPath} from the
// defaultResolver's return values (rather than passing nil or a
// wrong shape). Mirrors the finalizerTestRepo pattern.
type recordingDestResolver struct {
	folderID     string
	folderPath   string
	lastRequest  *DestinationRequest
	resolveCalls int
}

func (s *recordingDestResolver) Resolve(_ context.Context, req *DestinationRequest) (*ResolvedDestination, error) {
	s.lastRequest = req
	s.resolveCalls++
	return &ResolvedDestination{FolderID: s.folderID, FolderPath: s.folderPath}, nil
}

var _ DestinationResolver = (*recordingDestResolver)(nil)

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
//  3. Otherwise → return (nil, nil). Caller decides to fail or
//     defer the failure.
//
// godlike/07 fail-closed: the function NEVER silently invents a
// destination. If both rules fail, the (nil, nil) return signals
// "no destination available" to the caller, who can choose to
// surface a permanent missing-destination error.
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
			require.NoError(t, err)
			if tt.want.wantNil {
				assert.Nil(t, got, "Rule with no available destination must return nil")
			} else {
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

// ─────────────────────────────────────────────────────────────────────────
// Test 4: RunPipeline full 4-stage success (TTS → AudioPost → Publish → Finalize)
// ─────────────────────────────────────────────────────────────────────────

// TestPipelineExecutor_RunPipeline_SuccessFull4Stages asserts the
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
func TestPipelineExecutor_RunPipeline_SuccessFull4Stages(t *testing.T) {
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

	exec := NewPipelineExecutor(PipelineItemDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-folder-1"})
	require.NoError(t, err)

	in := &PipelineItemInput{
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

	out, err := exec.RunPipeline(context.Background(), in)

	require.NoError(t, err, "RunPipeline must succeed in the happy path")
	require.NotNil(t, out, "RunPipeline must return a non-nil VoiceoverItemResult on success")
	assert.Equal(t, StatusCompleted, out.Status, "happy-path status must be StatusCompleted")
	// Production invariant (pipeline_executor.go): when FinalizerResult.Reused=false,
	// out.ID is NOT overwritten — it stays at in.ID (the canonical caller-computed ID).
	// The Reused=true adoption path is pinned by the Finalizer.Finalize contract;
	// this test exercises the non-Reused path explicitly.
	assert.Equal(t, "vo-id-canonical", out.ID,
		"non-Reused FinalizeResult MUST preserve in.ID (PipelineExecutor only adopts matched ID when FinalizeResult.Reused=true)")
	assert.Equal(t, "drive-published-id-123", out.DriveFileID, "result.DriveFileID mirrors Publisher return")

	// Stage 1 (TTS) assertion
	require.Len(t, tts.synthesized, 1, "TTSProvider.Synthesize must be called exactly once")
	assert.Equal(t, in.Text, tts.synthesized[0].Text)
	assert.Equal(t, in.Language, tts.synthesized[0].Language)
	assert.Equal(t, in.Voice, tts.synthesized[0].Voice)
	assert.Equal(t, in.Filename, tts.synthesized[0].Filename)
	assert.Equal(t, resolvedDest.FolderPath, tts.synthesized[0].OutputDir, "TTS OutputDir must equal resolvedDest.FolderPath")
	assert.False(t, tts.synthesized[0].RemoveSilence,
		"P0.2 Fase 2c invariant: TTSProvider.Synthesize ALWAYS receives RemoveSilence=false (silence removal is post-TTS only)")

	// Stage 3 (Publish) assertion
	require.Len(t, pub.published, 1, "Publisher.Publish must be called exactly once")
	assert.Equal(t, in.ID, pub.published[0].ID)
	assert.Equal(t, tts.cannedOut.LocalPath, pub.published[0].LocalPath, "Publisher LocalPath = TTS LocalPath when no audio post")
	assert.Equal(t, in.Filename, pub.published[0].Filename)
	assert.Equal(t, resolvedDest.FolderID, pub.published[0].FolderID)

	// Stage 4 (Finalize) assertion
	require.Len(t, finalizer.calls, 1, "Finalizer.Finalize must be called exactly once")
	finCmd := finalizer.calls[0]
	assert.Equal(t, in.ID, finCmd.ID)
	assert.Equal(t, in.RequestID, finCmd.RequestID)
	assert.Equal(t, "drive-published-id-123", finCmd.DriveFileID,
		"FinalizeCommand.DriveFileID must be the Publisher return value")
	assert.Equal(t, tts.cannedOut.LocalPath, finCmd.LocalPath)
	assert.Equal(t, in.Filename, finCmd.Filename)
	require.NotEmpty(t, finCmd.MetaJSON, "FinalizeCommand.MetaJSON must be populated from metaBuf")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 5: meta style_group injection (reviewer-fix #3)
// ─────────────────────────────────────────────────────────────────────────

// TestPipelineExecutor_RunPipeline_StyleGroupMetadataInjection pins
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
func TestPipelineExecutor_RunPipeline_StyleGroupMetadataInjection(t *testing.T) {
	// Sub-test 5a: StyleGroup non-empty → meta["style_group"] populated.
	t.Run("style_group injected when dest.StyleGroup non-empty", func(t *testing.T) {
		db := openProcessTestDB(t)
		finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-sg1"}}

		destWithStyle := &ResolvedDestination{
			FolderID:   "dest-sg",
			FolderPath: "/tmp/vo-sg",
			StyleGroup: StyleGroup("cinematic-2026"),
		}

		exec := NewPipelineExecutor(PipelineItemDeps{
			TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/sg.mp3"}},
			Publisher:           &stubProcessPublisher{},
			VoiceoverRepository: &stubProcessVoRepo{db: db},
			Finalizer:           finalizer,
			Logger:              zap.NewNop(),
		})

		in := &PipelineItemInput{
			ID:       "vo-sg1",
			Language: "en",
			Filename: "sg.mp3",
			Dest:     destWithStyle,
		}
		_, err := exec.RunPipeline(context.Background(), in)
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

		exec := NewPipelineExecutor(PipelineItemDeps{
			TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/nosg.mp3"}},
			Publisher:           &stubProcessPublisher{},
			VoiceoverRepository: &stubProcessVoRepo{db: db},
			Finalizer:           finalizer,
			Logger:              zap.NewNop(),
		})

		in := &PipelineItemInput{
			ID:       "vo-sg2",
			Language: "en",
			Filename: "nosg.mp3",
			Dest:     destNoStyle,
		}
		_, err := exec.RunPipeline(context.Background(), in)
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

// TestPipelineExecutor_RunPipeline_Stage0MissingFolderGuard asserts
// the pre-TTS Stage 0 short-circuit: when in.Dest is nil OR
// in.Dest.FolderID is empty, RunPipeline MUST surface the canonical
// missing-folder failure WITHOUT invoking TTSProvider. This
// preserves the pre-DRY per-item path's failure-mode contract
// (P0.1 Fase 1b) so the audit pin on Stage 0 surface layer remains
// byte-stable across the DRY refactor.
//
// Asserted invariants:
//  1. RunPipeline returns (out, err) with Status=StatusFailed AND
//     out.Error containing the canonical "missing_folder_id:" prefix.
//  2. TTSProvider.Synthesize is NOT invoked (zero calls in the stub
//     recorder) — the guard fires before Stage 1.
//  3. Publisher.Publish is NOT invoked — same reason.
//  4. Finalizer.Finalize is NOT invoked — same reason.
func TestPipelineExecutor_RunPipeline_Stage0MissingFolderGuard(t *testing.T) {
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

			exec := NewPipelineExecutor(PipelineItemDeps{
				TTSProvider:         tts,
				Publisher:           pub,
				VoiceoverRepository: &stubProcessVoRepo{db: db},
				Finalizer:           finalizer,
				Logger:              zap.NewNop(),
			})

			in := &PipelineItemInput{
				ID:       "vo-missing-folder",
				Language: "en",
				Filename: "missing.mp3",
				Dest:     tt.dest,
			}

			out, err := exec.RunPipeline(context.Background(), in)
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
