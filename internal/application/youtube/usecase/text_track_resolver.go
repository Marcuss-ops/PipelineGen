// Package usecase — text_track_resolver.go is the SLIM ORCHESTRATOR
// of the 6-file text_track split. It is the SOLE canonical site for:
//  1. BCP-47 language normalisation (all asset.Normalize() calls
//     live here — never in the leaf files).
//  2. ResolvedTextBundle provenance assembly (SourceType, IsOriginal,
//     Provider, ModelName, ModelVersion, Confidence,
//     SourceLanguageCode).
//  3. The 5-level acquisition chain (payload → DB → YouTube
//     subtitles manual → YouTube subtitles auto → Whisper).
//  4. The RequireLanguageCertainty policy gate
//     (asset.ErrLanguageUndeterminable pre-Step-9 when the chain
//     exhausts without surfacing a real BCP-47 language).
//
// 6-file split (July 2026): the raw dataflow per source lives in
// the leaf files; this orchestrator composes (1) and (2) above.
//
//	text_track_resolver.go         -- orchestrator (this file)
//	text_track_resolver_persist.go -- Save / SaveMany / MaterializePayloadTexts
//	text_track_payload.go          -- raw payload first-non-empty extractor
//	text_track_database.go         -- READY-only DB lookup helper
//	text_track_subtitles.go        -- bundle -> TimedTextTrack converter
//	text_track_whisper.go          -- Whisper typed-port helper
//	text_track_persistence.go      -- row-factory + row <-> bundle
//	                                  transformers (bundleToTextTracks,
//	                                  sourceLangOf, confidencePtr,
//	                                  sourceOrProvided)
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The 5-level acquisition chain lives in AcquireSegmentText ONLY.
//     NO handler may re-implement priorities 3-5 inline
//     (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a, July 2026).
//   - The TextHash + SourceVersion SHA-256 formula lives in
//     internal/domain/asset/text_track_hashes.go. The orchestrator
//     calls the helpers and NEVER re-implements the formula inline.
//   - The canonical BCP-47 normalization rules live in
//     internal/domain/asset/bcp47.go. The orchestrator calls
//     asset.Normalize and NEVER re-derives the rules inline
//     (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026).
//   - The 6-file split delegates raw dataflow to leaf files but keeps
//     normalisation + provenance composition exclusively here.
//
// Resolver/persistence split (Commit D, July 2026):
//   - The 3 WRITE methods (Save, SaveMany, MaterializePayloadTexts)
//     now live in text_track_resolver_persist.go (same package).
//     The orchestrator retains only the 5-level READ chain
//     (ResolveOriginal / ResolveLanguage / ResolveBestAvailable /
//     AcquireSegmentText) + the languageInList helper. The split is
//     intra-package (no import cycle risk) and preserves godlike/06
//     SSOT: the row factory in text_track_persistence.go is the
//     SOLE canonical TextTrack builder, called identically from
//     both files.
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
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TextTrackResolver bundles the DB lookup, YouTube subtitle + Whisper
// ports, and the canonical SQL repository behind a single typed
// surface. The 5 field-shape is below the AGENTS.md 8-deps cap and
// matches the canonical port-bundle pattern.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): added
// RequireLanguageCertainty so the policy gate (cfg.Media.Multilingual
// .RequireLanguageCertainty) is plumbed end-to-end. When true,
// AcquireSegmentText fires asset.ErrLanguageUndeterminable pre-Step-9
// if the chain exhausts without surfacing a real BCP-47 language.
type TextTrackResolver struct {
	// Repo is the canonical SQL TextTrackRepository. nil-tolerance
	// matches the rest of the youtube package: a nil Repo skips
	// priority 2 silently (no DB lookup) and SaveMany is a no-op.
	Repo asset.TextTrackRepository
	// Subtitles is the OPTIONAL YouTube subtitle port (priority 3+4).
	// nil -> priority skipped; AcquireSegmentText falls through to
	// Whisper directly. Set at composition in build_bundles_domain_media.go.
	Subtitles youtubeports.SubtitleFetcherPort
	// Transcriber is the OPTIONAL Whisper port (priority 5). nil ->
	// priority skipped; AcquireSegmentText returns (nil, nil) when no
	// other level produced content. The orchestrator calls
	// TranscribeAudioWithDetection (Fase 1.b) via fetchWhisperTranscriptRaw
	// (text_track_whisper.go) so it can apply RequireLanguageCertainty.
	Transcriber youtubeports.WhisperTranscriberPort
	// Log is the canonical zap logger for operator forensics.
	Log *zap.Logger
	// RequireLanguageCertainty, when true, makes AcquireSegmentText
	// surface asset.ErrLanguageUndeterminable pre-Step-9 if the
	// chain exhausts without surfacing a real BCP-47 language (i.e.
	// the bundle's LanguageCode collapses to "und"). Default false
	// preserves the pre-Fase-1.b behavior (silent "und" + degraded
	// track). godlike/07 fail-closed: when this is true the writer
	// (CommitClipTextAndIndexEvent) ALSO surfaces
	// ErrClipLocaleNotReady if a non-und language was never resolved.
	// Plumbed at composition from cfg.Media.Multilingual.RequireLanguageCertainty.
	RequireLanguageCertainty bool
}

// TextTrackAcquireRequest bundles the inputs the 5-level chain needs.
// All fields are mandatory except LocalPath (Whisper fallback is
// only triggered when LocalPath is non-empty AND transcriber != nil)
// and PreferredLanguages (only used by AcquireSegmentText when the
// YouTube subtitle port is wired — the orchestrator filters the
// FetchSegmentSubtitles result to languages in this list before
// returning; the chain still probes ALL subtitles and the orchestrator
// picks the best match).
type TextTrackAcquireRequest struct {
	ClipID       string
	VideoID      string
	StartSec     int
	EndSec       int
	PayloadTexts []youtubetypes.LocalizedClipText
	LocalPath    string
	// PreferredLanguages is the BCP-47 priority list for
	// SubtitleFetcherPort.FetchSegmentSubtitles. The orchestrator
	// passes the FULL list to the port (the port probes them in
	// order); if the port's returned LanguageCode is NOT in this
	// list, the orchestrator falls through to priority 5 (Whisper).
	// Empty slice implies no preference (orchestrator does not
	// filter; the port's own language detection wins). godlike/06
	// SSOT: the list is BCP-47 normalized; the orchestrator does
	// NOT re-normalize caller-supplied codes inline (the caller is
	// expected to plumb cfg.Media.Multilingual.Languages
	// already-normalized).
	PreferredLanguages []string
}

// ResolveOriginal is the canonical "first payload-provided text wins"
// lookup (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026).
//
// The method returns the first LocalizedClipText entry whose
// Transcript is non-empty (via the text_track_payload.go leaf
// resolveRawPayload helper), normalized to a ResolvedTextBundle.
// The LanguageCode is normalized here via the canonical
// asset.Normalize helper — empty input collapses to "und" (BCP-47
// undetermined), never silently to "en" (godlike/07).
//
// Returns (nil, nil) when no payload text has a non-empty
// Transcript. Returns a typed error only on BCP-47 normalization
// failure (which is currently only triggered by empty input — the
// helper collapses empty to "und" without erroring). The
// orchestrator does NOT probe YouTube subtitles or Whisper in this
// method; the full 5-level chain is owned by AcquireSegmentText.
//
// godlike/06 SSOT: this is the SOLE canonical payload-priority
// surface. No handler may re-implement the priority-1 logic inline.
func (r *TextTrackResolver) ResolveOriginal(ctx context.Context, clipID string, payloadTexts []youtubetypes.LocalizedClipText) (*asset.ResolvedTextBundle, error) {
	if r == nil {
		return nil, nil
	}
	t, ok := resolveRawPayload(payloadTexts)
	if !ok {
		return nil, nil
	}
	// Normalize the language code. asset.Normalize collapses empty
	// input to "und" without erroring (canonical godlike/07
	// behavior); malformed input returns an error.
	lang, err := asset.Normalize(t.LanguageCode)
	if err != nil {
		return nil, fmt.Errorf("text_track_resolver.ResolveOriginal: invalid language %q in payload: %w", t.LanguageCode, err)
	}
	if r.Log != nil {
		r.Log.Info("text track resolved from payload (priority 1)",
			zap.String("clip_id", clipID),
			zap.String("language", lang),
			zap.String("source_type", string(sourceOrProvided(t.SourceType))))
	}
	return &asset.ResolvedTextBundle{
		LanguageCode:       lang,
		SourceLanguageCode: sourceLangOf(t, lang, t.IsOriginal),
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

// ResolveLanguage is the canonical "single language + kind lookup"
// method (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026).
//
// Returns the canonical *asset.TextTrack for the given
// (asset, language, kind) triple, READY-only (PENDING/FAILED rows
// are treated as not-found per the Fase 4 video-pipeline contract).
// Returns (nil, nil) when no row exists OR when the language is
// empty/unparseable (caller must surface ErrClipTextTrackNotReady
// rather than passing "und" to downstream consumers).
//
// godlike/06 SSOT: this is the SOLE canonical single-language
// typed lookup. No handler may re-implement the priority-2 logic
// inline.
//
// The language parameter is BCP-47 normalized via the canonical
// asset.Normalize helper (here, NOT in the leaf). Empty input
// collapses to "und" and the orchestrator returns (nil, nil) (the
// orchestrator does NOT substitute "en" — godlike/07).
func (r *TextTrackResolver) ResolveLanguage(ctx context.Context, clipID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	if r == nil || r.Repo == nil {
		return nil, nil
	}
	lang, err := asset.Normalize(languageCode)
	if err != nil {
		return nil, fmt.Errorf("text_track_resolver.ResolveLanguage: invalid language %q: %w", languageCode, err)
	}
	if lang == "und" {
		// Empty/unparseable language code is NOT a hard fail, but
		// the orchestrator does NOT probe the DB with "und" (that
		// would scan all rows for the asset). Caller is responsible
		// for the language-validation gate; we surface (nil, nil).
		return nil, nil
	}
	row, _, err := fetchDatabaseTrackRaw(ctx, r.Repo, clipID, lang, kind)
	if err != nil {
		return nil, fmt.Errorf("text_track_resolver.ResolveLanguage: db: %w", err)
	}
	if row != nil && r.Log != nil {
		r.Log.Info("text track resolved from DB (READY)",
			zap.String("clip_id", clipID),
			zap.String("language", row.LanguageCode),
			zap.String("kind", string(kind)))
	}
	return row, nil
}

// ResolveBestAvailable is the canonical "preferred-languages fan-out"
// lookup (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026).
//
//   - Iterates preferredLanguages in order
//   - Returns the first READY match (ResolveLanguage semantics)
//   - Returns (nil, nil) when no preferred language has a READY
//     row OR when preferredLanguages is empty
//
// godlike/06 SSOT: this is the SOLE canonical preferred-languages
// typed lookup. No handler may re-implement the fan-out logic inline.
//
// Each preferred-language is normalized via asset.Normalize BEFORE
// the DB probe (here, NOT in the leaf — the leaf receives an	// already-canonical BCP-47 code). Malformed entries are SKIPPED
// (not failed) so a partially-valid
// cfg.Media.Multilingual.Languages list doesn't break
// the chain.
func (r *TextTrackResolver) ResolveBestAvailable(ctx context.Context, clipID string, preferredLanguages []string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	if r == nil || r.Repo == nil {
		return nil, nil
	}
	if len(preferredLanguages) == 0 {
		return nil, nil
	}
	for _, raw := range preferredLanguages {
		lang, err := asset.Normalize(raw)
		if err != nil {
			// Skip malformed entries (godlike/07 honest lock:
			// a typo in the config MUST NOT break the chain).
			if r.Log != nil {
				r.Log.Warn("ResolveBestAvailable: skipping malformed preferred language",
					zap.String("clip_id", clipID),
					zap.String("raw", raw),
					zap.Error(err))
			}
			continue
		}
		if lang == "und" {
			// "und" is the known-undetermined marker; not a
			// useful probe target.
			continue
		}
		row, _, err := fetchDatabaseTrackRaw(ctx, r.Repo, clipID, lang, kind)
		if err != nil {
			return nil, fmt.Errorf("text_track_resolver.ResolveBestAvailable: db: %w", err)
		}
		if row != nil {
			if r.Log != nil {
				r.Log.Info("text track resolved via preferred language (priority 2 fan-out)",
					zap.String("clip_id", clipID),
					zap.String("language", row.LanguageCode),
					zap.String("kind", string(kind)))
			}
			return row, nil
		}
	}
	return nil, nil
}

// AcquireSegmentText runs the canonical 5-level chain and returns
// the primary (original-language) ResolvedTextBundle.
//
// Priority order:
//  1. Payload Texts (ResolveOriginal)
//  2. DB READY transcript (ResolveBestAvailable over PreferredLanguages)
//  3. YouTube subtitle manual via Subtitles.FetchSegmentSubtitles
//  4. YouTube subtitle auto (same FetchSegmentSubtitles call — the
//     adapter probes both manual + auto)
//  5. Whisper fallback via Transcriber.TranscribeAudioWithDetection
//     (with DetectedLanguage + Confidence)
//
// Returns (nil, nil) when every priority level fails to produce a
// transcript AND the policy does NOT require certainty. Returns
// (nil, &asset.ErrLanguageUndeterminable{...}) when the chain
// exhausts AND RequireLanguageCertainty=true. Returns (nil, err)
// only when a typed error surfaces (DB error, Whisper error);
// subtitle errors are logged+swallowed so the chain can fall
// through to priority 5.
//
// godlike/06 SSOT: this is the SOLE canonical chain. Handlers MUST
// NOT reimplement priority 3-5 inline (audit 2026-07-11 §2.b).
func (r *TextTrackResolver) AcquireSegmentText(ctx context.Context, req TextTrackAcquireRequest) (*asset.ResolvedTextBundle, error) {
	if r == nil {
		return nil, nil
	}

	// Priority 1: API payload (typed method, calls
	// resolveRawPayload internally).
	bundle, err := r.ResolveOriginal(ctx, req.ClipID, req.PayloadTexts)
	if err != nil {
		return nil, err
	}
	if bundle != nil {
		return bundle, nil
	}

	// Priority 2: DB READY transcript via preferred-languages
	// fan-out (typed method).
	row, err := r.ResolveBestAvailable(ctx, req.ClipID, req.PreferredLanguages, asset.TextTrackTranscript)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return cdbRowToBundle(*row), nil
	}

	// Priority 3 + 4: YouTube subtitle manual + auto. The
	// SubtitleFetcherPort.FetchSegmentSubtitles adapter probes
	// both (manual first then auto fallback) and returns ONE
	// typed bundle. The orchestrator filters the returned
	// LanguageCode against PreferredLanguages before accepting
	// (Fase 1.b: the port surfaces any language it found; the
	// orchestrator's policy is to discard the bundle when its
	// LanguageCode is NOT in PreferredLanguages and fall through
	// to Whisper).
	if r.Subtitles != nil && req.VideoID != "" {
		sub, subErr := r.Subtitles.FetchSegmentSubtitles(ctx, req.VideoID, req.StartSec, req.EndSec)
		if subErr != nil {
			if r.Log != nil {
				r.Log.Warn("subtitle acquisition failed; falling through to Whisper",
					zap.String("clip_id", req.ClipID),
					zap.String("video_id", req.VideoID),
					zap.Error(subErr))
			}
			// Non-fatal: continue to Whisper.
		} else if sub != nil && !sub.IsEmpty() {
			// Filter against PreferredLanguages. If the resolved
			// language is NOT in the caller's list (and the list
			// is non-empty), fall through to Whisper. godlike/07:
			// empty/unparseable LanguageCode on the bundle is
			// treated as "und" and is NOT a match for any
			// non-empty list.
			if len(req.PreferredLanguages) > 0 && !languageInList(sub.LanguageCode, req.PreferredLanguages) {
				if r.Log != nil {
					r.Log.Info("subtitle language not in PreferredLanguages; falling through to Whisper",
						zap.String("clip_id", req.ClipID),
						zap.String("subtitle_language", sub.LanguageCode),
						zap.Strings("preferred", req.PreferredLanguages))
				}
				// Skip; fall through to priority 5.
			} else {
				if r.Log != nil {
					r.Log.Info("text track acquired from YouTube subtitles",
						zap.String("clip_id", req.ClipID),
						zap.String("video_id", req.VideoID),
						zap.String("language", sub.LanguageCode))
				}
				return sub, nil
			}
		}
	}

	// Priority 5: Whisper fallback via TranscribeAudioWithDetection
	// (Fase 1.b). The orchestrator calls fetchWhisperTranscriptRaw
	// (text_track_whisper.go) so it can apply the
	// RequireLanguageCertainty policy gate and normalize the
	// detected language. The legacy TranscribeAudio plain-string
	// method is RETAINED on the port for back-compat with the
	// Step 10 metadata path; the chain uses the typed method.
	if r.Transcriber != nil && req.LocalPath != "" {
		det, wErr := fetchWhisperTranscriptRaw(ctx, r.Transcriber, req.LocalPath)
		if wErr != nil {
			// Apply policy gate: if the policy requires
			// certainty, surface ErrLanguageUndeterminable
			// (Whisper returned a typed error before the chain
			// could surface a language).
			if r.RequireLanguageCertainty {
				if r.Log != nil {
					r.Log.Warn("Whisper returned a typed error and the policy requires language certainty",
						zap.String("clip_id", req.ClipID),
						zap.String("local_path", req.LocalPath),
						zap.Error(wErr))
				}
				return nil, &asset.ErrLanguageUndeterminable{
					AssetID: req.ClipID,
					Reason:  "Whisper returned a typed error before the chain could surface a language; policy requires language certainty",
				}
			}
			return nil, wErr
		}
		if det.Text != "" {
			// Normalize the detected language. The concrete
			// adapter MUST already have done this (per the port
			// contract), but we double-normalize here for
			// defence-in-depth (godlike/07 honest lock).
			lang, nErr := asset.Normalize(det.DetectedLanguage)
			if nErr != nil {
				lang = "und"
			}
			if r.Log != nil {
				r.Log.Info("text track acquired from Whisper (Fase 1.b typed port)",
					zap.String("clip_id", req.ClipID),
					zap.String("local_path", req.LocalPath),
					zap.String("language", lang))
			}
			return &asset.ResolvedTextBundle{
				LanguageCode:       lang,
				SourceLanguageCode: lang,
				PlainText:          det.Text,
				Cues:               nil,
				SourceType:         asset.TextSourceWhisper,
				IsOriginal:         true,
				Provider:           "whisper",
				ModelName:          "",
				ModelVersion:       "",
				Confidence:         det.Confidence,
			}, nil
		}
	}

	// Chain exhausted without surfacing a transcript.
	if r.Log != nil {
		r.Log.Info("text track acquisition exhausted all 5 priorities without finding usable content",
			zap.String("clip_id", req.ClipID))
	}
	if r.RequireLanguageCertainty {
		return nil, &asset.ErrLanguageUndeterminable{
			AssetID: req.ClipID,
			Reason:  "all 5 chain levels exhausted without surfacing a real BCP-47 language; policy requires language certainty",
		}
	}
	return nil, nil
}

// languageInList reports whether lang is in the preferred list.
// The comparison is BCP-47-normalized (godlike/06 SSOT — the
// orchestrator never re-implements normalization rules inline;
// both sides of the comparison run asset.Normalize here).
//
// godlike/07: an empty input or an input that normalizes to "und"
// returns false (no match) so a malformed subtitle-language code
// does not silently fall into the "matches" branch.
func languageInList(lang string, preferred []string) bool {
	norm, err := asset.Normalize(lang)
	if err != nil || norm == "und" {
		return false
	}
	for _, p := range preferred {
		pn, perr := asset.Normalize(p)
		if perr != nil {
			continue
		}
		if pn == norm {
			return true
		}
	}
	return false
}
