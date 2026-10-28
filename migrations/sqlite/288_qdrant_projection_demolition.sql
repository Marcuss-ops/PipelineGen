-- database: primary
-- Migration 288: demolish the residual Qdrant registry tables.
--
-- WHY. Qdrant is no longer a media store (PostgreSQL + pgvector is the media
-- SSOT; the media-demolition wave of September 2026 retired the projection).
-- The projection registry tables survived as pure schema residue: zero rows in
-- every live database and ZERO code readers/writers in the Go tree (the only
-- references were this migration corpus, one boot fixture and the audit
-- reachability model). Data-layer unification says Qdrant may remain ONLY
-- outside the media domain, with residual usage explicitly justified — an
-- empty registry inside the primary media plane is neither.
--
-- WHAT. Drop the four registry tables and the derived status view. Every
-- index they own disappears with them. The historical CREATE TABLE migrations
-- (098/110/111) stay in the corpus: the ledger + the schema verifier treat a
-- DROP that post-dates the CREATE as a deliberate absence.
DROP VIEW IF EXISTS qdrant_collections_status_v1;
DROP TABLE IF EXISTS qdrant_cleanup_audit;
DROP TABLE IF EXISTS qdrantprojection_dlq;
DROP TABLE IF EXISTS qdrantprojection_checkpoints;
DROP TABLE IF EXISTS qdrant_collections;
