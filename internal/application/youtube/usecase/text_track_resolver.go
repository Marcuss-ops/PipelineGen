// Package usecase — text_track_resolver.go: priority-chain resolver for
// localized text tracks. Reduces Whisper invocations by checking the API
// payload and the DB before falling through to expensive transcription.
//
// Priority chain:
//  1. Text provided in the API payload (Segment.Texts[].Transcript)
//  2. Text already persisted in asset_text_tracks (TextTrackRepository)
//
// After a transcript is obtained from YouTube subtitles (Step 6) or
// Whisper (Step 7), the caller invokes Save to persist it for future
// reuse.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// resolve-before-transcribe decision logic.
package usecase

import (
	"context"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TextTrackResolver implements the priority-chain lookup for text tracks
// and provides a Save method for persisting transcripts obtained from
// YouTube subtitles or Whisper.
type TextTrackResolver struct {
	Repo asset.TextTrackRepository
	Log  *zap.Logger
}

// ResolveResult carries the outcome of a Resolve call.
type ResolveResult struct {
	// Transcript is the resolved text content (empty if not found).
	Transcript string
	// LanguageCode is the language of the resolved transcript.
	LanguageCode string
	// Source records which priority level produced the result.
	Source asset.TextTrackSource
	// Found is true when a valid transcript was resolved from the
	// payload or the DB (caller should skip Whisper).
	Found bool
}

// Resolve checks Priority 1 (API payload) and Priority 2 (DB) for an
// existing transcript. Returns Found=false when neither source has a
// usable transcript, signaling the caller to proceed with YouTube
// subtitles or Whisper.
func (r *TextTrackResolver) Resolve(
	ctx context.Context,
	clipID string,
	payloadTexts []youtubetypes.LocalizedClipText,
) (ResolveResult, error) {
	// Priority 1: API payload.
	for _, t := range payloadTexts {
		if t.Transcript != "" {
			lang := t.LanguageCode
			if lang == "" {
				lang = "en"
			}
			if r.Log != nil {
				r.Log.Info("text track resolved from payload",
					zap.String("clip_id", clipID),
					zap.String("language", lang))
			}
			return ResolveResult{
				Transcript:   t.Transcript,
				LanguageCode: lang,
				Source:       asset.TextSourceProvided,
				Found:        true,
			}, nil
		}
	}

	// Priority 2: DB lookup (default language "en" for transcript).
	// TODO: make default_language configurable or derive from payload.
	if r.Repo != nil {
		track, err := r.Repo.Find(ctx, clipID, "en", asset.TextTrackTranscript)
		if err != nil {
			return ResolveResult{}, err
		}
		if track != nil && track.TextContent != "" && track.Status == asset.TextTrackReady {
			if r.Log != nil {
				r.Log.Info("text track resolved from DB",
					zap.String("clip_id", clipID),
					zap.String("language", track.LanguageCode))
			}
			return ResolveResult{
				Transcript:   track.TextContent,
				LanguageCode: track.LanguageCode,
				Source:       track.SourceType,
				Found:        true,
			}, nil
		}
	}

	return ResolveResult{Found: false}, nil
}

// Save persists a transcript to asset_text_tracks for future reuse.
// source indicates the provenance (youtube_subtitle, whisper, provided,
// etc.). languageCode defaults to "en" when empty.
func (r *TextTrackResolver) Save(
	ctx context.Context,
	clipID string,
	transcript string,
	source asset.TextTrackSource,
	languageCode string,
) error {
	if r.Repo == nil || transcript == "" {
		return nil
	}
	if languageCode == "" {
		languageCode = "en"
	}

	track := asset.TextTrack{
		AssetID:      clipID,
		LanguageCode: languageCode,
		TextKind:     asset.TextTrackTranscript,
		TextContent:  transcript,
		SourceType:   source,
		IsOriginal:   source == asset.TextSourceYouTubeSubtitle || source == asset.TextSourceProvided,
		Status:       asset.TextTrackReady,
	}

	if err := r.Repo.UpsertBatch(ctx, []asset.TextTrack{track}); err != nil {
		if r.Log != nil {
			r.Log.Warn("failed to save text track",
				zap.String("clip_id", clipID),
				zap.String("source", string(source)),
				zap.Error(err))
		}
		return err
	}

	if r.Log != nil {
		r.Log.Info("text track saved",
			zap.String("clip_id", clipID),
			zap.String("source", string(source)),
			zap.String("language", languageCode))
	}
	return nil
}
