package adapters

// asset_transcript.go — transcript-as-a-service for canonical media assets.
//
// GET /api/clips/transcript used to be YouTube-only because the handler was
// wired to the subtitle fetcher. That left clips/stock assets with a READY
// text track (or a materializable source) unreachable without a YouTube URL.
// This adapter is the missing asset-side resolver:
//
//  1. reuse a READY canonical text track (same repository as the render path);
//  2. when no track exists, materialize the asset and run the SAME streaming
//     Whisper resolver used by clip.render, persisting the result so the next
//     request is a cache hit.
//
// It deliberately does not re-implement ASR, text hashing, persistence or
// language provenance: those stay owned by ClipRenderTranscriptResolver.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// AssetTranscriptTrackReader is the narrow text-track surface this resolver
// needs. *detail.TextTrackRepository satisfies it; tests can double it without
// implementing the whole repository.
type AssetTranscriptTrackReader interface {
	FindReady(ctx context.Context, assetID string, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error)
	ListByAsset(ctx context.Context, assetID string) ([]detail.TextTrack, error)
}

// AssetTranscriptResolver resolves a canonical asset_id to a transcript bundle.
// Text-track lookup is always available; generation requires the asset
// resolver, materializer and transcript resolver to be wired. A missing
// generation dependency is a typed error at call time (fail-closed), never a
// silent empty result.
type AssetTranscriptResolver struct {
	assets       cliprender.AssetResolver
	materializer cliprender.AssetMaterializer
	transcript   cliprender.TranscriptResolver
	tracks       AssetTranscriptTrackReader
	log          *zap.Logger
}

// NewAssetTranscriptResolver wires the resolver. tracks is required because
// the READY-track fast path must always work; the other dependencies are
// optional and only needed for the ASR fallback.
func NewAssetTranscriptResolver(
	assets cliprender.AssetResolver,
	materializer cliprender.AssetMaterializer,
	transcript cliprender.TranscriptResolver,
	tracks AssetTranscriptTrackReader,
	log *zap.Logger,
) (*AssetTranscriptResolver, error) {
	if tracks == nil {
		return nil, errors.New("asset transcript: text track reader is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetTranscriptResolver{
		assets:       assets,
		materializer: materializer,
		transcript:   transcript,
		tracks:       tracks,
		log:          log,
	}, nil
}

// FetchAssetTranscript returns (bundle, true, nil) when a READY track exists or
// ASR produced usable text. It returns (nil, false, nil) when the asset has no
// transcript (including a completed ASR pass with no speech), and a typed
// error for repository/materialization/ASR failures.
func (r *AssetTranscriptResolver) FetchAssetTranscript(
	ctx context.Context,
	assetID string,
	language string,
	startSec int,
	endSec int,
) (*detail.ResolvedTextBundle, bool, error) {
	if r == nil || r.tracks == nil {
		return nil, false, errors.New("asset transcript: resolver is not wired")
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return nil, false, errors.New("asset transcript: asset_id is required")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = r.preferredLanguage(ctx, assetID)
	}

	if language != "" {
		track, cues, err := r.tracks.FindReady(ctx, assetID, language, detail.TextTrackTranscript)
		if err != nil {
			return nil, false, err
		}
		if track != nil {
			bundle := bundleFromTextTrack(track, cues)
			if !bundle.IsEmpty() {
				return sliceTranscriptBundle(bundle, startSec, endSec), true, nil
			}
		}
	}

	if r.assets == nil || r.materializer == nil || r.transcript == nil {
		return nil, false, fmt.Errorf("asset transcript: ASR fallback not wired for %q", assetID)
	}
	ref, err := r.assets.ResolveAsset(ctx, assetID)
	if err != nil {
		return nil, false, err
	}
	if ref == nil {
		return nil, false, fmt.Errorf("asset transcript: resolver returned no asset for %q", assetID)
	}
	source, err := r.materializer.Materialize(ctx, *ref)
	if err != nil {
		return nil, false, err
	}
	if source == nil || source.LocalPath == "" {
		return nil, false, fmt.Errorf("asset transcript: materializer returned no local source for %q", assetID)
	}
	result, err := r.transcript.Generate(ctx, cliprender.TranscriptInput{
		AssetID:      assetID,
		Language:     language,
		Mode:         "generate",
		Persist:      true,
		SourceSHA256: source.SHA256,
	}, source)
	if errors.Is(err, cliprender.ErrNoSpeech) {
		r.log.Info("asset transcript: ASR found no speech",
			zap.String("asset_id", assetID),
			zap.String("language", language))
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !result.HasText() {
		return nil, false, nil
	}
	bundle := bundleFromTranscriptResult(result)
	return sliceTranscriptBundle(bundle, startSec, endSec), true, nil
}

// preferredLanguage picks the original READY transcript, then the first READY
// one, when the caller did not request a language. A repository error is
// logged and treated as "unknown": ASR can still detect the language, and a
// transient list failure must not hide a usable generation path.
func (r *AssetTranscriptResolver) preferredLanguage(ctx context.Context, assetID string) string {
	tracks, err := r.tracks.ListByAsset(ctx, assetID)
	if err != nil {
		r.log.Warn("asset transcript: list text tracks failed; falling back to ASR detection",
			zap.String("asset_id", assetID),
			zap.Error(err))
		return ""
	}
	var first string
	for _, track := range tracks {
		if track.TextKind != detail.TextTrackTranscript || track.Status != detail.TextTrackReady || strings.TrimSpace(track.LanguageCode) == "" {
			continue
		}
		if track.IsOriginal {
			return track.LanguageCode
		}
		if first == "" {
			first = track.LanguageCode
		}
	}
	return first
}

func bundleFromTextTrack(track *detail.TextTrack, cues []detail.TimedCue) *detail.ResolvedTextBundle {
	sourceLanguage := track.SourceLanguageCode
	if sourceLanguage == "" {
		sourceLanguage = track.LanguageCode
	}
	return &detail.ResolvedTextBundle{
		LanguageCode:       track.LanguageCode,
		SourceLanguageCode: sourceLanguage,
		PlainText:          track.TextContent,
		Cues:               append([]detail.TimedCue(nil), cues...),
		SourceType:         track.SourceType,
		IsOriginal:         track.IsOriginal,
		Provider:           track.Provider,
		Confidence:         track.Confidence,
	}
}

func bundleFromTranscriptResult(result *cliprender.TranscriptResult) *detail.ResolvedTextBundle {
	sourceType := detail.TextTrackSource(result.StreamSourceType)
	if sourceType == "" {
		sourceType = detail.TextSourceWhisper
	}
	sourceLanguage := result.DetectedLanguage
	if sourceLanguage == "" {
		sourceLanguage = result.Language
	}
	isOriginal := true
	if result.DetectedLanguage != "" && result.Language != "" {
		isOriginal = sameLanguage(result.DetectedLanguage, result.Language)
	}
	return &detail.ResolvedTextBundle{
		LanguageCode:       result.Language,
		SourceLanguageCode: sourceLanguage,
		PlainText:          result.Text,
		Cues:               mapClipRenderCues(result.Cues),
		SourceType:         sourceType,
		IsOriginal:         isOriginal,
		Provider:           "whisper",
		Confidence:         result.Confidence,
	}
}

// sliceTranscriptBundle filters cues to the requested asset-relative window.
// Timestamps stay on the asset timeline (the YouTube endpoint keeps
// source-video timestamps for the same operator contract); only cues that
// overlap the window are returned. When there are no cues, the full text is
// kept because it cannot be sliced reliably.
func sliceTranscriptBundle(bundle *detail.ResolvedTextBundle, startSec, endSec int) *detail.ResolvedTextBundle {
	if bundle == nil || (startSec <= 0 && endSec <= 0) || len(bundle.Cues) == 0 {
		return bundle
	}
	startMs := int64(0)
	if startSec > 0 {
		startMs = int64(startSec) * 1000
	}
	endMs := int64(0)
	if endSec > startSec {
		endMs = int64(endSec) * 1000
	}
	out := *bundle
	out.Cues = nil
	texts := make([]string, 0, len(bundle.Cues))
	for _, cue := range bundle.Cues {
		if cue.EndMs <= startMs {
			continue
		}
		if endMs > 0 && cue.StartMs >= endMs {
			continue
		}
		out.Cues = append(out.Cues, cue)
		if text := strings.TrimSpace(cue.Text); text != "" {
			texts = append(texts, text)
		}
	}
	out.PlainText = strings.Join(texts, "\n")
	return &out
}
