// Package usecase — process_segment_require_transcript_ready_test.go:
// focused unit test for PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5
// (July 2026) wiring. Verifies that
// ProcessSegmentDeps.RequireTranscriptReady flows to
// CommitLocalizedClipCommand.RequireTranscriptReady on the
// LocalizedClipWriter port — the canonical end-to-end
// regression guard for the policy gate.
//
// godlike/06 SSOT: this test is the SOLE canonical regression
// guard for the Fase 5 wiring. A future refactor that silently
// drops the field from the struct literal in
// process_segment_step6to9.go (e.g. a "simplification" that
// hardcodes `false` again) will surface as a test failure here
// — not as missing clips in production.
//
// godlike/07 minimum-blast-radius: the test calls
// step6to9_SubtitlesDriveWriter directly (package-internal
// method) with a minimal command + nil resolver + nil
// DriveFolderMgr, so Steps 6 and 8 are skipped and only Step 9
// (the LocalizedWriter call) exercises the wiring. No
// YouTube-segment cut / hash / Drive upload is required.
package usecase

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fase5StubVideoPipeline satisfies youtubeports.VideoPipelinePort
// as a no-op. Required by ProcessSegmentDeps.Validate() (which
// panics on nil VideoPipeline — it's a required port, not
// optional). Named fase5StubVideoPipeline (not stubVideoPipeline)
// to avoid a redeclaration collision with the existing
// stubVideoPipeline in process_segment_failfast_test.go. The
// single method DownloadAndCutYouTubeVideo is never called in
// this test (Step 3-5 are skipped because we invoke step6to9
// directly).
type fase5StubVideoPipeline struct{}

func (fase5StubVideoPipeline) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	return nil, nil
}

// fase5RecordingWriter satisfies localized.LocalizedClipWriter
// and records the last command received. The test asserts on
// the recorded command's RequireTranscriptReady field. Named
// fase5RecordingWriter (not recordingLocalizedWriter) to avoid
// collisions with any future recording stubs in other test
// files.
type fase5RecordingWriter struct {
	last   localized.CommitLocalizedClipCommand
	called int
}

func (r *fase5RecordingWriter) CommitClipTextAndIndexEvent(_ context.Context, cmd localized.CommitLocalizedClipCommand) error {
	r.last = cmd
	r.called++
	return nil
}

// TestStep6to9_RequireTranscriptReady_FlowsToWriterCommand is the
// canonical regression guard for PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 5 (July 2026) wiring. It asserts:
//
//  1. When ProcessSegmentDeps.RequireTranscriptReady == true, the
//     CommitLocalizedClipCommand passed to LocalizedClipWriter
//     has RequireTranscriptReady == true.
//  2. When ProcessSegmentDeps.RequireTranscriptReady == false,
//     the command has RequireTranscriptReady == false (the
//     canonical post-Fase-1.c default).
//
// The test exercises step6to9_SubtitlesDriveWriter directly
// (package-internal) with a nil TextTrackResolver and a nil
// DriveFolderMgr so Steps 6 and 8 are skipped — only Step 9
// (the LocalizedWriter call) exercises the wiring under test.
func TestStep6to9_RequireTranscriptReady_FlowsToWriterCommand(t *testing.T) {
	t.Run("TrueFlag_PropagatesToWriterCommand", func(t *testing.T) {
		writer := &fase5RecordingWriter{}
		u := NewProcessYouTubeSegmentFromSubBundles(
			ProcessSegmentCoreDeps{
				Cache:         testStubClipCache{},
				VideoPipeline: fase5StubVideoPipeline{}, // required by Validate()
				Hash:          testStubHash{},
				Writer:        testStubClipAtomicWriter{},
				SegmentsSvc:   NewSegmentsService(),
				SegmentPolicy: youtubetypes.DefaultSegmentPolicy(),
				Log:           zap.NewNop(),
			},
			ProcessSegmentMediaDeps{
				// nil → Step 6 skipped
				// nil → Step 8 skipped
			},
			ProcessSegmentMetadataDeps{
				LocalizedWriter: writer,
			},
			ProcessSegmentObservabilityDeps{
				RequireTranscriptReady: true, // <-- the field under test
			},
		)

		cmd := youtubetypes.ProcessSegmentCommand{
			VideoID:         "vid-fase5-test",
			VideoURL:        "https://www.youtube.com/watch?v=vid-fase5-test",
			Segment:         youtubetypes.Segment{Name: "fase5-true"},
			DriveFolderID:   "", // empty → Step 8 skipped even if DriveFolderMgr is non-nil
			DriveFolderPath: "",
		}
		// out.Item must carry the fields buildClipAsset reads:
		// LocalPath, DriveFileID, DriveLink, StartSeconds,
		// EndSeconds, Duration. Missing fields cause a panic
		// at Step 9 before reaching the writer assertion.
		out := youtubetypes.ProcessSegmentResult{
			Item: youtubetypes.ExtractItem{
				Name:         "fase5-true",
				Start:        "0",
				End:          "5",
				StartSeconds: 0,
				EndSeconds:   5,
				Duration:     5,
				LocalPath:    "/tmp/fase5-true.mp4",
				DriveFileID:  "drive-file-id-fase5-true",
				DriveLink:    "https://drive.google.com/file/d/drive-file-id-fase5-true/view",
				Filename:     "fase5-true.mp4",
			},
		}

		_, err := u.step6to9_SubtitlesDriveWriter(
			context.Background(), cmd, &out,
			"yt_vid-fase5-test_0_5_v1", 0, 5,
			"/tmp/fase5-true.mp4", "stubhash", "v1",
		)
		// err is nil: the stub writer always returns nil; the
		// RequireTranscriptReady gate is evaluated INSIDE the
		// concrete *ClipAtomicWriterAdapter (production), not in
		// the stub. This test verifies the FIELD FLOWS to the
		// command — the production writer's pre-tx check is
		// exercised in the clip_atomic_writer test suite
		// (TestErrClipLocaleNotReady_*).
		require.NoError(t, err, "stub writer should not error")
		require.Equal(t, 1, writer.called,
			"LocalizedWriter must be called exactly once")
		assert.True(t, writer.last.RequireTranscriptReady,
			"ProcessSegmentDeps.RequireTranscriptReady=true MUST propagate to CommitLocalizedClipCommand.RequireTranscriptReady=true")
		// Sanity: the other super-tx fields are populated.
		assert.NotEmpty(t, writer.last.Clip.ID,
			"super-tx must carry the clip ID")
		assert.Equal(t, writer.last.Clip.ID, writer.last.IndexEvent.AggregateID,
			"super-tx must carry the committed clip aggregate ID")
	})

	t.Run("FalseFlag_PropagatesToWriterCommand", func(t *testing.T) {
		writer := &fase5RecordingWriter{}
		u := NewProcessYouTubeSegmentFromSubBundles(
			ProcessSegmentCoreDeps{
				Cache:         testStubClipCache{},
				VideoPipeline: fase5StubVideoPipeline{},
				Hash:          testStubHash{},
				Writer:        testStubClipAtomicWriter{},
				SegmentsSvc:   NewSegmentsService(),
				SegmentPolicy: youtubetypes.DefaultSegmentPolicy(),
				Log:           zap.NewNop(),
			},
			ProcessSegmentMediaDeps{},
			ProcessSegmentMetadataDeps{
				LocalizedWriter: writer,
			},
			ProcessSegmentObservabilityDeps{
				RequireTranscriptReady: false, // <-- the canonical post-Fase-1.c default
			},
		)

		cmd := youtubetypes.ProcessSegmentCommand{
			VideoID:  "vid-fase5-false",
			VideoURL: "https://www.youtube.com/watch?v=vid-fase5-false",
			Segment:  youtubetypes.Segment{Name: "fase5-false"},
		}
		// out.Item must carry the fields buildClipAsset reads.
		out := youtubetypes.ProcessSegmentResult{
			Item: youtubetypes.ExtractItem{
				Name:         "fase5-false",
				Start:        "0",
				End:          "5",
				StartSeconds: 0,
				EndSeconds:   5,
				Duration:     5,
				LocalPath:    "/tmp/fase5-false.mp4",
				DriveFileID:  "drive-file-id-fase5-false",
				DriveLink:    "https://drive.google.com/file/d/drive-file-id-fase5-false/view",
				Filename:     "fase5-false.mp4",
			},
		}

		_, err := u.step6to9_SubtitlesDriveWriter(
			context.Background(), cmd, &out,
			"yt_vid-fase5-false_0_5_v1", 0, 5,
			"/tmp/fase5-false.mp4", "stubhash", "v1",
		)
		require.NoError(t, err)
		require.Equal(t, 1, writer.called)
		assert.False(t, writer.last.RequireTranscriptReady,
			"ProcessSegmentDeps.RequireTranscriptReady=false MUST propagate to CommitLocalizedClipCommand.RequireTranscriptReady=false (canonical post-Fase-1.c default)")
	})
}
