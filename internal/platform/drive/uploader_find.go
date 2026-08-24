// Package drive — uploader_find.go: Drive file-existence lookup methods.
//
// 2026-07-06 (Pattern 5 split): extracted from uploader.go. Owns the
// canonical file-existence lookup methods (FindFileByName, FindFileByIdempotencyKey)
// plus the helper lookupExisting that routes between them.
package drive

import (
	"context"
	"fmt"
	"strings"
)

// FindFileByName returns ALL non-trashed files in a folder with the
// given name. Pre-Wave B2 (June 2026) this returned the first match
// only — silently truncating the second/third/... matches, which made
// overwrite/skip non-deterministic when multiple files shared the
// same name+parent (e.g. a user manually uploaded a sibling copy and
// the pipeline uploaded another).
//
// Wave B2 makes the surface exhaustive: callers MUST branch on
// len(ExistingFileLookup.Matches) to distinguish 0/1/>1 matches per
// the routing table documented on the ExistingFileLookup type. The
// zero-value ExistingFileLookup (len(Matches) == 0) is the canonical
// "no match" surface, matching the pre-Wave B2 (nil, nil) return
// contract semantically.
//
// The >1 case is NOT signalled here — FindFileByName returns all
// matches; it is the CALLER's job to detect len > 1 and surface
// ErrAmbiguousDriveFile (fail-closed). This split is intentional:
// the port method is a pure read, while the ambiguous-state error is
// a policy decision owned by the caller.
func (u *Uploader) FindFileByName(ctx context.Context, folderID, filename string) (ExistingFileLookup, error) {
	if u.Service == nil {
		return ExistingFileLookup{}, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" || strings.TrimSpace(filename) == "" {
		return ExistingFileLookup{}, nil
	}

	query := fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false", strings.ReplaceAll(filename, "'", "\\'"), folderID)
	list, err := u.Service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink, md5Checksum)").
		Context(ctx).
		Do()
	if err != nil {
		return ExistingFileLookup{}, err
	}

	lookup := ExistingFileLookup{Matches: make([]RemoteFile, 0, len(list.Files))}
	for _, file := range list.Files {
		lookup.Matches = append(lookup.Matches, RemoteFile{
			FileID:      file.Id,
			Name:        file.Name,
			WebViewLink: file.WebViewLink,
			MD5Checksum: file.Md5Checksum,
		})
	}
	return lookup, nil
}

// FindFileByIdempotencyKey (P0.6, July 2026) returns all non-trashed
// files in folderID whose Drive appProperties contain
// pipelinegen_idempotency_key=<idemKey>. This is the canonical
// lookup surface for idempotent Drive publication — same key → same
// file, regardless of filename.
//
// The Drive API query uses the `appProperties` search operator:
//
//	appProperties has {key='pipelinegen_idempotency_key' and value='<key>'}
//	and trashed=false and '<folderID>' in parents
//
// Len(Matches) == 0 means no existing file with this idempotency key
// (equivalent to "create fresh"). Len(Matches) > 1 is a corrupted
// state (two files share the same idempotency key — the caller MUST
// fail-closed with ErrAmbiguousDriveFile, as for filename ambiguity).
func (u *Uploader) FindFileByIdempotencyKey(ctx context.Context, folderID, idemKey string) (ExistingFileLookup, error) {
	if u.Service == nil {
		return ExistingFileLookup{}, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" || strings.TrimSpace(idemKey) == "" {
		return ExistingFileLookup{}, nil
	}

	query := fmt.Sprintf(
		"appProperties has {key='pipelinegen_idempotency_key' and value='%s'} and trashed=false and '%s' in parents",
		strings.ReplaceAll(idemKey, "'", "\\'"),
		folderID,
	)
	list, err := u.Service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink, md5Checksum, appProperties)").
		Context(ctx).
		Do()
	if err != nil {
		return ExistingFileLookup{}, err
	}

	lookup := ExistingFileLookup{Matches: make([]RemoteFile, 0, len(list.Files))}
	for _, file := range list.Files {
		lookup.Matches = append(lookup.Matches, RemoteFile{
			FileID:      file.Id,
			Name:        file.Name,
			WebViewLink: file.WebViewLink,
			MD5Checksum: file.Md5Checksum,
		})
	}
	return lookup, nil
}
