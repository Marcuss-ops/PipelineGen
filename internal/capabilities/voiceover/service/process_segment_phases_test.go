// Package voiceover — BON-08 phase tests.
//
// These tests pin the pure decisions and builders extracted out of
// ProcessSegmentUseCase.Execute so a future change cannot silently alter
// cache reuse, cleaned-duration selection, publish failure mapping or
// async eligibility while the orchestration still compiles.
package voiceover

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

type phaseTestCache struct {
	hit              *VoiceoverCacheHit
	err              error
	calls            int
	timingRequiredIn bool
}

func (c *phaseTestCache) Lookup(_ context.Context, _ string, timingRequired bool) (*VoiceoverCacheHit, error) {
	c.calls++
	c.timingRequiredIn = timingRequired
	return c.hit, c.err
}

type phaseTestPool struct {
	submitted int
	jobs      []func()
}

func (p *phaseTestPool) Submit(_ context.Context, fn func()) {
	p.submitted++
	p.jobs = append(p.jobs, fn)
}
func (p *phaseTestPool) Wait() {}

type phaseTestPublisher struct{}

func (phaseTestPublisher) Publish(_ context.Context, _ VoiceoverPublishCommand) (string, error) {
	return "drive-file-id", nil
}

type phaseTestFailPublisher struct{ err error }

func (p phaseTestFailPublisher) Publish(_ context.Context, _ VoiceoverPublishCommand) (string, error) {
	return "", p.err
}

func newPhaseTestUseCase(t *testing.T, cache VoiceoverCacheLookup) *ProcessSegmentUseCase {
	t.Helper()
	return newPhaseUseCase(t, func(deps *ProcessSegmentDeps) {
		deps.Cache.VoiceoverCache = cache
	})
}

func newPhaseUseCase(t *testing.T, mutate func(*ProcessSegmentDeps)) *ProcessSegmentUseCase {
	t.Helper()
	deps := ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{},
		VoiceoverRepository: &stubProcessVoRepo{db: openProcessTestDB(t)},
		Finalizer:           &stubProcessFinalizer{},
		Logger:              zap.NewNop(),
	}
	if mutate != nil {
		mutate(&deps)
	}
	return NewProcessSegmentUseCase(deps)
}

func TestTimingPolicyFor(t *testing.T) {
	explicit := &audio.TimingRequest{
		Mode:         audio.TimingRequired,
		BoundaryMode: audio.BoundaryWord,
		Formats:      []audio.TimingFormat{audio.TimingJSON, audio.TimingSRT},
	}

	assert.Equal(t, audio.DefaultTimingRequest(), timingPolicyFor(nil))
	assert.Equal(t, audio.DefaultTimingRequest(), timingPolicyFor(&ProcessSegmentCommand{}))
	assert.Equal(t, explicit.Normalized(), timingPolicyFor(&ProcessSegmentCommand{Timing: explicit}))
}

func TestCacheHitReusable(t *testing.T) {
	artifact := &audio.SpeechTimingArtifact{}
	tests := []struct {
		name     string
		policy   audio.TimingRequest
		hit      *VoiceoverCacheHit
		expected bool
	}{
		{name: "nil hit", policy: audio.DefaultTimingRequest(), expected: false},
		{name: "timing disabled reuses audio-only hit", policy: audio.TimingRequest{Mode: audio.TimingDisabled}, hit: &VoiceoverCacheHit{}, expected: true},
		{name: "timing required rejects missing artifact", policy: audio.TimingRequest{Mode: audio.TimingRequired}, hit: &VoiceoverCacheHit{}, expected: false},
		{name: "timing required accepts hydrated artifact", policy: audio.TimingRequest{Mode: audio.TimingRequired}, hit: &VoiceoverCacheHit{Artifact: artifact}, expected: true},
		{name: "best effort accepts hydrated artifact", policy: audio.TimingRequest{Mode: audio.TimingBestEffort}, hit: &VoiceoverCacheHit{Artifact: artifact}, expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, cacheHitReusable(tt.policy, tt.hit))
		})
	}
}

func TestLookupCachedResult(t *testing.T) {
	ctx := context.Background()
	baseCmd := &ProcessSegmentCommand{
		Language: "it",
		Voice:    "it-IT-DiegoNeural",
		Dest:     &ResolvedDestination{FolderID: "folder-1", FolderPath: t.TempDir()},
	}

	t.Run("forced synthesis bypasses the cache", func(t *testing.T) {
		cache := &phaseTestCache{}
		u := newPhaseTestUseCase(t, cache)
		cmd := *baseCmd
		cmd.ForceSynthesis = true
		_, hit := u.lookupCachedResult(ctx, &cmd, audio.DefaultTimingRequest(), zap.NewNop())
		assert.False(t, hit)
		assert.Zero(t, cache.calls)
	})

	t.Run("no cache port is a miss", func(t *testing.T) {
		u := newPhaseTestUseCase(t, nil)
		_, hit := u.lookupCachedResult(ctx, baseCmd, audio.DefaultTimingRequest(), zap.NewNop())
		assert.False(t, hit)
	})

	t.Run("lookup error falls through and requests timing hydration", func(t *testing.T) {
		cache := &phaseTestCache{err: errors.New("db busy")}
		u := newPhaseTestUseCase(t, cache)
		policy := audio.TimingRequest{Mode: audio.TimingRequired}
		_, hit := u.lookupCachedResult(ctx, baseCmd, policy, zap.NewNop())
		assert.False(t, hit)
		assert.Equal(t, 1, cache.calls)
		assert.True(t, cache.timingRequiredIn)
	})

	t.Run("timing candidate without artifact falls through", func(t *testing.T) {
		cache := &phaseTestCache{hit: &VoiceoverCacheHit{ID: "vo-old"}}
		u := newPhaseTestUseCase(t, cache)
		policy := audio.TimingRequest{Mode: audio.TimingRequired}
		_, hit := u.lookupCachedResult(ctx, baseCmd, policy, zap.NewNop())
		assert.False(t, hit)
		assert.Equal(t, 1, cache.calls)
	})

	t.Run("verified hit returns the cached result", func(t *testing.T) {
		cache := &phaseTestCache{hit: &VoiceoverCacheHit{
			ID:          "vo-cached",
			Voice:       "it-IT-DiegoNeural",
			Filename:    "scene-0-it.mp3",
			DriveFileID: "drive-1",
			DurationMs:  2400,
			Artifact:    &audio.SpeechTimingArtifact{},
		}}
		u := newPhaseTestUseCase(t, cache)
		policy := audio.TimingRequest{Mode: audio.TimingRequired}
		result, hit := u.lookupCachedResult(ctx, baseCmd, policy, zap.NewNop())
		require.True(t, hit)
		require.NotNil(t, result)
		assert.Equal(t, "vo-cached", result.ID)
		assert.True(t, result.CacheHit)
		assert.Equal(t, StatusCompleted, result.Status)
		assert.Equal(t, "drive-1", result.DriveFileID)
	})
}

func TestApplySynthesisOutput(t *testing.T) {
	out := &VoiceoverItemResult{Voice: "caller-voice"}
	applySynthesisOutput(out, TTSOutput{
		LocalPath:     "/tmp/raw.mp3",
		CleanedPath:   "/tmp/clean.mp3",
		Voice:         "provider-voice",
		ContentSHA256: "sha-1",
		Duration:      2500 * time.Millisecond,
	})
	assert.Equal(t, "/tmp/raw.mp3", out.LocalPath)
	assert.Equal(t, "/tmp/clean.mp3", out.CleanedPath)
	assert.Equal(t, "provider-voice", out.Voice)
	assert.Equal(t, "sha-1", out.ContentSHA256)
	assert.Equal(t, int64(2500), out.DurationMs)

	// An empty provider voice must not erase the caller's resolved voice.
	out = &VoiceoverItemResult{Voice: "caller-voice"}
	applySynthesisOutput(out, TTSOutput{Duration: time.Second})
	assert.Equal(t, "caller-voice", out.Voice)
}

func TestApplyAudioPostOutput(t *testing.T) {
	tts := TTSOutput{Duration: 5 * time.Second}

	t.Run("nil post leaves the result untouched", func(t *testing.T) {
		out := &VoiceoverItemResult{CleanedPath: "/tmp/raw.mp3", DurationMs: 5000}
		assert.Nil(t, applyAudioPostOutput(out, tts, nil))
		assert.Equal(t, "/tmp/raw.mp3", out.CleanedPath)
		assert.Equal(t, int64(5000), out.DurationMs)
	})

	t.Run("cleaned audio replaces path, hash and duration", func(t *testing.T) {
		out := &VoiceoverItemResult{
			CleanedPath:   "/tmp/raw.mp3",
			ContentSHA256: "raw-sha",
			DurationMs:    5000,
		}
		post := &AudioPostOutput{
			CleanedPath:   "/tmp/clean.mp3",
			ContentSHA256: "clean-sha",
			DurationUS:    4_000_001,
		}
		assert.Nil(t, applyAudioPostOutput(out, tts, post))
		assert.Equal(t, "/tmp/clean.mp3", out.CleanedPath)
		assert.Equal(t, "clean-sha", out.ContentSHA256)
		assert.Equal(t, int64(4001), out.DurationMs) // ceil to the next ms
	})

	t.Run("empty post fields preserve prior values", func(t *testing.T) {
		out := &VoiceoverItemResult{CleanedPath: "/tmp/raw.mp3", ContentSHA256: "raw-sha", DurationMs: 5000}
		applyAudioPostOutput(out, tts, &AudioPostOutput{})
		assert.Equal(t, "/tmp/raw.mp3", out.CleanedPath)
		assert.Equal(t, "raw-sha", out.ContentSHA256)
		assert.Equal(t, int64(5000), out.DurationMs)
	})

	t.Run("edits produce the silence cleanup report", func(t *testing.T) {
		out := &VoiceoverItemResult{}
		post := &AudioPostOutput{
			DurationUS: 4_000_000,
			EditMap: []audio.AudioEdit{
				{SourceStartUS: 0, SourceEndUS: 500_000},
				{SourceStartUS: 4_500_000, SourceEndUS: 5_000_000},
			},
		}
		report := applyAudioPostOutput(out, tts, post)
		require.NotNil(t, report)
		assert.Equal(t, int64(5_000_000), report.OriginalDurationUS)
		assert.Equal(t, int64(500_000), report.TrimStartUS)
		assert.Equal(t, int64(500_000), report.TrimEndUS)
		assert.Equal(t, int64(4_000_000), report.CleanDurationUS)
	})
}

func TestFinalDurationSeconds(t *testing.T) {
	tts := TTSOutput{Duration: 2500 * time.Millisecond}
	assert.InDelta(t, 2.5, finalDurationSeconds(tts, nil), 1e-9)
	assert.InDelta(t, 1.75, finalDurationSeconds(tts, &AudioPostOutput{DurationUS: 1_750_000}), 1e-9)
	// A post output without a measured duration cannot override the stream.
	assert.InDelta(t, 2.5, finalDurationSeconds(tts, &AudioPostOutput{}), 1e-9)
}

func TestBuildFinalizeCommand(t *testing.T) {
	cmd := &ProcessSegmentCommand{
		JobID:          "job-1",
		ID:             "vo-1",
		RequestID:      "req-1",
		TextHash:       "hash-1",
		Text:           "testo",
		Language:       "it",
		Filename:       "scene-0-it.mp3",
		Strategy:       "per_item",
		ShouldSwap:     true,
		OldDriveFileID: "old-drive",
		OldLocalPath:   "/tmp/old.mp3",
		OldCleanedPath: "/tmp/old-clean.mp3",
		Dest:           &ResolvedDestination{FolderID: "folder-1", FolderPath: "/tmp/out"},
	}
	out := &VoiceoverItemResult{
		Voice:         "it-IT-DiegoNeural",
		LocalPath:     "/tmp/raw.mp3",
		CleanedPath:   "/tmp/clean.mp3",
		ContentSHA256: "clean-sha",
		DriveFileID:   "drive-1",
		DriveLink:     "https://drive.example/1",
		DownloadLink:  "https://drive.example/1/download",
	}
	pub := &publishStageResult{MetaJSON: []byte(`{"k":"v"}`), IdemKey: "idem-1"}

	got := buildFinalizeCommand(cmd, out, pub, "fingerprint-1", 1.75)
	require.NotNil(t, got)
	assert.Equal(t, "fingerprint-1", got.Fingerprint)
	assert.Equal(t, "idem-1", got.IdempotencyKey)
	assert.Equal(t, "job-1", got.JobID)
	assert.Equal(t, "vo-1", got.ID)
	assert.Equal(t, "hash-1", got.TextHash)
	assert.Equal(t, Language("it"), got.Language)
	assert.Equal(t, "it-IT-DiegoNeural", got.Voice)
	assert.Equal(t, "/tmp/clean.mp3", got.CleanedPath)
	assert.Equal(t, "clean-sha", got.ContentSHA256)
	assert.Equal(t, 1.75, got.DurationSeconds)
	assert.Equal(t, "folder-1", got.FolderID)
	assert.Equal(t, "/tmp/out", got.FolderPath)
	assert.Equal(t, "drive-1", got.DriveFileID)
	assert.Equal(t, "old-drive", got.OldDriveFileID)
	assert.True(t, got.ShouldSwap)
	assert.Equal(t, pub.MetaJSON, got.MetaJSON)
}

func TestClassifyPublishFailure(t *testing.T) {
	t.Run("typed pipeline error is propagated unchanged", func(t *testing.T) {
		typed := newPipelineErrorCode(StageTiming, false, FailureTimingUnavailable, errors.New("no boundaries"))
		decision := classifyPublishFailure(typed)
		require.NotNil(t, decision.Pipeline)
		assert.Same(t, typed, decision.Pipeline)
		assert.Equal(t, string(FailureTimingUnavailable), decision.Code)
		assert.Equal(t, typed.Error(), decision.Message)
		assert.False(t, decision.DriveUploadFailure)
	})

	t.Run("destination parent mismatch is permanent", func(t *testing.T) {
		err := fmt.Errorf("upload: %w", delivery.ErrDestinationParentMismatch)
		decision := classifyPublishFailure(err)
		assert.Nil(t, decision.Pipeline)
		assert.Equal(t, VoiceoverDestinationMismatchCode, decision.Code)
		assert.Equal(t, StageUpload, decision.Stage)
		assert.False(t, decision.Retryable)
		assert.Equal(t, FailureDestinationMismatch, decision.FailureCode)
		assert.True(t, decision.DriveUploadFailure)
		assert.Contains(t, decision.Message, VoiceoverDestinationMismatchCode)
	})

	t.Run("generic publisher error is retryable", func(t *testing.T) {
		err := errors.New("network reset")
		decision := classifyPublishFailure(err)
		assert.Nil(t, decision.Pipeline)
		assert.Equal(t, string(FailureUpload), decision.Code)
		assert.Equal(t, StageUpload, decision.Stage)
		assert.True(t, decision.Retryable)
		assert.Equal(t, FailureUpload, decision.FailureCode)
		assert.True(t, decision.DriveUploadFailure)
		assert.Contains(t, decision.Message, "network reset")
	})
}

func TestShouldPublishAsync(t *testing.T) {
	pool := &phaseTestPool{}
	var publisher VoiceoverPublisher = phaseTestPublisher{}
	disabled := audio.TimingRequest{Mode: audio.TimingDisabled}
	required := audio.TimingRequest{Mode: audio.TimingRequired}

	assert.False(t, shouldPublishAsync(nil, publisher, disabled))
	assert.False(t, shouldPublishAsync(pool, nil, disabled))
	assert.False(t, shouldPublishAsync(pool, publisher, required))
	assert.True(t, shouldPublishAsync(pool, publisher, disabled))
}

func TestHasLocalPayload(t *testing.T) {
	assert.False(t, hasLocalPayload(nil))
	assert.False(t, hasLocalPayload(&VoiceoverItemResult{}))
	assert.True(t, hasLocalPayload(&VoiceoverItemResult{LocalPath: "/tmp/raw.mp3"}))
	assert.True(t, hasLocalPayload(&VoiceoverItemResult{CleanedPath: "/tmp/clean.mp3"}))
}

func TestPrepareSegment(t *testing.T) {
	t.Run("nil command is rejected", func(t *testing.T) {
		out, err := newPhaseTestUseCase(t, nil).prepareSegment(nil)
		assert.Nil(t, out)
		require.Error(t, err)
	})

	t.Run("local-only run accepts a missing Drive folder", func(t *testing.T) {
		out, err := newPhaseTestUseCase(t, nil).prepareSegment(&ProcessSegmentCommand{
			Language: "it",
			Dest:     &ResolvedDestination{FolderPath: "/tmp/out"},
		})
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, Language("it"), out.Language)
	})

	t.Run("delivery run fails closed on a missing Drive folder", func(t *testing.T) {
		u := newPhaseUseCase(t, func(deps *ProcessSegmentDeps) {
			deps.Publisher = phaseTestPublisher{}
		})
		out, err := u.prepareSegment(&ProcessSegmentCommand{
			Language: "it",
			Dest:     &ResolvedDestination{FolderPath: "/tmp/out"},
		})
		require.Error(t, err)
		require.NotNil(t, out)
		assert.Equal(t, StatusFailed, out.Status)
		assert.Equal(t, string(FailureMissingFolder), out.ErrorCode)
		assert.Contains(t, out.Error, "missing_folder_id")
		var pipelineErr *PipelineError
		require.ErrorAs(t, err, &pipelineErr)
		assert.Equal(t, StageDestinationResolve, pipelineErr.Stage)
		assert.False(t, pipelineErr.Retryable)
		assert.Equal(t, FailureMissingFolder, pipelineErr.FailureCode())
	})
}

func TestFailSegment(t *testing.T) {
	out := &VoiceoverItemResult{}
	cmd := &ProcessSegmentCommand{Language: "it", JobID: "job-1"}
	cause := errors.New("provider down")

	pipelineErr := failSegment(out, cmd, StageTTS, true, FailureTTS, cause)

	require.NotNil(t, pipelineErr)
	assert.Equal(t, string(FailureTTS), out.ErrorCode)
	assert.Equal(t, "tts_failed: provider down", out.Error)
	assert.NotEmpty(t, out.StageProgress)
	assert.Equal(t, StageTTS, pipelineErr.Stage)
	assert.True(t, pipelineErr.Retryable)
	assert.Equal(t, FailureTTS, pipelineErr.FailureCode())
	assert.ErrorIs(t, pipelineErr, cause)
}

func TestPublishAndClassify(t *testing.T) {
	baseCmd := &ProcessSegmentCommand{
		Language: "it",
		Filename: "scene-0-it.mp3",
		Timing:   &audio.TimingRequest{Mode: audio.TimingDisabled},
		Dest:     &ResolvedDestination{FolderID: "folder-1", FolderPath: t.TempDir()},
	}

	t.Run("raw publisher error is classified as a retryable upload failure", func(t *testing.T) {
		u := newPhaseUseCase(t, func(deps *ProcessSegmentDeps) {
			deps.Publisher = phaseTestFailPublisher{err: errors.New("network reset")}
		})
		out := &VoiceoverItemResult{LocalPath: "/tmp/raw.mp3"}
		_, err := u.publishAndClassify(context.Background(), baseCmd, out, &TTSOutput{}, nil, zap.NewNop())
		require.Error(t, err)
		assert.Equal(t, string(FailureUpload), out.ErrorCode)
		var pipelineErr *PipelineError
		require.ErrorAs(t, err, &pipelineErr)
		assert.Equal(t, StageUpload, pipelineErr.Stage)
		assert.True(t, pipelineErr.Retryable)
		assert.Equal(t, FailureUpload, pipelineErr.FailureCode())
	})

	t.Run("destination parent mismatch is permanent", func(t *testing.T) {
		u := newPhaseUseCase(t, func(deps *ProcessSegmentDeps) {
			deps.Publisher = phaseTestFailPublisher{err: fmt.Errorf("parent: %w", delivery.ErrDestinationParentMismatch)}
		})
		out := &VoiceoverItemResult{LocalPath: "/tmp/raw.mp3"}
		_, err := u.publishAndClassify(context.Background(), baseCmd, out, &TTSOutput{}, nil, zap.NewNop())
		require.Error(t, err)
		assert.Equal(t, VoiceoverDestinationMismatchCode, out.ErrorCode)
		var pipelineErr *PipelineError
		require.ErrorAs(t, err, &pipelineErr)
		assert.Equal(t, StageUpload, pipelineErr.Stage)
		assert.False(t, pipelineErr.Retryable)
		assert.Equal(t, FailureDestinationMismatch, pipelineErr.FailureCode())
	})
}

func TestStartAsyncPublish(t *testing.T) {
	pool := &phaseTestPool{}
	u := newPhaseUseCase(t, func(deps *ProcessSegmentDeps) {
		deps.Publisher = phaseTestPublisher{}
		deps.Cache.AsyncPublish = pool
	})
	cmd := &ProcessSegmentCommand{
		ID:       "vo-1",
		Language: "it",
		Dest:     &ResolvedDestination{FolderID: "folder-1", FolderPath: "/tmp/out"},
	}
	out := &VoiceoverItemResult{Language: "it", DurationMs: 2500}

	u.startAsyncPublish(context.Background(), cmd, out, TTSOutput{}, nil, zap.NewNop())

	assert.Equal(t, StatusGenerated, out.Status)
	assert.Equal(t, 1, pool.submitted)
	require.Len(t, pool.jobs, 1)
	assert.NotEmpty(t, out.StageProgress)
}
