-- database: primary
-- Migration 173: Backfill metadata_json.provider_tags for Artlist rows ingested
-- before commit e819dc9 (feat(asset): separate tag provenance into provider,
-- vlm, manual, transcript fields). The new commit wired ProviderTags on the
-- Asset struct (internal/domain/asset/asset_types.go:42) and the Artlist
-- mapper populates clip.Metadata["provider_tags"] at ingestion time
-- (internal/application/assets/providers/artlist/service.go:610, 685). Pre-
-- e819dc9 Artlist rows still carry provenance only in the flat `tags` column.
--
-- Forward: copy tags JSON to provider_tags when key is missing.
-- Idempotency: provider_tags IS NULL OR 'null' guard makes re-run a no-op.
-- Cutover/contract (follow-up): flatten provider_tags back into the retiring
-- flat `tags` column after consumers migrate.
--
-- Rollback (manual, do not auto-apply):
--   UPDATE media_assets
--   SET metadata_json = json_remove(metadata_json, '$.provider_tags')
--   WHERE source = 'artlist';
--
-- AGENTS.md: applies only to the database owning media_assets (database:
-- primary); follows the canonical expand-then-backfill pattern.

UPDATE media_assets
SET metadata_json = json_set(
    COALESCE(metadata_json, '{}'),
    '$.provider_tags',
    COALESCE(json(tags), '[]')
)
WHERE source = 'artlist'
  AND json_type(tags) = 'array'
  AND json_array_length(tags) > 0
  AND (
        json_extract(metadata_json, '$.provider_tags') IS NULL
        OR  json_extract(metadata_json, '$.provider_tags') = 'null'
      );
