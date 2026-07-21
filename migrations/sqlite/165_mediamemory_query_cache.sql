-- 165_mediamemory_query_cache.sql — Fase 1.2 media_query_cache SSOT.
--
-- godlike/06 SSOT (one canonical owner per fact): this is the
-- SOLE DDL for the media_query_cache table. Architecture doc
-- section 13 explicitly distinguishes this cache from the
-- PipelineGen script-generation cache (different fingerprints,
-- different invariants, different ownership).
--
-- godlike/07 NO-FAKE-AVAILABILITY: hit_count monotonically
-- increments; expires_at drives a TTL pass (composition-root
-- sweeper deletes expired rows; no fake availability on stale
-- entries).
--
-- request_json + result_json are TEXT columns carrying the
-- canonical envelopes (see
-- internal/application/mediamemory/types.go::ResolveRequest +
-- ResolveResult). JSON columns are intentionally TEXT (not
-- SQLite's JSON1 type) — the canonical type lives in the
-- application layer; the row only stores the marshalled bytes.

CREATE TABLE IF NOT EXISTS media_query_cache (
    id                  TEXT     PRIMARY KEY,
    phrase_fingerprint  TEXT     NOT NULL,
    language            TEXT     NOT NULL,
    request_json        TEXT     NOT NULL,
    result_json         TEXT     NOT NULL,
    provider_state_json TEXT,
    hit_count           INTEGER  NOT NULL DEFAULT 0,
    expires_at          DATETIME,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    UNIQUE(phrase_fingerprint)
);

-- godlike/06 SSOT: UNIQUE(phrase_fingerprint) is the canonical
-- dedup invariant. The application-layer PhraseFingerprint is
--     SHA256(language + ":" + normalizedText + ":" + visual_intent_version)
-- so two phrases in different languages ALREADY produce
-- different fingerprints — UNIQUE on the single column is
-- sufficient to prevent cross-language overwrites. A row
-- insert that violates the SSOT is a programmatic error and
-- surfaces as wrapped ErrDuplicateBinding (godlike/07).

CREATE INDEX IF NOT EXISTS idx_media_query_cache_fingerprint
    ON media_query_cache(phrase_fingerprint);

CREATE INDEX IF NOT EXISTS idx_media_query_cache_expiration
    ON media_query_cache(expires_at);
