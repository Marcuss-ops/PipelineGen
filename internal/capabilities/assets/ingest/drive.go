package ingest

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

// resolveDriveFolder returns the (folderID, path) tuple for a given
// ingest request.
//
// PR-WAVE-1-DRIVE-SSOT (July 2026): the legacy `s.driveAdmin drive.Admin`
// reference and the dependency on driveutil.EnsureFolderPath are
// REMOVED entirely. The DeletionService / ingest.Service canonical
// ingest path is:
//
//	(a) Caller-supplied explicit folder_id / folder_path on the
//	    request wins (returned verbatim). This is the canonical
//	    operator-supplied override path.
//	(b) No folder_id supplied → return ("", "") so downstream
//	    lifecycle.Publisher resolves the folder at upload time
//	    via the canonical DestinationRegistry + PathBuilder
//	    pipeline (godlike/06 SSOT one-canonical-owner-per-fact).
//
// Pre-fix behaviour: ingest pre-resolved the folder on Drive
// (recursive GetOrCreateFolder walk) via s.driveAdmin. That
// produced a write-then-reconcile flow with a dueling-owners
// violation (ingest wrote folders; publisher wrote folders; both
// surface different identities in differentiator logs). Post-fix
// the folder creation is centralised at Publisher.Publish — single
// canonical owner, idempotent on retry, deterministic via
// DestinationKey + path-builder segments.
func (s *Service) resolveDriveFolder(_ context.Context, kind Kind, _rootFolderID string, req *Request) (string, string, error) {
	if strings.TrimSpace(req.FolderID) != "" {
		return strings.TrimSpace(req.FolderID), strings.TrimSpace(req.FolderPath), nil
	}
	zap.L().Debug("ingest.resolveDriveFolder: deferring folder resolution to publisher.Publish (godlike/06 SSOT)",
		zap.String("kind", string(kind)))
	return "", "", nil
}
