-- database: primary
-- Migration 194: content_objects — CAS content identity registry.
--
-- CAS design (August 2026): the SHA-256 of the bytes is the PRIMARY KEY of
-- every immutable content object. Logical media_assets reference these
-- objects through content_sha256; multiple logical assets with identical
-- bytes share ONE physical object (global byte deduplication).
--
-- The physical blob itself lives in the content-addressed store
-- (<dataDir>/blobs/sha256/XX/<hash>); this table is the registry of what
-- exists, where it is (storage_uri), and whether it has been verified
-- against the stored digest (integrity_status + verified_at).
--
-- Invariant (canonical): filename / URL / Drive folder DO NOT establish
-- identity. SHA-256 establishes byte identity. Objects are immutable.
--
-- Idempotency: CREATE TABLE IF NOT EXISTS + partial index only; re-apply on
-- an existing schema is a no-op.

CREATE TABLE IF NOT EXISTS content_objects (
    sha256           TEXT PRIMARY KEY,
    size_bytes       INTEGER NOT NULL,
    mime_type        TEXT,
    storage_uri      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    verified_at      TEXT,
    integrity_status TEXT NOT NULL
);

-- Partial index for the CAS integrity scanner (cas-verify): scan only
-- objects that are not yet marked VERIFIED.
CREATE INDEX IF NOT EXISTS idx_content_objects_integrity_status
    ON content_objects(integrity_status)
    WHERE integrity_status != 'VERIFIED';
