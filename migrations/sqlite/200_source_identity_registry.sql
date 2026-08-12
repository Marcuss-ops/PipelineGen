-- Migration 200: source_identity_registry
--
-- CAS design (August 2026): before downloading, the acquisition flow asks
-- "do we already know what bytes this source resolves to?". This table
-- remembers the source -> content SHA-256 mapping so a repeat acquisition
-- of the same Drive file / Artlist asset / URL can be served from the CAS
-- store WITHOUT hitting the network.
--
-- Invariant: SHA-256 establishes byte identity; the source identity is
-- metadata used only to avoid redundant downloads. A source row MAY be
-- updated to a new digest when the provider content changes (version/etag
-- bump) — source identity is NOT immutable, content objects ARE.
--
-- source_type  : drive | artlist | url | youtube | manual
-- source_key   : Drive file ID, Artlist asset ID, canonical URL, ...
-- content_sha256 : sha256 of the bytes the source resolves to
-- source_version : provider etag / modified_time / version when exposed
CREATE TABLE source_identity_registry (
    source_type         TEXT NOT NULL,
    source_key          TEXT NOT NULL,
    content_sha256      TEXT NOT NULL,
    source_version      TEXT NOT NULL DEFAULT '',
    discovered_at       TEXT NOT NULL,
    last_seen_at        TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'UNVERIFIED',
    PRIMARY KEY (source_type, source_key)
);

-- Reverse lookup: every content object with a known source (dedup audit,
-- provenance reporting, integrity sweep).
CREATE INDEX idx_source_identity_content
    ON source_identity_registry(content_sha256);
