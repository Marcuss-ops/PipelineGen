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
//   - The canonical BCP-47 normalization rules live in
//     internal/domain/asset/bcp47.go. This resolver calls
//     bcp47.Normalize and NEVER re-derives the rules inline
//     (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026).
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
	// nil → priority skipped; AcquireSegmentText falls through to
	// Whisper directly. Set at composition in build_bundles_domain_media.go.
	Subtitles youtubeports.SubtitleFetcherPort
	// Transcriber is the OPTIONAL Whisper port (priority 5). nil →
	// priority skipped; AcquireSegmentText returns (nil, nil) when no
	// other level produced content. The resolver calls
	// TranscribeAudioWithDetection (Fase 1.b) so it can apply the
	// RequireLanguageCertainty policy gate.
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
	// ErrClipLocaleNotReady if a non-und language was never
	// resolved. Plumbed at composition from
	// cfg.Media.Multilingual.RequireLanguageCertainty.
	RequireLanguageCertainty bool
}

// TextTrackAcquireRequest bundles the inputs the 5-level chain needs.
// All fields are mandatory except LocalPath (Whisper fallback is
// only triggered when LocalPath is non-empty AND transcriber != nil)
// and PreferredLanguages (only used by AcquireSegmentText when the
// YouTube subtitle port is wired — the resolver filters the
// FetchSegmentSubtitles result to languages in this list before
// returning; the chain still probes ALL subtitles and the resolver
// picks the best match).
type TextTrackAcquireRequest struct {
	ClipID       string
	VideoID      string
	StartSec     int
	EndSec       int
	PayloadTexts []youtubetypes.LocalizedClipText
	LocalPath    string
	// PreferredLanguages is the BCP-47 priority list for
	// SubtitleFetcherPort.FetchSegmentSubtitles. The resolver
	// passes the FULL list to the port (the port probes them in
	// order); if the port's returned LanguageCode is NOT in this
	// list, the resolver falls through to priority 5 (Whisper).
	// Empty slice ⇒ no preference (resolver does not filter; the
	// port's own language detection wins). godlike/06 SSOT: the
	// list is BCP-47 normalized; the resolver does NOT
	// re-normalize caller-supplied codes inline (the caller is
	// expected to plumb cfg.Media.Multilingual.MaterializeLanguages
	// already-normalized).
	PreferredLanguages []string
}

// ResolveOriginal is the canonical "first payload-provided text wins"
// lookup (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026). It
// replaces the legacy 2-level Resolve method's payload-only
// priority-1 logic with a typed result.
//
// The method returns the first LocalizedClipText entry whose
// Transcript is non-empty, projected to a ResolvedTextBundle. The
// LanguageCode is normalized via the canonical bcp47.Normalize
// helper — empty input collapses to "und" (BCP-47 undetermined),
// never silently to "en" (godlike/07).
//
// Returns (nil, nil) when no payload text has a non-empty
// Transcript. Returns a typed error only on BCP-47 normalization
// failure (which is currently only triggered by empty input —
// the helper collapses empty to "und" without erroring). The
// resolver does NOT probe YouTube subtitles or Whisper in this
// method; the full 5-level chain is owned by AcquireSegmentText.
//
// godlike/06 SSOT: this is the SOLE canonical payload-priority
// surface. No handler may re-implement the priority-1 logic
// inline.
func (r *TextTrackResolver) ResolveOriginal(ctx context.Context, clipID string, payloadTexts []youtubetypes.LocalizedClipText) (*asset.ResolvedTextBundle, error) {
	if r == nil {
		return nil, nil
	}
	for i := range payloadTexts {
		t := payloadTexts[i]
		if t.Transcript == "" {
			continue
		}
		// Normalize the language code. bcp47.Normalize collapses
		// empty input to "und" without erroring (canonical godlike/07
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
	return nil, nil
}

// ResolveLanguage is the canonical "single language + kind lookup"
// method (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026). It
// replaces the legacy Find(ctx, "en", TextTrackTranscript) hardcoded
// pattern (audit 2026-07-11 §2.c).
//
// Returns the canonical *asset.TextTrack for the given
// (asset, language, kind) triple, READY-only (PENDING/FAILED rows
// are treated as not-found per the Fase 4 video-pipeline
// contract). Returns (nil, nil) when no row exists OR when the
// language is empty/unparseable (caller must surface
// ErrClipTextTrackNotReady rather than passing "und" to
// downstream consumers).
//
// godlike/06 SSOT: this is the SOLE canonical single-language
// typed lookup. No handler may re-implement the priority-2 logic
// inline (e.g. a `Find(lang, kind)` then in-memory status check).
//
// The language parameter is BCP-47 normalized via the canonical
// bcp47.Normalize helper. Empty input collapses to "und" and
// the resolver returns (nil, nil) (the resolver does NOT
// substitute "en" — godlike/07).
func (r *TextTrackResolver) ResolveLanguage(ctx context.Context, clipID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	if r == nil || r.Repo == nil {
		return nil, nil
	}
	// Normalize the requested language. Empty → "und"; malformed
	// returns a typed error (the resolver never silently substitutes
	// "en" — godlike/07).
	lang, err := asset.Normalize(languageCode)
	if err != nil {
		return nil, fmt.Errorf("text_track_resolver.ResolveLanguage: invalid language %q: %w", languageCode, err)
	}
	if lang == "und" {
		// Empty/unparseable language code is NOT a hard fail, but
		// the resolver does NOT probe the DB with "und" (that would
		// scan all rows for the asset). Caller is responsible for
		// the language-validation gate; we surface (nil, nil).
		return nil, nil
	}
	row, _, err := r.Repo.FindReady(ctx, clipID, lang, kind)
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
// lookup (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026). It
// replaces the legacy AcquireSegmentText priority-2 logic with a
// typed method that:
//   - Iterates preferredLanguages in order
//   - Returns the first READY match (ResolveLanguage semantics)
//   - Returns (nil, nil) when no preferred language has a READY
//     row OR when preferredLanguages is empty
//
// godlike/06 SSOT: this is the SOLE canonical preferred-languages
// typed lookup. No handler may re-implement the fan-out logic
// inline.
//
// Each preferred-language is normalized via the canonical
// bcp47.Normalize helper before the DB probe. Malformed entries
// are SKIPPED (not failed) so a partially-valid
// cfg.Media.Multilingual.MaterializeLanguages list doesn't break
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
		row, _, err := r.Repo.FindReady(ctx, clipID, lang, kind)
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

// AcquireSegmentText runs the canonical 5-level chain and returns the
// primary (original-language) ResolvedTextBundle. PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 1.b (July 2026): the chain is now driven by the three
// typed resolver methods (ResolveOriginal, ResolveBestAvailable,
// priority 3+4 Subtitles, priority 5 WhisperWithDetection); the
// policy gate (RequireLanguageCertainty) fires
// asset.ErrLanguageUndeterminable pre-Step-9 when the chain
// exhausts without surfacing a real BCP-47 language.
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
// exhausts AND RequireLanguageCertainty=true. Returns
// (nil, err) only when a typed error surfaces (DB error, Whisper
// error); subtitle errors are logged+swallowed so the chain can
// fall through to priority 5.
//
// godlike/06 SSOT: this is the SOLE canonical chain. Handlers MUST
// NOT reimplement priority 3-5 inline (audit 2026-07-11 §2.b).
func (r *TextTrackResolver) AcquireSegmentText(ctx context.Context, req TextTrackAcquireRequest) (*asset.ResolvedTextBundle, error) {
	if r == nil {
		return nil, nil
	}

	// Priority 1: API payload (typed method).
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
	// SubtitleFetcherPort.FetchSegmentSubtitles adapter probes both
	// (manual first then auto fallback) and returns ONE typed bundle.
	// The resolver filters the returned LanguageCode against
	// PreferredLanguages before accepting (Fase 1.b: the port
	// surfaces any language it found; the resolver's policy is
	// to discard the bundle when its LanguageCode is NOT in
	// PreferredLanguages and fall through to Whisper).
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
			// Filter against PreferredLanguages. If the
			// resolved language is NOT in the caller's list
			// (and the list is non-empty), fall through to
			// Whisper. godlike/07: empty/unparseable
			// LanguageCode on the bundle is treated as "und"
			// and is NOT a match for any non-empty list.
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
	// (Fase 1.b). The new port method surfaces DetectedLanguage +
	// Confidence so the resolver can apply the
	// RequireLanguageCertainty policy gate. The legacy
	// TranscribeAudio plain-string method is RETAINED on the port
	// for back-compat with the Step 10 metadata path (and
	// Service.TranscribeAudio shim); the chain uses the typed
	// method exclusively.
	if r.Transcriber != nil && req.LocalPath != "" {
		det, wErr := r.Transcriber.TranscribeAudioWithDetection(ctx, req.LocalPath)
		if wErr != nil {
			// Apply policy gate: if the policy requires
			// certainty, surface ErrLanguageUndeterminable
			// (Whisper returned a typed error before the
			// chain could surface a language).
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
			// adapter MUST already have done this (per the
			// port contract), but we double-normalize here
			// for defence-in-depth (godlike/07 honest lock).
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

// languageInList reports whether lang is in the preferred list. The
// comparison is BCP-47-normalized (godlike/06 SSOT — the resolver
// never re-implements normalization rules inline).
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
	lang, err := asset.Normalize(languageCode)
	if err != nil {
		return fmt.Errorf("text_track_resolver.Save: invalid language %q: %w", languageCode, err)
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
//
// Fase 1.b: language codes are normalized via bcp47.Normalize
// before the row is built (godlike/07: empty input collapses to
// "und" without error).
func (r *TextTrackResolver) MaterializePayloadTexts(clipID string, texts []youtubetypes.LocalizedClipText) []asset.TextTrack {
	var out []asset.TextTrack
	for _, t := range texts {
		rawLang := t.LanguageCode
		lang, err := asset.Normalize(rawLang)
		if err != nil {
			// Skip the entire entry on a malformed language
			// code (godlike/07 honest lock: a typo in the
			// payload MUST NOT poison the batch).
			if r.Log != nil {
				r.Log.Warn("MaterializePayloadTexts: skipping entry with invalid language",
					zap.String("clip_id", clipID),
					zap.String("raw", rawLang),
					zap.Error(err))
			}
			continue
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
//     here (not a hard fail); the resolver emits a Warn with the
//     dropped count so operators can trace which clips are losing
//     cues (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5.c logger plumb,
//     July 2026).
//   - The bundle's LanguageCode is normalized to BCP-47 ("und"
//     when empty — mirroring bundleToTextTracks).
//
// Method receiver (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5.c): the
// resolver's *zap.Logger is plumbed end-to-end so the dropped-cue
// diagnostic is emitted via r.Log.Warn instead of being lost. The
// resolver already had a Log field (Fase 1.b); this is the first
// in-package method to consume it. Free-function callers (none
// remain in this package after the conversion) would lose the
// diagnostic silently.
func (r *TextTrackResolver) bundleToTimedTrack(clipID string, bundle *asset.ResolvedTextBundle) *localized.TimedTextTrack {
	if r == nil {
		return nil
	}
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
	dropped := 0
	for _, c := range bundle.Cues {
		if c.StartMs < 0 || c.EndMs < c.StartMs || c.Text == "" {
			// Skip invalid cues rather than blow up the bundle.
			// godlike/07 honest lock: a malformed upstream cue
			// MUST NOT poison the whole super-tx; the resolver
			// drops it loudly and surfaces the count via
			// r.Log.Warn so operators can trace which clips are
			// losing cues (Fase 5.c logger plumb).
			dropped++
			continue
		}
		cues = append(cues, c)
	}
	if dropped > 0 && r.Log != nil {
		r.Log.Warn("bundleToTimedTrack: dropped malformed cues",
			zap.String("clip_id", clipID),
			zap.Int("dropped", dropped))
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
