// Package voiceover — usecase/process_segment_e2e_test.go
//
// E2E-phase tests for the SHARED per-item pipeline runner
// (usecase/process_segment.go). These tests cover the late-FASE and
// E2E contracts — audio format validation, transactional outbox
// orphan-cleanup, real-finalizer happy paths, real-finalizer failure
// rollback — WITHOUT re-testing the construction / execution /
// idempotency gates (those live in the construction + execution +
// idempotency files respectively).
//
// godlike/06 SSOT (one canonical owner per fact): each test pins
// exactly one E2E capability concern:
//
//  14. TestIsValidAudioFormat_MP3FrameSync — FASE 2 audio format
//     validation helper (MP3 frame sync + WAV RIFF header).
//
//  15. TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyProject
//     — FASE 2 contract #5 (Publisher empty Project verbatim).
//
//  16. TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup
//     — FASE 4 happy-orphan-cleanup contract (cleanup event in separate tx).
//
//  17. TestProcessSegmentUseCase_Execute_FASE4_Stage0Failure_NoCleanupEvent
//     — FASE 4 nil-guard (Stage 0 short-circuit, no Drive upload, no cleanup).
//
//  18. TestProcessSegmentUseCase_Execute_FASE4_NilOutboxEnqueuer_NoPanic
//     — FASE 4 nil-safe (orphan-cleanup path silently skipped).
//
//  19. TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupBeginTxFail_Warns
//     — FASE 4 BeginTx-failure path (Warn log, no cleanup event).
//
//  20. TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupEnqueueFail_Warns
//     — FASE 4 EnqueueCleanupEvent-failure path (Warn log, tx rolled back).
//
//  21. TestProcessSegmentUseCase_Execute_FASE5_E2E_RealFinalizer_HappyPath
//     — FASE 5 E2E with REAL voiceoverFinalizer (row in SQLite).
//
//  22. TestProcessSegmentUseCase_Execute_FASE5_E2E_IdempotencyReplay
//     — FASE 5 E2E idempotency replay with real finalizer.
//
//  23. TestProcessSegmentUseCase_Execute_FASE6_E2E_OrphanCleanup_RealFinalizer
//     — FASE 6 E2E orphan-cleanup with real finalizer + InsertTx failure.
//
// godlike/07 minimum-blast-radius: zero production code changes.
// Inline test-only helpers (mp3SyncPatterns, wavRIFFHeader,
// isValidAudioFormat) are kept here rather than promoted to the
// shared helpers file because they are hermetic test-internal
// constants, not stubs for production ports. Promoting them to the
// helpers surface would amplify a non-port surface for one test
// (Test 14), which is the wrong side of the SSOT tradeoff.
package voiceover

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 14: FASE 2 audio format validation — mp3/wav header detection
// ─────────────────────────────────────────────────────────────────────────

// mp3SyncPatterns are the valid MPEG audio frame sync bytes for Layer III.
// The first byte must be 0xFF and the second byte's top 3 bits must be
// 111 (0xE0 mask). Common values: 0xFF 0xFB (MPEG1 Layer3), 0xFF 0xF3
// (MPEG2 Layer3), 0xFF 0xF2 (MPEG2.5 Layer3).
var mp3SyncPatterns = [][]byte{
	{0xFF, 0xFB},
	{0xFF, 0xF3},
	{0xFF, 0xF2},
}

// isValidAudioFormat reads the first bytes of a file and checks for
// MP3 frame sync or WAV RIFF header. Returns nil on valid audio,
// a descriptive error otherwise.
func isValidAudioFormat(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open audio file: %w", err)
	}
	defer f.Close()

	// Read first 12 bytes to cover both MP3 (2 bytes) and WAV (12 bytes).
	buf := make([]byte, 12)
	n, err := f.Read(buf)
	if err != nil || n < 2 {
		return fmt.Errorf("audio file too short (%d bytes): %w", n, err)
	}

	// MP3 check: first byte 0xFF, second byte top 3 bits = 111 (0xE0 mask).
	if buf[0] == 0xFF && (buf[1]&0xE0) == 0xE0 {
		return nil
	}

	// WAV check: first 4 bytes = "RIFF", bytes 8-11 = "WAVE".
	if n >= 12 && string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WAVE" {
		return nil
	}

	return fmt.Errorf("not a valid audio format: first 2 bytes = %02X %02X", buf[0], buf[1])
}

// TestIsValidAudioFormat_MP3FrameSync pinc the FASE 2 contract #1 extension:
// the isValidAudioFormat helper must correctly detect MP3 files (frame sync
// pattern 0xFF 0xFB/0xF3/0xF2) and WAV files (RIFF header). Rejects empty
// files, non-audio bytes, and missing files.
//
// godlike/07 NO-FAKE-AVAILABILITY: each sub-test writes a real temp file
// with the advertised bytes — the helper reads real disk bytes, not
// stub strings.
func TestIsValidAudioFormat_MP3FrameSync(t *testing.T) {
	tests := []struct {
		name    string
		bytes   []byte
		wantErr bool
		errText string
	}{
		{
			name:    "MPEG1 Layer3 (0xFF 0xFB)",
			bytes:   []byte{0xFF, 0xFB, 0x90, 0x00}, // minimal valid frame header
			wantErr: false,
		},
		{
			name:    "MPEG2 Layer3 (0xFF 0xF3)",
			bytes:   []byte{0xFF, 0xF3, 0x90, 0x00},
			wantErr: false,
		},
		{
			name:    "MPEG2.5 Layer3 (0xFF 0xF2)",
			bytes:   []byte{0xFF, 0xF2, 0x90, 0x00},
			wantErr: false,
		},
		{
			name:    "WAV RIFF header",
			bytes:   append([]byte("RIFF\x00\x00\x00\x00WAVE"), 0x00),
			wantErr: false,
		},
		{
			name:    "empty file (too short)",
			bytes:   []byte{},
			wantErr: true,
			errText: "too short",
		},
		{
			name:    "HTML error page (non-audio bytes)",
			bytes:   []byte("<!DOCTYPE html>\n"),
			wantErr: true,
			errText: "not a valid audio format",
		},
		{
			name:    "missing file",
			bytes:   nil, // special case: don't create the file
			wantErr: true,
			errText: "cannot open",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.bytes != nil {
				f, err := os.CreateTemp(t.TempDir(), "audio-test-*")
				require.NoError(t, err)
				_, err = f.Write(tt.bytes)
				require.NoError(t, err)
				require.NoError(t, f.Close())
				path = f.Name()
			} else {
				path = "/nonexistent/path/for/audio/validation/test"
			}

			err := isValidAudioFormat(path)
			if tt.wantErr {
				require.Error(t, err, "FASE 2 contract: invalid audio must return error")
				if tt.errText != "" {
					assert.Contains(t, err.Error(), tt.errText)
				}
			} else {
				assert.NoError(t, err, "FASE 2 contract: valid audio must return nil error")
			}
		})
	}

	// Cross-reference: canonical MP3 sync patterns must match the spec.
	for i, pat := range mp3SyncPatterns {
		assert.Equal(t, byte(0xFF), pat[0],
			"mp3SyncPatterns[%d] first byte must be 0xFF (MPEG frame sync)", i)
		assert.True(t, (pat[1]&0xE0) == 0xE0,
			"mp3SyncPatterns[%d] second byte top 3 bits must be 111 (0xE0 mask)", i)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 15: FASE 2 — Stage 3 Publisher with empty Project propagates correctly
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyProject
// pins the FASE 2 contract #5: when ProcessSegmentCommand carries an
// empty Project, the VoiceoverPublishCommand must forward the empty
// value verbatim. The Publisher adapter then routes empty Project →
// no ProjectID in the PublishRequest (semantic-first path).
//
// This is the companion to Test 9 which tests non-empty Project.
// Together they lock the full propagation contract.
