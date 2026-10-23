// Package scan — percheck_clip_folders_writer_canonical is the
// forward-prevention gate that bans direct SQL writes to `clip_folders`
// from ANY package outside the canonical folder writers.
//
// MEDIA-SSOT folder projection (POSTGRES-MEDIA-CUTOVER, September 2026):
// catalog sync reads ListFolders from the PostgreSQL clip_folders projection
// (internal/platform/postgres/media.FolderRepository). The operational SQLite
// row stays the write-through owner and mirrors every mutation into that
// projection through the imagesregistry.FolderProjection port.
//
// The split is only sound while EVERY folder write passes through the
// canonical writer. A raw `INSERT/UPDATE/DELETE clip_folders` executed
// anywhere else silently desynchronises the projection: the folder then never
// appears in the PostgreSQL list, so pruneMissingFolders can never reconcile
// it, and the row survives forever in one store only. Four such writers used
// to exist in cmd/admin (sync-outros, reset-video-ai, reset-stock-subfolders,
// list-drive-folder); this gate exists so they cannot come back.
//
// CANONICAL OWNERSHIP IS BY PACKAGE, NOT BY FILENAME, resolved through
// policy.IsCanonicalClipFolderWriter — the same construction as the
// media_assets fence, for the same anti-drift reason.
//
// matched rule_id: `percheck_clip_folders_writer_canonical`.
package boundaries

// ScanClipFoldersWriterCanonical walks internal/ + cmd/ and inspects every
// non-test Go file for direct SQL writes to clip_folders. Canonical owners are
// exempt (resolved through policy.IsCanonicalClipFolderWriter).
//
// God comments are masked before matching, so a doc block that quotes the
// legacy statement is not reported as a live write.

// inspectClipFoldersWriterFile scans one Go file for forbidden clip_folders
// writes. Canonical owners, the scanner package itself, the SQL migration
// trees and the closed test-only support list are exempt.
