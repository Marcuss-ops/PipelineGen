-- 170_mediamemory_bindings_provider.sql — Fase 4.3 binding
-- provenance column.
--
-- godlike/06 SSOT (one canonical DDL home per column): this
-- file is the SOLE place the provider column lands. The
-- application-level canonical is in
-- internal/application/mediamemory/types.go::MediaBinding.Provider.
-- The SQL ↔ Go row-scan lives in
-- internal/infrastructure/database/sqlite/mediamemory/bindings_repository.go.
--
-- godlike/06 SSOT (binding provenance contract): every binding
-- row now carries the canonical Provider tag from the candidate
-- that produced it. Default 'local' backfills existing rows
-- (pre-Fase-4.3 bindings get 'local' since they were created
-- by the canonical dashboard manual-editor path). External
-- SearchFanOut providers (artlist, youtube, pexels) are
-- translucent handoffs and are recorded verbatim into this
-- column when the linker worker writes the binding.
--
-- godlike/07 NO-FAKE-AVAILABILITY: NOT NULL DEFAULT 'local'
-- means a missing-value write is impossible — the ranker
-- always sees a non-empty provider tag and deriveLayerProvider
-- never has to handle the empty-string path.

ALTER TABLE media_bindings
    ADD COLUMN provider TEXT NOT NULL DEFAULT 'local';

CREATE INDEX IF NOT EXISTS idx_media_bindings_provider
    ON media_bindings(provider);