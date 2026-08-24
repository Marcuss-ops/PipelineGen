package voiceover

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// ProcessSegmentCommand is the canonical input for one voiceover segment.
type ProcessSegmentCommand struct {
	JobID, ID, RequestID string
	TextHash             TextHash
	Text                 string
	Language             Language
	Voice, Filename      string
	Strategy             string
	Metadata             map[string]any
	RemoveSilence        bool
	Timing               *audio.TimingRequest
	Moments              []audio.MomentQuery
	Project              string
	Dest                 *ResolvedDestination
	ShouldSwap           bool
	OldDriveFileID       string
	OldLocalPath         string
	OldCleanedPath       string
}

type VoiceoverCacheLookup interface {
	Lookup(ctx context.Context, fingerprint string, timingRequired bool) (*VoiceoverCacheHit, error)
}

type AsyncPublishPool interface {
	Submit(ctx context.Context, fn func())
	Wait()
}

type VoiceoverCacheHit struct {
	ID, Voice, Filename     string
	DriveFileID, DriveLink  string
	DownloadLink, LocalPath string
	CleanedPath             string
	DurationMs              int64
	LegacyFileMD5           string
	MetaJSON                []byte
}

type ProcessSegmentCacheDeps struct {
	VoiceoverCache VoiceoverCacheLookup
	AsyncPublish   AsyncPublishPool
}

type ProcessSegmentDeps struct {
	TTSProvider         TTSProvider
	AudioPostProcessor  AudioPostProcessor
	Publisher           VoiceoverPublisher
	VoiceoverRepository persistence.Repository
	Finalizer           VoiceoverFinalizer
	TxOutboxEnqueuer    TxOutboxEnqueuer
	SemanticTagger      SemanticTaggerFunc
	Cache               ProcessSegmentCacheDeps
	Logger              *zap.Logger
}

type ProcessSegmentUseCase struct{ deps ProcessSegmentDeps }

func NewProcessSegmentUseCase(deps ProcessSegmentDeps) *ProcessSegmentUseCase {
	if deps.TTSProvider == nil {
		panic("voiceover.NewProcessSegmentUseCase: TTSProvider is required")
	}
	if deps.Publisher == nil {
		panic("voiceover.NewProcessSegmentUseCase: Publisher is required")
	}
	if deps.VoiceoverRepository == nil {
		panic("voiceover.NewProcessSegmentUseCase: VoiceoverRepository is required")
	}
	if deps.Finalizer == nil {
		panic("voiceover.NewProcessSegmentUseCase: Finalizer is required (P0.4 Fase 3a — unified finalization port)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ProcessSegmentUseCase{deps: deps}
}
