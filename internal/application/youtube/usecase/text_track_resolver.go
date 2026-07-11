package usecase

// text_track_resolver.go implements the priority-chain resolver AND
// the centralised persistence of localized text tracks per clip.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The 5-level acquisition chain (payload → DB → YouTube subtitle
//     → Whisper) lives in AcquireSegmentText ONLY. NO handler may
//     re-implement priority 3-5 inline (PR-PY-CLIPS-CORRETTE-TRADOTTE
//     Fase 1.a, July 2026).
//   - The TextHash + SourceVersion SHA-256 formula lives in
//     internal/domain/asset/text_track_hashes.go. This resolver calls
//     the helpers and NEVER re-implements the chain inline.
//
// Pre-Step-9 persistence is FORBIDDEN: the asset_text_tracks.asset_id
// foreign key references media_assets.id. Saving a track whose clip
// row doesn't yet exist will surface as a typed SQLite FK violation
// and silently fail the batch. Callers MUST invoke SaveMany +
// MaterializePayloadTexts AFTER Step 9 completed
// (ClipAtomicWriter.CommitClipAndIndexEvent).
//
// godlike/07 no-fake-availability: persistence errors propagate
// VERBATIM (no `_ = ...`). The caller in step6to9 mirrors the
// BLOCKER #4 partial-state pattern (log + status mutation, do NOT
// fail the pipeline) so the clip is always safely persisted.

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TextTrackResolver bundles the DB lookup, YouTube subtitle + Whisper
// ports, and the canonical SQL repository behind a single typed
// surface. The 4 field-shape is below the AGENTS.md 8-deps cap and
// matches the canonical port-bundle pattern.
type TextTrackResolver struct {
	// Repo is the canonical SQL TextTrackRepository. nil-tolerance
	// matches the rest of the youtube package: a nil Repo skips
	// priority 2 silently (no DB lookup) and SaveMany is a no-op.
	Repo asset.TextTrackRepository
	// Subtitles is the OPTIONAL YouTube subtitle port (priority 3+4).
	// nil → priority skipped; AcquireSegmentText falls through to
	// Whisper directly. Set at composition in build_bundles_domain_media.go.
	Subtitles youtubeports.SubtitleFetcherPort
	// Transcriber is the OPTIONAL Whisper port (priority 5). nil →
	// priority skipped; AcquireSegmentText returns (nil, nil) when no
	// other level produced content. Set at composition; Fase 1.b will
	// introduce the concrete whisper adapter at wire-time.
	Transcriber youtubeports.WhisperTranscriberPort
	// Log is the canonical zap logger for operator forensics.
	Log *zap.Logger
}

// ResolveResult is the legacy DB/payload lookup shape retained for
// callers that want a TWO-LEVEL existence check (payload + DB) without
// running the full 5-level chain (e.g. clipindexer's prefix search).
type ResolveResult struct {
	Transcript   string
	LanguageCode string
	Source       asset.TextTrackSource
	Found        bool
}

// TextTrackAcquireRequest bundles the inputs the 5-level chain needs.
// All fields are mandatory except LocalPath (Whisper fallback is
// only triggered when LocalPath is non-empty AND transcriber != nil).
type TextTrackAcquireRequest struct {
	ClipID       string
	VideoID      string
	StartSec     int
	EndSec       int
	PayloadTexts []youtubetypes.LocalizedClipText
	LocalPath    string
}

// Resolve performs a TWO-LEVEL priority lookup (payload → DB) and
// returns the first transcript with non-empty content. Does NOT
// probe YouTube subtitles or Whisper — use AcquireSegmentText for
// the full 5-level chain.
//
// godlike/07 honest lock: empty languageCode defaults to BCP-47
// "und" rather than "en" (the legacy caller-substitution pattern
// was the root cause of the "Italian original stored as English"
// audit finding, godlike 2026-07-11 §2.c).
func (r *TextTrackResolver) Resolve(ctx context.Context, clipID string, payloadTexts []youtubetypes.LocalizedClipText) (ResolveResult, error) {
	// Priority 1: API payload.
	for _, t := range payloadTexts {
		if t.Transcript == "" {
			continue
		}
		lang := t.LanguageCode
		if lang == "" {
			lang = "und"
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

	// Priority 2: DB lookup — first READY transcript row, regardless
	// of language. The codebase may carry multi-language DB rows for
	// the same clip; the resolver surfaces the first match without
	// bias (callers pick by LanguageCode after).
	if r.Repo != nil {
		tracks, err := r.Repo.ListByAsset(ctx, clipID)
		if err != nil {
			return ResolveResult{}, err
		}
		for _, row := range tracks {
			if row.TextKind == asset.TextTrackTranscript && row.Status == asset.TextTrackReady && row.TextContent != "" {
				if r.Log != nil {
					r.Log.Info("text track resolved from DB",
						zap.String("clip_id", clipID),
						zap.String("language", row.LanguageCode))
				}
				return ResolveResult{
					Transcript:   row.TextContent,
					LanguageCode: row.LanguageCode,
					Source:       row.SourceType,
					Found:        true,
				}, nil
			}
		}
	}

	return ResolveResult{Found: false}, nil
}

// AcquireSegmentText runs the canonical 5-level chain and returns the
// primary (original-language) ResolvedTextBundle.
//
// Priority order:
//  1. Payload Texts (first non-empty Transcript wins as primary)
//  2. DB READY transcript (any language)
//  3. YouTube subtitle manual via Subtitles.FetchSegmentSubtitles
//  4. YouTube subtitle auto (same FetchSegmentSubtitles call — the
//     adapter probes both manual + auto)
//  5. Whisper fallback via Transcriber.TranscribeAudio (language
//     "und" until Fase 1.b extends the port with DetectedLanguage)
//
// Returns (nil, nil) when every priority level fails to produce a
// transcript — caller surfaces a typed TEXT_TRACK_NOT_READY error
// in Fase 4. Returns (nil, err) only when Whisper returned a typed
// error; subtitle errors are logged+swallowed so the chain can
// fall through to priority 5 (matches godlike/07 no-fake-availability:
// one Whisper failure must not cascade into a hard pipeline error).
//
// godlike/06 SSOT: this is the SOLE canonical chain. Handlers MUST
// NOT reimplement priority 3-5 inline (audit 2026-07-11 §2.b: the
// pre-PR inline Whisper path in step6to9 was the root cause of the
// double-Whisper inconsistency between Step 7 and Step 10).
func (r *TextTrackResolver) AcquireSegmentText(ctx context.Context, req TextTrackAcquireRequest) (*asset.ResolvedTextBundle, error) {
	if r == nil {
		return nil, nil
	}

	// Priority 1: API payload.
	for i := range req.PayloadTexts {
		t := req.PayloadTexts[i]
		if t.Transcript == "" {
			continue
		}
		lang := t.LanguageCode
		if lang == "" {
			lang = "und"
		}
		if r.Log != nil {
			r.Log.Info("text track acquired from payload",
				zap.String("clip_id", req.ClipID),
				zap.String("language", lang))
		}
		return &asset.ResolvedTextBundle{
			LanguageCode:       lang,
			SourceLanguageCode: sourceLangOf(t, lang),
			PlainText:          t.Transcript,
			Cues:               nil,
			SourceType:         sourceOrProvided(t.SourceType),
			IsOriginal:         t.IsOriginal || t.SourceLanguageCode == "" || t.SourceLanguageCode == lang,
			Provider:           "",
			ModelName:          t.ModelName,
			ModelVersion:       t.ModelVersion,
			Confidence:         confidencePtr(t.Confidence),
		}, nil
	}

	// Priority 2: DB READY transcript row.
	if r.Repo != nil {
		tracks, err := r.Repo.ListByAsset(ctx, req.ClipID)
		if err != nil {
			return nil, err
		}
		for _, row := range tracks {
			if row.TextKind == asset.TextTrackTranscript && row.Status == asset.TextTrackReady && row.TextContent != "" {
				if r.Log != nil {
					r.Log.Info("text track acquired from DB (READY)",
						zap.String("clip_id", req.ClipID),
						zap.String("language", row.LanguageCode))
				}
				return cdbRowToBundle(row), nil
			}
		}
	}

	// Priority 3 + 4: YouTube subtitle manual + auto. The
	// SubtitleFetcherPort.FetchSegmentSubtitles adapter probes both
	// (manual first then auto fallback) and returns ONE typed bundle.
	var bundle *asset.ResolvedTextBundle
	if r.Subtitles != nil && req.VideoID != "" {
		var subErr error
		bundle, subErr = r.Subtitles.FetchSegmentSubtitles(ctx, req.VideoID, req.StartSec, req.EndSec)
		if subErr != nil {
			if r.Log != nil {
				r.Log.Warn("subtitle acquisition failed; falling through to Whisper",
					zap.String("clip_id", req.ClipID),
					zap.String("video_id", req.VideoID),
					zap.Error(subErr))
			}
			// Non-fatal: continue to Whisper.
		} else if bundle != nil && !bundle.IsEmpty() {
			if r.Log != nil {
				r.Log.Info("text track acquired from YouTube subtitles",
					zap.String("clip_id", req.ClipID),
					zap.String("video_id", req.VideoID),
					zap.String("language", bundle.LanguageCode))
			}
			return bundle, nil
		}
	}

	// Priority 5: Whisper fallback. TranscribeAudio returns just
	// (string, error) today; Fase 1.b will extend the port to
	// (string, languageCode, *float64, error). For now the
	// LanguageCode defaults to BCP-47 "und" (NEVER "en").
	if r.Transcriber != nil && req.LocalPath != "" {
		text, wErr := r.Transcriber.TranscribeAudio(ctx, req.LocalPath)
		if wErr != nil {
			return nil, wErr
		}
		if text != "" {
			if r.Log != nil {
				r.Log.Info("text track acquired from Whisper (language=und; Fase 1.b will surface DetectedLanguage)",
					zap.String("clip_id", req.ClipID),
					zap.String("local_path", req.LocalPath))
			}
			return &asset.ResolvedTextBundle{
				LanguageCode:       "und",
				SourceLanguageCode: "und",
				PlainText:          text,
				Cues:               nil,
				SourceType:         asset.TextSourceWhisper,
				IsOriginal:         true,
				Provider:           "whisper",
				ModelName:          "",
				ModelVersion:       "",
				Confidence:         nil,
			}, nil
		}
	}

	if r.Log != nil {
		r.Log.Info("text track acquisition exhausted all 5 priorities without finding usable content",
			zap.String("clip_id", req.ClipID))
	}
	return nil, nil
}

// Save persists a single transcript row (text_kind=transcript) to
// asset_text_tracks. To materialise all payload-provided texts
// (transcript + title + summary + description per language) use
// MaterializePayloadTexts + SaveMany.
//
// godlike/07 honest lock:
//   - empty languageCode defaults to BCP-47 "und" (NEVER "en");
//   - empty Source is a TYPED error (no silent classify-as-provided).
//
// Empty transcript or nil Repo → no-op (idempotent).
func (r *TextTrackResolver) Save(
	ctx context.Context,
	clipID string,
	transcript string,
	source asset.TextTrackSource,
	languageCode string,
) error {
	if r == nil || r.Repo == nil || transcript == "" {
		return nil
	}
	if source == "" {
		return fmt.Errorf("text_track_resolver.Save: source is required (got empty); refusing to write provenance-less asset_text_tracks row")
	}
	lang := languageCode
	if lang == "" {
		lang = "und"
	}
	bundle := &asset.ResolvedTextBundle{
		LanguageCode:       lang,
		SourceLanguageCode: lang,
		PlainText:          transcript,
		SourceType:         source,
		IsOriginal:         source == asset.TextSourceYouTubeSubtitle || source == asset.TextSourceProvided || source == asset.TextSourceWhisper,
		Provider:           "",
	}
	tracks := bundleToTextTracks(clipID, bundle, asset.TextTrackTranscript)
	if len(tracks) == 0 {
		return nil
	}
	if r.Log != nil {
		r.Log.Info("text track saved",
			zap.String("clip_id", clipID),
			zap.String("source", string(source)),
			zap.String("language", lang))
	}
	return r.Repo.UpsertBatch(ctx, tracks)
}

// SaveMany persists a pre-built batch of TextTrack rows. Idempotent
// and nil-safe: nil Receiver or empty slice is a no-op. Errors are
// propagated verbatim (caller in step6to9 mirrors the BLOCKER #4
// partial-state pattern on failure).
func (r *TextTrackResolver) SaveMany(ctx context.Context, tracks []asset.TextTrack) error {
	if r == nil || r.Repo == nil || len(tracks) == 0 {
		return nil
	}
	if r.Log != nil {
		r.Log.Info("text track batch saved",
			zap.Int("count", len(tracks)))
	}
	return r.Repo.UpsertBatch(ctx, tracks)
}

// MaterializePayloadTexts converts the payload's []LocalizedClipText
// into TextTrack rows for every non-empty field per input
// (transcript/title/summary/description, max 4 rows per input). Each
// row's TextHash + SourceVersion are computed via the canonical
// factory.
//
// Pre-step-9 pre-condition: this method does NOT verify the clip row
// exists. Callers MUST have committed the clip row
// (ClipAtomicWriter.CommitClipAndIndexEvent) before invoking SaveMany
// with the rows returned here — otherwise the FK constraint on
// asset_text_tracks.asset_id will reject the upsert and the SQLite
// transaction rolls back. The atomic same-tx write is scope of
// Fase 2.b; Fase 1.a uses post-step-9 persistence as an interim
// safe pattern (no FK violation; clip + outbox are precommitted).
func (r *TextTrackResolver) MaterializePayloadTexts(clipID string, texts []youtubetypes.LocalizedClipText) []asset.TextTrack {
	var out []asset.TextTrack
	for _, t := range texts {
		lang := t.LanguageCode
		if lang == "" {
			lang = "und"
		}
		srcType := asset.TextTrackSource(t.SourceType)
		if srcType == "" {
			srcType = asset.TextSourceProvided
		}
		isOriginal := t.IsOriginal ||
			srcType == asset.TextSourceProvided ||
			srcType == asset.TextSourceYouTubeSubtitle ||
			srcType == asset.TextSourceWhisper

		srcLang := t.SourceLanguageCode
		if srcLang == "" && isOriginal {
			srcLang = lang
		}

		push := func(kind asset.TextTrackKind, text string) {
			if text == "" {
				return
			}
			hash := asset.TextHash(text, lang, kind)
			out = append(out, asset.TextTrack{
				AssetID:            clipID,
				LanguageCode:       lang,
				TextKind:           kind,
				TextContent:        text,
				SourceType:         srcType,
				SourceLanguageCode: srcLang,
				IsOriginal:         isOriginal,
				Provider:           "",
				ModelName:          t.ModelName,
				ModelVersion:       t.ModelVersion,
				TextHash:           hash,
				SourceVersion:      asset.SourceVersion(hash, srcLang, lang, "", t.ModelName, t.ModelVersion, ""),
				Confidence:         confidencePtr(t.Confidence),
				Status:             asset.TextTrackReady,
			})
		}
		push(asset.TextTrackTranscript, t.Transcript)
		push(asset.TextTrackTitle, t.Title)
		push(asset.TextTrackSummary, t.Summary)
		push(asset.TextTrackDescription, t.Description)
	}
	return out
}

// bundleToTextTracks converts a non-empty bundle into a single
// TextTrack row of the requested kind. Returns nil for empty bundle
// or empty plaintext. Computes canonical hashes via the factory.
func bundleToTextTracks(clipID string, bundle *asset.ResolvedTextBundle, kind asset.TextTrackKind) []asset.TextTrack {
	if bundle == nil || bundle.IsEmpty() || bundle.PlainText == "" {
		return nil
	}
	lang := bundle.LanguageCode
	if lang == "" {
		lang = "und"
	}
	srcLang := bundle.SourceLanguageCode
	if srcLang == "" {
		srcLang = lang
	}
	hash := asset.TextHash(bundle.PlainText, lang, kind)
	return []asset.TextTrack{{
		AssetID:            clipID,
		LanguageCode:       lang,
		TextKind:           kind,
		TextContent:        bundle.PlainText,
		SourceType:         bundle.SourceType,
		SourceLanguageCode: srcLang,
		IsOriginal:         bundle.IsOriginal,
		Provider:           bundle.Provider,
		ModelName:          bundle.ModelName,
		ModelVersion:       bundle.ModelVersion,
		TextHash:           hash,
		SourceVersion:      asset.SourceVersion(hash, srcLang, lang, bundle.Provider, bundle.ModelName, bundle.ModelVersion, ""),
		Confidence:         bundle.Confidence,
		Status:             asset.TextTrackReady,
	}}
}

// bundleToTimedTrack converts a non-empty bundle's Cues into a
// single localized.TimedTextTrack row (text_kind=transcript).
// Returns nil for nil bundle or empty Cues. SequenceNo assignment
// is the writer's responsibility (the writer sorts ascending by
// StartMs before assigning sequence_no based on the array index).
//
// godlike/06 SSOT: this is the canonical converter between the
// resolver's plain-text bundle and the writer's per-cue timed
// shape. The LocalizationWriter consumes it directly — handlers
// MUST NOT re-shape the data inline.
//
// Scope (Fase 2.b):
//   - Today, ONLY the resolved BUNDLE (priority 1-5 winner from
//     AcquireSegmentText) carries Cues — payload-provided
//     LocalizedClipText rows in Segment.Texts yield TextTrack
//     rows only (no cues). The cue-row surface is bundle-only.
//   - Fase 5 (payload-level cues): if Segment.Texts grows a
//     Cues field, this helper grows a parallel MaterializePayload
//     TimedTracks API. Until then the publisher contract is
//     bundle-only.
//
// Pre-conditions (audit 2026-07-11 §2.c):
//   - Each Cue has StartMs >= 0, EndMs >= StartMs, Text != "".
//     The writer validates these again (defence in depth), but
//     early surfacing here keeps the writer's typed errors
//     narrowed to "writer-level" failures, not "caller-level"
//     invariants. Malformed upstream cues are SILENTLY DROPPED
//     here (not a hard fail); the writer's metrics surface the
//     dropped-cue count via log line.
//
//   - The bundle's LanguageCode is normalized to BCP-47 ("und"
//     when empty — mirroring bundleToTextTracks).
func bundleToTimedTrack(clipID string, bundle *asset.ResolvedTextBundle) *localized.TimedTextTrack {
	if bundle == nil {
		return nil
	}
	if len(bundle.Cues) == 0 {
		return nil
	}
	lang := bundle.LanguageCode
	if lang == "" {
		lang = "und"
	}
	cues := make([]asset.TimedCue, 0, len(bundle.Cues))
	for _, c := range bundle.Cues {
		if c.StartMs < 0 || c.EndMs < c.StartMs || c.Text == "" {
			// Skip invalid cues rather than blow up the bundle.
			// godlike/07 honest lock: a malformed upstream cue
			// MUST NOT poison the whole super-tx; the resolver
			// dropped it loudly and the writer logs the count.
			continue
		}
		cues = append(cues, c)
	}
	if len(cues) == 0 {
		return nil
	}
	return &localized.TimedTextTrack{
		LanguageCode: lang,
		TextKind:     asset.TextTrackTranscript,
		SourceType:   bundle.SourceType,
		Cues:         cues,
	}
}

func cdbRowToBundle(row asset.TextTrack) *asset.ResolvedTextBundle {
	return &asset.ResolvedTextBundle{
		LanguageCode:       row.LanguageCode,
		SourceLanguageCode: row.SourceLanguageCode,
		PlainText:          row.TextContent,
		Cues:               nil,
		SourceType:         row.SourceType,
		IsOriginal:         row.IsOriginal,
		Provider:           row.Provider,
		ModelName:          row.ModelName,
		ModelVersion:       row.ModelVersion,
		Confidence:         row.Confidence,
	}
}

func sourceOrProvided(s string) asset.TextTrackSource {
	if s == "" {
		return asset.TextSourceProvided
	}
	return asset.TextTrackSource(s)
}

func sourceLangOf(t youtubetypes.LocalizedClipText, fallback string) string {
	if t.SourceLanguageCode != "" {
		return t.SourceLanguageCode
	}
	if t.IsOriginal {
		return fallback
	}
	return ""
}

func confidencePtr(c float64) *float64 {
	if c == 0 {
		return nil
	}
	return &c
}
