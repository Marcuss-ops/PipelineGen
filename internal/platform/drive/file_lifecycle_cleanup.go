// Package drive — file_lifecycle_cleanup.go: bulk-cleanup file lifecycle operation.
//
// 2026-07-06 (Pattern 5 split): extracted from file_lifecycle.go. Owns the
// CleanupRequest (structured filter for Drive bulk-trash operations), its
// buildQuery helper, and the FileLifecycleAdapter.Cleanup method (paginated
// Files.List + per-file Trash with partial-failure reporting).
package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// CleanupRequest is the structured request for FileLifecycle.Cleanup
// (Wave D D2, June 2026). All fields are optional (zero value = no
// filter on that dimension) but at least one filter MUST be set —
// a request with all-zero fields would match every non-trashed file
// on Drive (a Drive-wide wipe) and is rejected upfront with a typed
// error so a misconfigured caller never accidentally trashes the
// whole Drive.
type CleanupRequest struct {
	ParentFolderID string
	Name           string
	MimeType       string
	OlderThan      time.Time
}

// buildQuery constructs the Drive Files.List Q string from the
// CleanupRequest. Always includes "trashed = false" so the loop never
// re-processes already-trashed entries. Escapes single quotes in
// user-supplied strings (Drive query syntax requires it for literal
// apostrophes inside quoted values).
func (req CleanupRequest) buildQuery() (string, error) {
	if strings.TrimSpace(req.ParentFolderID) == "" &&
		strings.TrimSpace(req.Name) == "" &&
		strings.TrimSpace(req.MimeType) == "" &&
		req.OlderThan.IsZero() {
		return "", fmt.Errorf("cleanup: at least one filter is required (ParentFolderID, Name, MimeType, or OlderThan)")
	}
	parts := []string{"trashed = false"}
	if req.ParentFolderID != "" {
		parts = append(parts, fmt.Sprintf("'%s' in parents", strings.ReplaceAll(req.ParentFolderID, "'", "\\'")))
	}
	if req.Name != "" {
		parts = append(parts, fmt.Sprintf("name = '%s'", strings.ReplaceAll(req.Name, "'", "\\'")))
	}
	if req.MimeType != "" {
		parts = append(parts, fmt.Sprintf("mimeType = '%s'", strings.ReplaceAll(req.MimeType, "'", "\\'")))
	}
	if !req.OlderThan.IsZero() {
		parts = append(parts, fmt.Sprintf("modifiedTime < '%s'", req.OlderThan.UTC().Format(time.RFC3339)))
	}
	return strings.Join(parts, " and "), nil
}

// Cleanup bulk-trashes files matching the structured CleanupRequest,
// paginating exhaustively via nextPageToken. The "trashed = false"
// filter is always included so the loop never re-processes
// already-trashed entries (Trash is idempotent, so re-trashing is
// harmless but wastes quota). Partial failures during the loop are
// logged AND surfaced in the returned CleanupResult.FailedIDs so
// callers can retry or audit them.
//
// Wave D (June 2026) D2: signature changed from
// `Cleanup(ctx, query string) (int, error)` to
// `Cleanup(ctx, req CleanupRequest) (int, error)`. The Drive query
// is built from the request via CleanupRequest.buildQuery.
//
// Wave D (June 2026) D3: return type changed to
// `(CleanupResult, error)`. The Matched counter is bumped BEFORE
// the Trash attempt so callers can distinguish "found N but failed
// to trash all of them" from "found N and trashed all of them"
// without re-issuing a Files.List.
func (a *FileLifecycleAdapter) Cleanup(ctx context.Context, req CleanupRequest) (CleanupResult, error) {
	query, err := req.buildQuery()
	if err != nil {
		return CleanupResult{FailedIDs: []string{}}, err
	}
	if a.svc == nil {
		return CleanupResult{FailedIDs: []string{}}, fmt.Errorf("drive service not configured")
	}

	result := CleanupResult{FailedIDs: []string{}}
	var pageToken string
	for {
		listReq := a.svc.Files.List().Q(query).
			Fields("nextPageToken, files(id)").
			Context(ctx)
		if pageToken != "" {
			listReq = listReq.PageToken(pageToken)
		}
		res, err := listReq.Do()
		if err != nil {
			return result, fmt.Errorf("cleanup list (page=%q): %w", pageToken, err)
		}
		if res == nil || len(res.Files) == 0 {
			if res == nil {
				return result, fmt.Errorf("cleanup: %w", ErrDriveListNil)
			}
			return result, nil
		}
		for _, f := range res.Files {
			result.Matched++
			if err := a.Trash(ctx, f.Id); err != nil {
				a.log.Warn("cleanup: failed to trash file",
					zap.String("file_id", f.Id),
					zap.String("query", query),
					zap.Error(err))
				result.Failed++
				result.FailedIDs = append(result.FailedIDs, f.Id)
				continue
			}
			result.Trashed++
		}
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}
	return result, nil
}
