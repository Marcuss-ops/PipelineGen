package enrichment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Transcriber is the real Whisper port. Empty text is not a successful
// enrichment and is never persisted.
type Transcriber interface {
	TranscribeAudioWithDetection(context.Context, string) (asset.TranscriptResult, error)
}

// SubtitleWriter owns deterministic ASS creation, validation and Drive
// publication. The concrete implementation is the existing ASS materializer.
type SubtitleWriter interface {
	Write(ctx context.Context, in SubtitleInput) error
}
type SubtitleInput struct {
	AssetID, Filename, Language string
	Track                       asset.TextTrack
	DurationMs                  int64
	DriveFolderID               string
	ContentHash                 string
}

// SemanticDescriber must inspect the clip (frames/audio context) and return a
// useful human-facing description. A transcript-only implementation is not a
// valid substitute for this port.
type SemanticDescriber interface {
	Describe(context.Context, DescriptionInput) (DescriptionOutput, error)
}
type DescriptionInput struct{ AssetID, LocalPath, Transcript, Language string }
type DescriptionOutput struct {
	Description, Summary     string
	Provider, Model, Version string
}

type TrackWriter interface {
	FindReady(context.Context, string, string, asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error)
	UpsertBatch(context.Context, []asset.TextTrack) error
}
type ClipReindexer interface {
	RequestAsset(context.Context, string) error
}

type ClipService struct {
	transcriber Transcriber
	subtitles   SubtitleWriter
	describer   SemanticDescriber
	tracks      TrackWriter
	reindex     ClipReindexer
}

func NewClipService(t Transcriber, s SubtitleWriter, d SemanticDescriber, tracks TrackWriter, reindex ClipReindexer) (*ClipService, error) {
	if t == nil || s == nil || d == nil || tracks == nil || reindex == nil {
		return nil, errors.New("clip enrichment: all dependencies are required")
	}
	return &ClipService{transcriber: t, subtitles: s, describer: d, tracks: tracks, reindex: reindex}, nil
}

type ClipInput struct {
	AssetID, LocalPath, Filename, Language, DriveFolderID, ContentHash string
	DurationMs                                                         int64
}
type ClipResult struct {
	AssetID                                                                                                 string
	TranscriptReused, TranscriptCreated, DescriptionCreated, SummaryCreated, SubtitleCreated, ReindexQueued bool
}

// Process performs the safe order: validate/reuse transcript, persist a new
// transcript only when missing, materialize ASS, then run semantic analysis.
// A failed semantic provider never fabricates a description or summary.
func (s *ClipService) Process(ctx context.Context, in ClipInput) (ClipResult, error) {
	if strings.TrimSpace(in.AssetID) == "" || strings.TrimSpace(in.LocalPath) == "" || strings.TrimSpace(in.Language) == "" {
		return ClipResult{}, errors.New("clip enrichment: asset_id, local_path and language are required")
	}
	result := ClipResult{AssetID: in.AssetID}
	track, cues, err := s.tracks.FindReady(ctx, in.AssetID, in.Language, asset.TextTrackTranscript)
	if err != nil {
		return result, fmt.Errorf("read transcript: %w", err)
	}
	if track != nil && strings.TrimSpace(track.TextContent) != "" {
		result.TranscriptReused = true
	} else {
		out, transcribeErr := s.transcriber.TranscribeAudioWithDetection(ctx, in.LocalPath)
		if transcribeErr != nil {
			return result, fmt.Errorf("whisper: %w", transcribeErr)
		}
		if strings.TrimSpace(out.Text) == "" || len(out.Cues) == 0 {
			return result, errors.New("clip enrichment: whisper returned no validated transcript cues")
		}
		lang := out.DetectedLanguage
		if lang == "" {
			lang = in.Language
		}
		track = &asset.TextTrack{AssetID: in.AssetID, LanguageCode: lang, TextKind: asset.TextTrackTranscript, TextContent: strings.TrimSpace(out.Text), SourceType: asset.TextSourceWhisper, SourceLanguageCode: lang, IsOriginal: true, Provider: "whisper", TextHash: asset.TextHash(out.Text, lang, asset.TextTrackTranscript), Status: asset.TextTrackReady, IsCurrent: true, Confidence: out.Confidence}
		cues = out.Cues
		if err := s.tracks.UpsertBatch(ctx, []asset.TextTrack{*track}); err != nil {
			return result, fmt.Errorf("persist transcript: %w", err)
		}
		result.TranscriptCreated = true
	}
	if len(cues) == 0 {
		return result, errors.New("clip enrichment: validated transcript has no timed cues for ASS")
	}
	if err := s.subtitles.Write(ctx, SubtitleInput{AssetID: in.AssetID, Filename: in.Filename, Language: track.LanguageCode, Track: *track, DurationMs: in.DurationMs, DriveFolderID: in.DriveFolderID, ContentHash: in.ContentHash}); err != nil {
		return result, fmt.Errorf("materialize ASS: %w", err)
	}
	result.SubtitleCreated = true
	desc, err := s.describer.Describe(ctx, DescriptionInput{AssetID: in.AssetID, LocalPath: in.LocalPath, Transcript: track.TextContent, Language: track.LanguageCode})
	if err != nil {
		return result, fmt.Errorf("semantic description: %w", err)
	}
	if strings.TrimSpace(desc.Description) == "" {
		return result, errors.New("clip enrichment: semantic provider returned empty description")
	}
	description := asset.TextTrack{AssetID: in.AssetID, LanguageCode: track.LanguageCode, TextKind: asset.TextTrackDescription, TextContent: strings.TrimSpace(desc.Description), SourceType: asset.TextSourceVisualAnalysis, SourceLanguageCode: track.LanguageCode, IsOriginal: true, Provider: desc.Provider, ModelName: desc.Model, ModelVersion: desc.Version, TextHash: asset.TextHash(desc.Description, track.LanguageCode, asset.TextTrackDescription), Status: asset.TextTrackReady, IsCurrent: true}
	if err := s.tracks.UpsertBatch(ctx, []asset.TextTrack{description}); err != nil {
		return result, fmt.Errorf("persist description: %w", err)
	}
	result.DescriptionCreated = true
	if strings.TrimSpace(desc.Summary) != "" {
		summary := asset.TextTrack{AssetID: in.AssetID, LanguageCode: track.LanguageCode, TextKind: asset.TextTrackSummary, TextContent: strings.TrimSpace(desc.Summary), SourceType: asset.TextSourceVisualAnalysis, SourceLanguageCode: track.LanguageCode, IsOriginal: true, Provider: desc.Provider, ModelName: desc.Model, ModelVersion: desc.Version, TextHash: asset.TextHash(desc.Summary, track.LanguageCode, asset.TextTrackSummary), Status: asset.TextTrackReady, IsCurrent: true}
		if err := s.tracks.UpsertBatch(ctx, []asset.TextTrack{summary}); err != nil {
			return result, fmt.Errorf("persist summary: %w", err)
		}
		result.SummaryCreated = true
	}
	if err := s.reindex.RequestAsset(ctx, in.AssetID); err != nil {
		return result, fmt.Errorf("targeted reindex: %w", err)
	}
	result.ReindexQueued = true
	return result, nil
}
