-- database: primary
-- Migration 290: fold legacy entity-type prefixes in durable semantic rows.
--
-- canonical_entity_id used to be minted from the extractor's own type spelling,
-- so one place could be stored as gpe:, location:, city: or country: and the
-- identity never joined. The identity owner
-- (internal/capabilities/entities) now folds every spelling through ONE alias
-- table (type_aliases.go), so NEW rows are written canonically. This migration
-- is the one-time backfill for rows already written: it rewrites ONLY the type
-- prefix and preserves the slug verbatim (SafeEntityID never changed, so the
-- slug is not recomputed here).
--
-- Idempotent: the WHERE clause matches only legacy spellings, so re-running it
-- over an already-folded database updates nothing.
--
-- NOT folded here: entity_image_catalog_entities is PERSON-only by schema
-- (CHECK canonical_entity_id LIKE 'person:%'), and the per-entity Drive folder
-- is derived from CanonicalEntitySlug, which never carried the type — so no
-- folder rename is required for this fold.
--
-- The alias list is FROZEN at this migration's version: a future alias added to
-- the Go table needs its own forward migration, because an applied migration's
-- SQL must never be edited.

UPDATE script_semantic_items
SET canonical_entity_id = lower(
        CASE upper(substr(canonical_entity_id, 1, instr(canonical_entity_id, ':') - 1))
            -- People
            WHEN 'PER' THEN 'PERSON'
            WHEN 'HUMAN' THEN 'PERSON'
            WHEN 'CEO' THEN 'PERSON'
            WHEN 'FOUNDER' THEN 'PERSON'
            WHEN 'PRESIDENT' THEN 'PERSON'
            -- Organizations
            WHEN 'ORGANIZATION' THEN 'ORG'
            WHEN 'ORGANISATION' THEN 'ORG'
            WHEN 'COMPANY' THEN 'ORG'
            WHEN 'CORP' THEN 'ORG'
            WHEN 'CORPORATION' THEN 'ORG'
            WHEN 'BUSINESS' THEN 'ORG'
            WHEN 'INSTITUTION' THEN 'ORG'
            WHEN 'AGENCY' THEN 'ORG'
            WHEN 'NGO' THEN 'ORG'
            WHEN 'GOVERNMENT' THEN 'ORG'
            WHEN 'UNIVERSITY' THEN 'ORG'
            WHEN 'TEAM' THEN 'ORG'
            -- Places
            WHEN 'GPE' THEN 'PLACE'
            WHEN 'LOC' THEN 'PLACE'
            WHEN 'LOCATION' THEN 'PLACE'
            WHEN 'CITY' THEN 'PLACE'
            WHEN 'COUNTRY' THEN 'PLACE'
            WHEN 'NATION' THEN 'PLACE'
            WHEN 'REGION' THEN 'PLACE'
            WHEN 'STATE' THEN 'PLACE'
            WHEN 'PROVINCE' THEN 'PLACE'
            WHEN 'TOWN' THEN 'PLACE'
            WHEN 'VILLAGE' THEN 'PLACE'
            WHEN 'LANDMARK' THEN 'PLACE'
            WHEN 'SITE' THEN 'PLACE'
            WHEN 'GEO' THEN 'PLACE'
            WHEN 'ADDRESS' THEN 'PLACE'
            WHEN 'FAC' THEN 'PLACE'
            WHEN 'FACILITY' THEN 'PLACE'
            -- Products, brands and editorial kinds
            WHEN 'BRAND' THEN 'LOGO'
            WHEN 'WORK' THEN 'WORK_OF_ART'
            WHEN 'ARTWORK' THEN 'WORK_OF_ART'
            -- Value kinds
            WHEN 'DATETIME' THEN 'DATE'
            WHEN 'CALENDAR_DATE' THEN 'DATE'
            WHEN 'NUM' THEN 'NUMBER'
            WHEN 'CARDINAL' THEN 'NUMBER'
            WHEN 'ORDINAL' THEN 'NUMBER'
            WHEN 'QUANTITY' THEN 'NUMBER'
            WHEN 'METRIC' THEN 'NUMBER'
            WHEN 'METRICS' THEN 'NUMBER'
            WHEN 'STATISTIC' THEN 'NUMBER'
            WHEN 'STATISTICS' THEN 'NUMBER'
            WHEN 'COUNT' THEN 'NUMBER'
            WHEN 'CURRENCY' THEN 'MONEY'
            WHEN 'PERCENTAGE' THEN 'PERCENT'
            -- Concepts
            WHEN 'MISC' THEN 'CONCEPT'
            WHEN 'NORP' THEN 'CONCEPT'
            WHEN 'LAW' THEN 'CONCEPT'
            WHEN 'LANGUAGE' THEN 'CONCEPT'
            WHEN 'IDEA' THEN 'CONCEPT'
            WHEN 'VISUAL_CONCEPT' THEN 'CONCEPT'
            ELSE upper(substr(canonical_entity_id, 1, instr(canonical_entity_id, ':') - 1))
        END
    ) || substr(canonical_entity_id, instr(canonical_entity_id, ':'))
WHERE instr(canonical_entity_id, ':') > 1
  AND upper(substr(canonical_entity_id, 1, instr(canonical_entity_id, ':') - 1)) IN (
    'PER', 'HUMAN', 'CEO', 'FOUNDER', 'PRESIDENT',
    'ORGANIZATION', 'ORGANISATION', 'COMPANY', 'CORP', 'CORPORATION', 'BUSINESS',
    'INSTITUTION', 'AGENCY', 'NGO', 'GOVERNMENT', 'UNIVERSITY', 'TEAM',
    'GPE', 'LOC', 'LOCATION', 'CITY', 'COUNTRY', 'NATION', 'REGION', 'STATE',
    'PROVINCE', 'TOWN', 'VILLAGE', 'LANDMARK', 'SITE', 'GEO', 'ADDRESS', 'FAC', 'FACILITY',
    'BRAND', 'WORK', 'ARTWORK',
    'DATETIME', 'CALENDAR_DATE', 'NUM', 'CARDINAL', 'ORDINAL', 'QUANTITY',
    'METRIC', 'METRICS', 'STATISTIC', 'STATISTICS', 'COUNT', 'CURRENCY', 'PERCENTAGE',
    'MISC', 'NORP', 'LAW', 'LANGUAGE', 'IDEA', 'VISUAL_CONCEPT'
  );
