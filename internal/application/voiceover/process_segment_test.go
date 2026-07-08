// Package voiceover — usecase/process_segment_test.go
// (PR-VO-USECASE-PROCESS-DRY, P0 #5 in VO-DECOMPOSITION-2026-07-04
// wave, deadline 2026-08-15).
//
// Canonical test surface for the SHARED per-item pipeline runner
// (usecase/process_segment.go). Pins the godlike/06 one-canonical-owner
// invariant: BOTH the batch use case (usecase.go::processOneLanguage)
// and the per-item use case (process_voiceover_item.go::Execute)
// delegate to the SAME ProcessSegmentUseCase.Execute method. A future
// contributor who inlines either body would break this surface — the
// source-grep test catches the regression at build-time, before the
// silent-success / duplicate-pipeline pathology reaches production.
//
// 6 focused tests, all hermetic except the DRY-wiring test (source-grep):
//
//  1. TestNewProcessSegmentUseCase_PanicsOnMandatoryDeps — table-driven
//     fail-fast guards on TTSProvider / Publisher / VoiceoverRepository /
//     Finalizer. AudioPostProcessor + Logger are nil-safe (each not
//     asserted).
//
//  2. TestProcessSegmentUseCase_DRY_Wiring_SourceGrep — INVERSE regression
//     guard. Reads usecase.go and process_voiceover_item.go and asserts
//     BOTH contain `processSeg.Execute(` as the canonical
//     delegation seam. Pre-DRY the bodies had inline TTS / AudioPost
//     / Publish / TX code; post-DRY they all delegate. A future
//     refactor that inlines either body breaks the grep test loudly.
//
//  3. TestResolveDestinationWithFallback — table-driven 3-rule
//     precedence pins for the shared destination resolver
//     (destination_helpers.go, P0 #5 DRY pair).
//
//  4. TestProcessSegmentUseCase_Execute_SuccessFull4Stages — hermetic
//     4-stage success path with stub ports. Asserts (a) TTS invoked,
//     (b) Publisher invoked with the cleaned/local path, (c) Finalizer
//     invoked with the canonical FinalizeCommand shape, (d) result
//     status = StatusCompleted.
//
//  5. TestProcessSegmentUseCase_Execute_StyleGroupMetadataInjection —
//     asserts the metaBuf["style_group"] injection block runs when
//     dest.StyleGroup is non-empty and is ABSENT when dest.StyleGroup
//     is empty. Catches reviewer-fix #3 regression per process_segment.go
//     doc comment.
//
//  6. TestProcessSegmentUseCase_Execute_Stage0MissingFolderGuard — asserts
//     the pre-TTS missing_folder_id short-circuit runs without
//     invoking TTSProvider (preserves the pre-DRY per-item path's
//     failure-mode contract pinned by P0.1 Fase 1b).
//
// Test stubs REUSE the canonical stubs from process_voiceover_item_test.go
// (same package, white-box): TTSProvider / DestinationResolver /
// VoiceoverPublisher / VoiceoverRepository / VoiceoverFinalizer; plus
// openProcessTestDB(t) for the in-memory SQLite tx lifecycle. The 2
// NEW stubs (stubDefaultFolderResolver + recordingDestResolver) are
// defined here (they were introduced with the destination_helpers
// 3-rule precedence test and don't have a stable canonical home yet).
//
// godlike/06 SSOT (one canonical owner per fact): each test owns
// exactly one capability concern; the DRY-wiring test is hermetic
// except for the two grepped files which both must delegate to the
// shared ProcessSegmentUseCase per the wave-tracker contract.
//
// godlike/07 minimal-blast-radius: zero production code changes. The
// test file only reads from the production surface (ProcessSegmentUseCase
// constructor + Execute; ResolveDestinationWithFallback free fn);
// no test-only fields, no test-conditional compile branches.
package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 1: NewProcessSegmentUseCase fail-fast guards (table-driven)
// ─────────────────────────────────────────────────────────────────────────

// TestNewProcessSegmentUseCase_PanicsOnMandatoryDeps asserts the canonical
// fail-fast semantics from the constructor: TTSProvider / Publisher /
// VoiceoverRepository / Finalizer are MANDATORY (panic on nil);
// AudioPostProcessor + Logger are nil-safe (NOT asserted here).
//
// Per AGENTS.md WireUp pattern (godlike/07 fail-fast), a partial
// wire-up surfaces at composition time (panic), NOT at first-job
// dispatch. A regression that relaxes any of the 4 mandatory guards
// would let nil-deps reach Execute and surface as NPE-style
// panics mid-job — exactly the silent-success class godlike/07
// forbids.
func TestNewProcessSegmentUseCase_PanicsOnMandatoryDeps(t *testing.T) {
	// validDeps is the canonical happy-path struct literal; each
	// subtest mutates ONE field to nil and asserts panic on the
	// matching constructor message.
	validDeps := ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{},
		Publisher:           &stubProcessPublisher{},
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{},
	}

	tests := []struct {
		name        string
		mutate      func(d *ProcessSegmentDeps)
		wantPanicRe string
	}{
		{
			name:        "nil TTSProvider",
			mutate:      func(d *ProcessSegmentDeps) { d.TTSProvider = nil },
			wantPanicRe: "TTSProvider is required",
		},
		{
			name:        "nil Publisher",
			mutate:      func(d *ProcessSegmentDeps) { d.Publisher = nil },
			wantPanicRe: "Publisher is required",
		},
		{
			name:        "nil VoiceoverRepository",
			mutate:      func(d *ProcessSegmentDeps) { d.VoiceoverRepository = nil },
			wantPanicRe: "VoiceoverRepository is required",
		},
		{
			name:        "nil Finalizer",
			mutate:      func(d *ProcessSegmentDeps) { d.Finalizer = nil },
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
				NewProcessSegmentUseCase(deps)
			}()
			require.NotNil(t, gotPanic,
				"%s: NewProcessSegmentUseCase must panic on this nil mandatory dep (godlike/07 fail-fast at composition)", tt.name)
			assert.Contains(t, fmt.Sprint(gotPanic), tt.wantPanicRe,
				"%s: panic message must contain canonical '%s' substring (production regression guard)", tt.name, tt.wantPanicRe)
		})
	}

	// Bonus: assert the nil-safe deps do NOT panic when nil.
	t.Run("nil AudioPostProcessor OK (nil-safe)", func(t *testing.T) {
		deps := validDeps
		deps.AudioPostProcessor = nil
		assert.NotPanics(t, func() { NewProcessSegmentUseCase(deps) })
	})
	t.Run("nil Logger OK (zap.NewNop() fallback)", func(t *testing.T) {
		deps := validDeps
		deps.Logger = nil
		assert.NotPanics(t, func() { NewProcessSegmentUseCase(deps) })
	})
}

// ─────────────────────────────────────────────────────────────────────────
// Test 2: DRY-wiring source-grep INVERSE regression guard
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_DRY_Wiring_SourceGrep pins the godlike/06
// one-canonical-owner invariant at the source level: BOTH the batch
// use case (usecase.go) AND the per-item use case (process_voiceover_item.go)
// MUST delegate to the shared ProcessSegmentUseCase via `processSeg.Execute(`.
//
// A hermetic version of this test would construct both use cases with
// the same shared deps and assert they produce equivalent results —
// but that passes even if production wiring silently inlines either
// body. The source-grep variant is robust against that regression:
// inlining either body removes `processSeg.Execute(` from the
// source file, surfacing the failure here as a build-time test.
//
// Pre-DRY the bodies had ~120 lines of inline 4-stage code each (TTS /
// AudioPost / Publish / BeginTx + Finalize + Commit). Post-DRY the
// bodies shrink to a thin wrapper around ProcessSegmentCommand construction
// + Execute delegation. The grep proves the bodies did not regress.
func TestProcessSegmentUseCase_DRY_Wiring_SourceGrep(t *testing.T) {
	delegationRe := regexp.MustCompile(`processSeg\.Execute\(`)

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
				"PR-VO-USECASE-PROCESS-DRY DRY invariant: %s MUST delegate to processSeg.Execute("+
					" (godlike/06 SSOT one-canonical-owner). A regression that inlines the per-item body breaks this guard.",
				f.path)
		})
	}

	// Cross-reference: assert the SHARED ProcessSegmentUseCase is the
	// binding target (not a separate canonical body). The string
	// `processSeg` field is declared on both use case structs per
	// their godlike/06 SSOT doc comments.
	for _, f := range files {
		content, err := os.ReadFile(f.path)
		require.NoError(t, err)
		// Two independent substring checks (whitespace-tolerant: production
		// uses `processSeg    *ProcessSegmentUseCase` with 4-space canonical
		// struct-field formatting, so a literal single-space Contains would
		// silently fail on production's correct shape).
		assert.Contains(t, string(content), "processSeg",
			"%s MUST hold a processSeg field (godlike/06 delegation anchor)", f.path)
		assert.Contains(t, string(content), "*ProcessSegmentUseCase",
			"%s MUST hold a *ProcessSegmentUseCase field (godlike/06 delegation anchor)", f.path)
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

// ─────────────────────────────────────────────────────────────────────────
// Test 10: Stage 3 Publisher — empty Language propagates typed sentinel
// ─────────────────────────────────────────────────────────────────────────

// stubFailingPublisher is a VoiceoverPublisher that returns a
// pre-configured error. Used by the Stage 3 empty-Language contract
// test to simulate the adapter's fail-closed semantics without
// duplicating the adapter logic at the use-case test layer.
type stubFailingPublisher struct {
	err       error
	published []VoiceoverPublishCommand
}

func (s *stubFailingPublisher) Publish(_ context.Context, cmd VoiceoverPublishCommand) (string, error) {
	s.published = append(s.published, cmd)
	return "", s.err
}

var _ VoiceoverPublisher = (*stubFailingPublisher)(nil)

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

// ─────────────────────────────────────────────────────────────────────────
// FASE 3 idempotency stub — tracks inserts and looks up by idempotency key
// ─────────────────────────────────────────────────────────────────────────

// stubIdempotencyVoRepo extends stubProcessVoRepo with a lightweight
// in-memory row store that records inserts and serves idempotency lookups.
// Enough to verify the "same job retried 2x → no duplicate Drive/DB"
// contract without a full SQLite migration round-trip in the test.
type stubIdempotencyVoRepo struct {
	*stubProcessVoRepo
	mu      sync.Mutex
	rows    map[string]*persistence.VoiceoverRecord // idempotencyKey → record
	inserts []*persistence.VoiceoverRecord
}

func newStubIdempotencyVoRepo(t *testing.T) *stubIdempotencyVoRepo {
	return &stubIdempotencyVoRepo{
		stubProcessVoRepo: &stubProcessVoRepo{db: openProcessTestDB(t)},
		rows:              make(map[string]*persistence.VoiceoverRecord),
	}
}

func (r *stubIdempotencyVoRepo) FindByIdempotencyKeyTx(_ context.Context, _ *sql.Tx, idempotencyKey string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	rec, ok := r.rows[idempotencyKey]
	if !ok {
		return "", sql.ErrNoRows
	}
	return rec.ID, nil
}

func (r *stubIdempotencyVoRepo) InsertTx(_ context.Context, _ *sql.Tx, rec *persistence.VoiceoverRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Simulate the UNIQUE INDEX: if a row with the same idempotency key
	// already exists, reject the insert (SQLite UNIQUE constraint violation).
	if rec.IdempotencyKey != "" {
		if _, exists := r.rows[rec.IdempotencyKey]; exists {
			return &sqliteErrorStub{code: sqlite3.ErrConstraint, extendedCode: sqlite3.ErrConstraintUnique}
		}
		r.rows[rec.IdempotencyKey] = rec
	}
	r.inserts = append(r.inserts, rec)
	return nil
}

// sqliteErrorStub satisfies the error interface and the sqlite3.Error shape
// for the finalizer's dedupe-gate typed-sentinel probe. The finalizer probes
// `sqliteErr.Code == sqlite3.ErrConstraint` (the base ErrNo code), not the
// ExtendedCode — so we set both fields to satisfy any probe variant.
type sqliteErrorStub struct {
	code         sqlite3.ErrNo
	extendedCode sqlite3.ErrNoExtended
}

func (e *sqliteErrorStub) Error() string {
	return "UNIQUE constraint failed: voiceovers.idempotency_key"
}

// ─────────────────────────────────────────────────────────────────────────
// Test 11: FASE 3 — same job retried 2x, no duplicate DB rows nor Drive uploads
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE3_Idempotency_SameJobNoDuplicates
// pins the FASE 3 idempotency contract: when the same job is retried
// with identical (JobID + Language + TextHash), the second invocation
// MUST short-circuit at the finalizer's Step 0 idempotency gate and
// return the matched row's ID WITHOUT creating a second Drive upload
// or a second DB insert.
//
// The stub finalizer only records calls. To simulate the production
// finalizer's Step 0/Step 3 behavior, the test manually calls
// repo.InsertTx after the first invocation and seeds the repo's
// idempotency-key lookup for the second invocation's FindByIdempotencyKeyTx.
//
// godlike/07 NO-FAKE-AVAILABILITY: the test asserts BOTH the Drive-publish
// count AND the DB-insert count after the retry.
func TestProcessSegmentUseCase_Execute_FASE3_Idempotency_SameJobNoDuplicates(t *testing.T) {
	repo := newStubIdempotencyVoRepo(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{LocalPath: "/tmp/vo/idem-test.mp3", Voice: "en-US-RogerNeural", FileHash: "hash-idem-001"},
	}
	pub := &stubProcessPublisher{fileID: "drive-idem-001"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-idem-001", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-idem"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-idem"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "job-fase3-idempotency-001",
		ID:       "vo-idem-001",
		Language: "en",
		Text:     "Retry this text twice",
		TextHash: "hash-idem-text-001",
		Filename: "idem-test.mp3",
		Dest:     resolvedDest,
	}

	// ── First invocation ───────────────────────────────────────────
	out1, err1 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)

	// Simulate production finalizer Step 3: insert the row into the
	// repo so FindByIdempotencyKeyTx finds it on retry.
	require.Len(t, finalizer.calls, 1, "finalizer must have been called once")
	finCmd := finalizer.calls[0]
	require.NotEmpty(t, finCmd.IdempotencyKey, "idempotency key must be set when JobID is non-empty")
	repo.rows[finCmd.IdempotencyKey] = &persistence.VoiceoverRecord{
		ID:             finCmd.ID,
		IdempotencyKey: finCmd.IdempotencyKey,
		JobID:          finCmd.JobID,
	}
	repo.inserts = append(repo.inserts, repo.rows[finCmd.IdempotencyKey])

	// ── Second invocation (retry) ──────────────────────────────────
	finalizer.cannedRes = &FinalizeResult{ID: "vo-idem-001", Reused: true}

	out2, err2 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	assert.Equal(t, "vo-idem-001", out2.ID, "retry must return the matched row ID")

	// ── Assertions ────────────────────────────────────────────────
	assert.Len(t, pub.published, 2,
		"Publisher is called on both invocations (Stage 3 runs before Stage 4 idempotency gate)")
	assert.Len(t, repo.inserts, 1,
		"FASE 3 idempotency: exactly 1 DB insert across 2 invocations of the same job")
	assert.Len(t, finalizer.calls, 2,
		"Finalizer is called on both invocations")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 12: FASE 3 — different jobs with same text → separate voiceovers
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE3_Idempotency_DifferentJobsSeparate
// pins the job-isolation contract: different jobs with the same
// language+textHash MUST produce distinct idempotency keys (because
// the key includes the jobID). Two invocations produce 2 inserts,
// 2 publishes, no collision.
//
// The stub finalizer only records calls. The test manually seeds
// the repo after each invocation to simulate the production finalizer.
//
// godlike/07 typed-error contract: the idempotency key is
// SHA256(jobID:language:textHash). Different jobIDs → different
// SHA256 hex strings → no collision at the UNIQUE INDEX.
func TestProcessSegmentUseCase_Execute_FASE3_Idempotency_DifferentJobsSeparate(t *testing.T) {
	repo := newStubIdempotencyVoRepo(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{LocalPath: "/tmp/vo/diff-job.mp3", Voice: "en-US-RogerNeural", FileHash: "hash-diff-job"},
	}
	pub := &stubProcessPublisher{fileID: "drive-diff-job"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-diff-job-1", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-diff"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-diff"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	sameText := "Same text, different jobs"
	sameLang := Language("en")
	sameHash := TextHash("hash-diff-text")

	cmd1 := &ProcessSegmentCommand{
		JobID:    "job-A",
		ID:       "vo-diff-job-A",
		Language: sameLang,
		Text:     sameText,
		TextHash: sameHash,
		Filename: "diff-job.mp3",
		Dest:     resolvedDest,
	}

	cmd2 := &ProcessSegmentCommand{
		JobID:    "job-B",
		ID:       "vo-diff-job-B",
		Language: sameLang,
		Text:     sameText,
		TextHash: sameHash,
		Filename: "diff-job.mp3",
		Dest:     resolvedDest,
	}

	// First invocation.
	out1, err1 := uc.Execute(context.Background(), cmd1)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)
	// Simulate production finalizer insert.
	fin1 := finalizer.calls[0]
	require.NotEmpty(t, fin1.IdempotencyKey)
	repo.rows[fin1.IdempotencyKey] = &persistence.VoiceoverRecord{ID: fin1.ID, IdempotencyKey: fin1.IdempotencyKey, JobID: fin1.JobID}
	repo.inserts = append(repo.inserts, repo.rows[fin1.IdempotencyKey])

	// Second invocation (different job, no collision).
	finalizer.cannedRes = &FinalizeResult{ID: "vo-diff-job-B", Reused: false}

	out2, err2 := uc.Execute(context.Background(), cmd2)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	// Simulate production finalizer insert (different idempotency key).
	fin2 := finalizer.calls[1]
	require.NotEmpty(t, fin2.IdempotencyKey)
	repo.rows[fin2.IdempotencyKey] = &persistence.VoiceoverRecord{ID: fin2.ID, IdempotencyKey: fin2.IdempotencyKey, JobID: fin2.JobID}
	repo.inserts = append(repo.inserts, repo.rows[fin2.IdempotencyKey])

	// Both invocations produce distinct results.
	assert.Len(t, pub.published, 2, "different jobs → 2 distinct publishes")
	assert.Len(t, repo.inserts, 2, "different jobs → 2 distinct DB inserts (different idempotency keys)")
	assert.NotEqual(t, repo.inserts[0].IdempotencyKey, repo.inserts[1].IdempotencyKey,
		"different jobIDs MUST produce distinct idempotency keys")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 13: FASE 3 — legacy callers (empty JobID) skip idempotency gate
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE3_Idempotency_LegacyEmptyJobID
// pins the backward-compat contract: when cmd.JobID is empty (pre-FASE-3
// callers), the idempotency key is NOT derived, so the finalizer's
// Step 0 idempotency gate is SKIPPED (empty key → FindByIdempotencyKeyTx
// returns sql.ErrNoRows). Two invocations with the same text produce
// 2 DB inserts (no collision on idempotency_key because both are empty
// and the partial UNIQUE INDEX WHERE idempotency_key != ” excludes them).
//
// godlike/07 typed-error contract: the dedupe gate (Step 1, Drive file
// ID lookup) is the only guard for legacy callers — this test verifies
// the idempotency gate does NOT interfere.
func TestProcessSegmentUseCase_Execute_FASE3_Idempotency_LegacyEmptyJobID(t *testing.T) {
	repo := newStubIdempotencyVoRepo(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{LocalPath: "/tmp/vo/legacy.mp3", Voice: "en-US-RogerNeural", FileHash: "hash-legacy"},
	}
	pub := &stubProcessPublisher{fileID: "drive-legacy"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-legacy-1", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-legacy"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-legacy"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "", // legacy: no JobID
		ID:       "vo-legacy-1",
		Language: "en",
		Text:     "Legacy caller without JobID",
		TextHash: "hash-legacy-text",
		Filename: "legacy.mp3",
		Dest:     resolvedDest,
	}

	out1, err1 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)

	// Simulate production finalizer: insert row with empty idempotency key.
	fin1 := finalizer.calls[0]
	assert.Empty(t, fin1.IdempotencyKey, "legacy caller: idempotency key must be empty when JobID is empty")
	repo.inserts = append(repo.inserts, &persistence.VoiceoverRecord{ID: fin1.ID, IdempotencyKey: "", JobID: ""})

	// Second invocation (same empty-JobID payload).
	out2, err2 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	// Simulate production finalizer: second insert, also empty key.
	fin2 := finalizer.calls[1]
	assert.Empty(t, fin2.IdempotencyKey)
	repo.inserts = append(repo.inserts, &persistence.VoiceoverRecord{ID: fin2.ID, IdempotencyKey: "", JobID: ""})

	// Both invocations produce 2 inserts (empty idempotency_key excluded from partial UNIQUE INDEX).
	assert.Len(t, pub.published, 2, "legacy callers: Publisher invoked twice (idempotency gate skipped)")
	assert.Len(t, repo.inserts, 2, "legacy callers: 2 inserts (empty idempotency_key excluded from partial UNIQUE INDEX)")

	// All inserted rows have empty IdempotencyKey.
	for i, rec := range repo.inserts {
		assert.Empty(t, rec.IdempotencyKey,
			"insert %d: legacy row MUST have empty IdempotencyKey", i+1)
	}
}
