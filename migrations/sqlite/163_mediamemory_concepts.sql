-- 163_mediamemory_concepts.sql — Fase 1.2 media_concepts SSOT.
--
-- godlike/06 SSOT (one canonical owner per fact): this is the
-- SOLE DDL for the media_concepts table. The corresponding row-
-- to-struct conversion lives in
-- internal/infrastructure/database/sqlite/mediamemory/concepts_repository.go.
--
-- godlike/07 NO-FAKE-AVAILABILITY: UNIQUE(language,
-- phrase_fingerprint) is the canonical SSOT for "same phrase +
-- same language" equality. Duplicate inserts surface as wrapped
-- ErrDuplicateBinding at the repository envelope.
--
-- concept_type is the canonical closed enum
-- (phrase|entity|person|location|event|action|object|topic|emotion);
-- see internal/application/mediamemory/types.go::IsKnownConceptType.
--
-- embedding_version is nullable: empty until the Phase-2 indexer
-- first upserts the concept. Bumping the version invalidates the
-- PhraseFingerprint cache cleanly (the visual_intent_version stamp
-- ties together so old Level-0 lookups stay valid for as long as
-- the DDL shape is unchanged).

CREATE TABLE IF NOT EXISTS media_concepts (
    id                  TEXT     PRIMARY KEY,
    canonical_text      TEXT     NOT NULL,
    language            TEXT     NOT NULL,
    normalized_text     TEXT     NOT NULL,
    phrase_fingerprint  TEXT     NOT NULL,
    concept_type        TEXT     NOT NULL,
    embedding_version   TEXT,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    UNIQUE(language, phrase_fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_media_concepts_language
    ON media_concepts(language);

CREATE INDEX IF NOT EXISTS idx_media_concepts_type
    ON media_concepts(concept_type);

CREATE INDEX IF NOT EXISTS idx_media_concepts_embedding_version
    ON media_concepts(embedding_version);
