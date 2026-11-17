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
	TTSRate              string
	SilencePolicy        audio.SilenceRemovalPolicy
	Strategy             string
	Metadata             map[string]any
	RemoveSilence        bool
	ForceSynthesis       bool
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
	ContentSHA256           string
	MetaJSON                []byte
	Artifact                *audio.SpeechTimingArtifact
}

type ProcessSegmentCacheDeps struct {
	VoiceoverCache VoiceoverCacheLookup
	AsyncPublish   AsyncPublishPool
}

// ProcessSegmentEnrichmentDeps groups the optional analytic ports used to
// enrich a finished segment: the semantic tagger and the Rust phrase aligner.
// They were top-level ProcessSegmentDeps fields until the struct hit the
// 8-mandatory-port gate; grouping them keeps the dependency bag readable
// without making either port mandatory (both stay nil-safe per call site).
type ProcessSegmentEnrichmentDeps struct {
	SemanticTagger SemanticTaggerFunc
	PhraseAligner  audio.PhraseAligner
}

type ProcessSegmentDeps struct {
	TTSProvider        TTSProvider
	AudioPostProcessor AudioPostProcessor
	// Publisher is the OPTIONAL delivery port (Drive upload). When nil the
	// pipeline is LOCAL-ONLY: per-scene audio + timing artifacts are
	// certified and persisted next to the audio, and NOTHING is uploaded.
	// The video pipeline wires no publisher (waste-cut 2026-10-24: per-scene
	// TTS/timing are intermediates, not delivery products); the explicit
	// delivery surfaces (batch GenerateBatch, UploadIntentUseCase) wire one.
	Publisher           VoiceoverPublisher
	VoiceoverRepository persistence.Repository
	Finalizer           VoiceoverFinalizer
	TxOutboxEnqueuer    TxOutboxEnqueuer
	// Enrichment carries the optional analytic ports: SemanticTagger and the
	// Rust PhraseAligner used to ground optional moment annotations. A nil
	// aligner fails closed only when a command asks for moments.
	Enrichment ProcessSegmentEnrichmentDeps
	Cache      ProcessSegmentCacheDeps
	Logger     *zap.Logger
}

type ProcessSegmentUseCase struct{ deps ProcessSegmentDeps }

func NewProcessSegmentUseCase(deps ProcessSegmentDeps) *ProcessSegmentUseCase {
	if deps.TTSProvider == nil {
		panic("voiceover.NewProcessSegmentUseCase: TTSProvider is required")
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
