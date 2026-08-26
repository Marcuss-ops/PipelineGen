package enrichment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Transcriber is the real Whisper port. Empty text is not a successful
// enrichment and is never persisted.
type Transcriber interface {
	TranscribeAudioWithDetection(context.Context, string) (detail.TranscriptResult, error)
}

// SubtitleWriter owns deterministic ASS creation, validation and Drive
// publication. The concrete implementation is the existing ASS materializer.
type SubtitleWriter interface {
	Write(ctx context.Context, in SubtitleInput) error
}
type SubtitleInput struct {
	AssetID, Filename, Language string
	Track                       detail.TextTrack
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
	FindReady(context.Context, string, string, detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error)
	UpsertBatch(context.Context, []detail.TextTrack) error
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
	track, cues, err := s.tracks.FindReady(ctx, in.AssetID, in.Language, detail.TextTrackTranscript)
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
		track = &detail.TextTrack{AssetID: in.AssetID, LanguageCode: lang, TextKind: detail.TextTrackTranscript, TextContent: strings.TrimSpace(out.Text), SourceType: detail.TextSourceWhisper, SourceLanguageCode: lang, IsOriginal: true, Provider: "whisper", TextHash: detail.TextHash(out.Text, lang, detail.TextTrackTranscript), Status: detail.TextTrackReady, IsCurrent: true, Confidence: out.Confidence}
		cues = out.Cues
		if err := s.tracks.UpsertBatch(ctx, []detail.TextTrack{*track}); err != nil {
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
	description := detail.TextTrack{AssetID: in.AssetID, LanguageCode: track.LanguageCode, TextKind: detail.TextTrackDescription, TextContent: strings.TrimSpace(desc.Description), SourceType: detail.TextSourceVisualAnalysis, SourceLanguageCode: track.LanguageCode, IsOriginal: true, Provider: desc.Provider, ModelName: desc.Model, ModelVersion: desc.Version, TextHash: detail.TextHash(desc.Description, track.LanguageCode, detail.TextTrackDescription), Status: detail.TextTrackReady, IsCurrent: true}
	if err := s.tracks.UpsertBatch(ctx, []detail.TextTrack{description}); err != nil {
		return result, fmt.Errorf("persist description: %w", err)
	}
	result.DescriptionCreated = true
	if strings.TrimSpace(desc.Summary) != "" {
		summary := detail.TextTrack{AssetID: in.AssetID, LanguageCode: track.LanguageCode, TextKind: detail.TextTrackSummary, TextContent: strings.TrimSpace(desc.Summary), SourceType: detail.TextSourceVisualAnalysis, SourceLanguageCode: track.LanguageCode, IsOriginal: true, Provider: desc.Provider, ModelName: desc.Model, ModelVersion: desc.Version, TextHash: detail.TextHash(desc.Summary, track.LanguageCode, detail.TextTrackSummary), Status: detail.TextTrackReady, IsCurrent: true}
		if err := s.tracks.UpsertBatch(ctx, []detail.TextTrack{summary}); err != nil {
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
