// Package jobs — timing_negative_e2e_test.go (PR-VOICEOVER-TIMING-NEG-E2E).
//
// Job-level negative E2E for the voiceover timing policy. It drives the
// canonical child handler GenerateItemJobHandler.HandleJob through the
// REAL ProcessVoiceoverItemUseCase (TTS → publish → timing → finalize) with
// a TTS stub that returns audio but ZERO word boundaries — the exact
// failure mode the plan calls "Edge returns audio, zero WordBoundary":
//
//	voiceover_timing.mode=required   → JOB FAILED, error code
//	                                   VOICEOVER_TIMING_UNAVAILABLE.
//	voiceover_timing.mode=best_effort → JOB SUCCEEDED (audio completes),
//	                                   timing.status=unavailable surfaced
//	                                   in the job result map.
//
// godlike/07 NO-FAKE-AVAILABILITY: a best-effort item with no boundaries
// must never pretend the timing exists (timing.status stays "unavailable"),
// and a required item must never pretend the job succeeded. There is
// exactly ONE TTS synthesis per job and zero transcription passes.
package jobs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// noBoundaryTTS synthesizes audio with ZERO word boundaries in one pass.
// It models the negative-E2E provider contract: audio chunks arrive, but
// the WordBoundary stream is empty.
type noBoundaryTTS struct {
	dir   string
	synth int
}

func (s *noBoundaryTTS) Synthesize(_ context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	s.synth++
	path := filepath.Join(s.dir, "audio.mp3")
	_ = os.WriteFile(path, []byte("fake-mp3-bytes"), 0o644)
	return voiceover.TTSOutput{
		LocalPath:      path,
		Voice:          in.Voice,
		LegacyFileMD5:  "no-boundary-hash",
		Provider:       "edge_tts",
		BoundaryMode:   audio.BoundaryWord,
		Duration:       2 * time.Second,
		WordBoundaries: nil, // KEY: audio present, zero boundaries
	}, nil
}

var _ voiceover.TTSProvider = (*noBoundaryTTS)(nil)

type e2eDestResolver struct {
	folderID   string
	folderPath string
}

func (r *e2eDestResolver) Resolve(_ context.Context, _ *voiceover.DestinationRequest) (*voiceover.ResolvedDestination, error) {
	return &voiceover.ResolvedDestination{FolderID: r.folderID, FolderPath: r.folderPath}, nil
}

var _ voiceover.DestinationResolver = (*e2eDestResolver)(nil)

type e2ePublisher struct {
	fileID    string
	published []voiceover.VoiceoverPublishCommand
}

func (p *e2ePublisher) Publish(_ context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	p.published = append(p.published, cmd)
	return p.fileID, nil
}

var _ voiceover.VoiceoverPublisher = (*e2ePublisher)(nil)

// e2eVoRepo satisfies persistence.Repository. The per-item pipeline only
// calls BeginTx (the finalizer owns the INSERT/outbox inside the tx), so
// the remaining methods are no-op stubs.
type e2eVoRepo struct{ db *sql.DB }

func (r *e2eVoRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

func (r *e2eVoRepo) InsertTx(context.Context, *sql.Tx, *persistence.VoiceoverRecord) error {
	return nil
}

func (r *e2eVoRepo) DeleteByIDTx(context.Context, *sql.Tx, string) error {
	return nil
}

func (r *e2eVoRepo) PreReadByID(context.Context, string) (*persistence.VoiceoverRecord, error) {
	return nil, nil
}

func (r *e2eVoRepo) CountByDriveFileIDTx(context.Context, *sql.Tx, string, string) (string, int, error) {
	return "", 0, nil
}

func (r *e2eVoRepo) FindByIdempotencyKeyTx(context.Context, *sql.Tx, string) (string, error) {
	return "", sql.ErrNoRows
}

var _ persistence.Repository = (*e2eVoRepo)(nil)

type e2eFinalizer struct{ id string }

func (f *e2eFinalizer) Finalize(_ context.Context, _ *sql.Tx, _ *voiceover.FinalizeCommand) (*voiceover.FinalizeResult, error) {
	return &voiceover.FinalizeResult{ID: f.id}, nil
}

var _ voiceover.VoiceoverFinalizer = (*e2eFinalizer)(nil)

// newTimingJobHandler wires the REAL ProcessVoiceoverItemUseCase behind the
// canonical child handler with stub ports, so the test exercises the full
// TTS → publish → timing → finalize pipeline (not just handler mapping).
func newTimingJobHandler(t *testing.T, tts voiceover.TTSProvider, pub *e2ePublisher) *GenerateItemJobHandler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	uc := voiceover.NewProcessVoiceoverItemUseCase(voiceover.ProcessVoiceoverItemDeps{
		Pipeline: voiceover.ProcessVoiceoverPipelineDeps{
			TTSProvider:         tts,
			DestinationResolver: &e2eDestResolver{folderID: "folder-e2e", folderPath: t.TempDir()},
			Publisher:           pub,
			VoiceoverRepository: &e2eVoRepo{db: db},
		},
		Finalize: voiceover.ProcessVoiceoverFinalizeDeps{
			Finalizer: &e2eFinalizer{id: "vo-e2e"},
		},
		Logger: zap.NewNop(),
	})
	return NewGenerateItemJobHandler(uc, zap.NewNop())
}

// timingItemCmd builds a fully-populated child command with the given
// timing mode. The exact shape mirrors FanoutVoiceoversUseCase's per-item
// construction (with Timing threaded from the parent batch policy).
func timingItemCmd(mode audio.TimingMode) *voiceover.GenerateVoiceoverItemCommand {
	return &voiceover.GenerateVoiceoverItemCommand{
		ParentJobID: "parent-timing-neg",
		RequestID:   "vo_timing_neg",
		Text:        "Garibaldi incontra re Vittorio Emanuele II.",
		TextHash:    "timing-neg-hash",
		Language:    "it",
		Voice:       "it-IT-DiegoNeural",
		Filename:    "timing_neg_it.mp3",
		Destination: &voiceover.DestinationRequest{FolderID: "folder-e2e"},
		Strategy:    "verify",
		Timing: &audio.TimingRequest{
			Mode:         mode,
			BoundaryMode: audio.BoundaryWord,
			Formats:      []audio.TimingFormat{audio.TimingJSON},
		},
	}
}

// TestTimingJob_Required_NoBoundaries_FailsJob pins the required-timing
// fail-closed contract at the job boundary: a provider that returns audio
// but zero word boundaries MUST fail the voiceover.generate_item job with
// the typed VOICEOVER_TIMING_UNAVAILABLE code — never a fake SUCCEEDED.
func TestTimingJob_Required_NoBoundaries_FailsJob(t *testing.T) {
	tts := &noBoundaryTTS{dir: t.TempDir()}
	pub := &e2ePublisher{fileID: "drive-e2e"}
	h := newTimingJobHandler(t, tts, pub)

	item := timingItemCmd(audio.TimingRequired)
	j := &appjobs.Job{ID: "child-required-nb", Payload: marshalItemCmd(t, item)}
	resultMap, err := h.HandleJob(context.Background(), j, &appjobs.JobTools{Progress: func(int, string) {}})

	require.Error(t, err, "required timing + no boundaries must FAIL the job")
	assert.Contains(t, err.Error(), "VOICEOVER_TIMING_UNAVAILABLE")
	assert.Equal(t, voiceover.StatusFailed, resultMap["status"])
	assert.Equal(t, "VOICEOVER_TIMING_UNAVAILABLE", resultMap["error_code"])
	ok, hasOK := resultMap["ok"].(bool)
	assert.True(t, hasOK)
	assert.False(t, ok, "required timing failure must surface ok=false")

	// Exactly one synthesis, zero transcription passes. The audio upload
	// already happened (Stage 3) before the timing bundle failed closed.
	assert.Equal(t, 1, tts.synth, "exactly one TTS synthesis; zero Whisper/transcription")
	require.Len(t, pub.published, 1, "audio is uploaded before the timing failure")
	assert.Equal(t, "timing_neg_it.mp3", pub.published[0].Filename)
}

// TestTimingJob_BestEffort_NoBoundaries_SucceedsUnavailable pins the
// best-effort contract at the job boundary: the job SUCCEEDS (audio is
// completed) but the timing bundle is explicitly surfaced as
// status=unavailable — never silently dropped, never fabricated.
func TestTimingJob_BestEffort_NoBoundaries_SucceedsUnavailable(t *testing.T) {
	tts := &noBoundaryTTS{dir: t.TempDir()}
	pub := &e2ePublisher{fileID: "drive-e2e"}
	h := newTimingJobHandler(t, tts, pub)

	item := timingItemCmd(audio.TimingBestEffort)
	j := &appjobs.Job{ID: "child-best-effort-nb", Payload: marshalItemCmd(t, item)}
	resultMap, err := h.HandleJob(context.Background(), j, &appjobs.JobTools{Progress: func(int, string) {}})

	require.NoError(t, err, "best-effort timing + no boundaries must SUCCEED the job (audio completes)")
	assert.Equal(t, voiceover.StatusCompleted, resultMap["status"])
	ok, hasOK := resultMap["ok"].(bool)
	assert.True(t, hasOK)
	assert.True(t, ok, "best-effort audio must surface ok=true")

	timing, hasTiming := resultMap["timing"].(map[string]any)
	require.True(t, hasTiming, "job result must surface the timing bundle")
	assert.Equal(t, "unavailable", timing["status"],
		"best-effort + no boundaries must surface timing.status=unavailable, not completed")

	// Exactly one synthesis, zero transcription passes; only the audio
	// file is published (no timing.json/srt/vtt for zero boundaries).
	assert.Equal(t, 1, tts.synth, "exactly one TTS synthesis; zero Whisper/transcription")
	require.Len(t, pub.published, 1, "only the audio is published when no boundaries exist")
	assert.Equal(t, "timing_neg_it.mp3", pub.published[0].Filename)
}
