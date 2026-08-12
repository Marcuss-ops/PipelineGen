// Package e2e — Text Track pipeline E2E tests (Passo 10, July 2026).
//
// Hermetic end-to-end tests verifying the full text track pipeline:
//  1. TextTrackResolver resolves transcript from payload Texts[] (Whisper NOT called)
//  2. asset_text_tracks rows created in SQLite
//  3. Qdrant receives multilingual search text via PayloadMapper
//  4. source_version hash changes when text tracks are added
//  5. Backfill: existing clips can get text tracks added retroactively
//
// Uses the canonical e2e fixture stack: in-memory SQLite + mock Qdrant
// REST surrogate + production adapters. TextTrackRepository is wired
// into the PayloadMapper via SetTextTrackQuerier + SetIndexLanguages.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the legacy
// 2-level `Resolver.Resolve(ctx, clipID, payloadTexts)` method
// (which returned a result envelope with Found/Transcript/LanguageCode/
// Source) is RETIRED. The typed `ResolveOriginal` (priority 1) +
// `ResolveBestAvailable` (priority 2) are the SOLE canonical surfaces.
// The migration is mechanical: result.Found → bundle != nil,
// result.Transcript → bundle.PlainText, result.LanguageCode →
// bundle.LanguageCode, result.Source → bundle.SourceType. The
// Save signature (ctx, clipID, transcript, source, languageCode)
// is UNCHANGED.
package e2e
