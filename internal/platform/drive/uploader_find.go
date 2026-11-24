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
	"time"
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
	return u.lookupFolderIndex(ctx, folderID, filename)
}

// folderIndexTTL bounds how long one folder listing is reused. One second-class
// window is enough to collapse a run's upload burst (the audit window issued
// 5.724 per-file lookups) while keeping a sibling process's new file visible
// within a minute.
const folderIndexTTL = time.Minute

// folderIndexEntry is the memoized listing of one folder, keyed by name so a
// per-file lookup is a map read.
type folderIndexEntry struct {
	fetchedAt time.Time
	files     map[string][]RemoteFile
}

// lookupFolderIndex answers a name lookup from the folder listing, listing the
// folder only when the entry is missing or stale (WST-01). The single-name
// query it replaces was one API call per upload plus the same retry/429
// exposure; a listing is one call per folder per TTL.
func (u *Uploader) lookupFolderIndex(ctx context.Context, folderID, filename string) (ExistingFileLookup, error) {
	if matches, ok := u.cachedFolderMatches(folderID, filename); ok {
		return ExistingFileLookup{Matches: matches}, nil
	}
	files, err := u.listFolderFiles(ctx, folderID)
	if err != nil {
		return ExistingFileLookup{}, err
	}
	index := make(map[string][]RemoteFile, len(files))
	for _, file := range files {
		index[file.Name] = append(index[file.Name], file)
	}
	u.indexMu.Lock()
	if u.folderIndexCache == nil {
		u.folderIndexCache = make(map[string]folderIndexEntry)
	}
	u.folderIndexCache[folderID] = folderIndexEntry{fetchedAt: time.Now(), files: index}
	matches := append([]RemoteFile(nil), index[filename]...)
	u.indexMu.Unlock()
	return ExistingFileLookup{Matches: matches}, nil
}

// cachedFolderMatches returns a copy of the matches for filename when the
// folder entry is still fresh. The copy keeps the cache immutable to callers.
func (u *Uploader) cachedFolderMatches(folderID, filename string) ([]RemoteFile, bool) {
	u.indexMu.Lock()
	defer u.indexMu.Unlock()
	entry, ok := u.folderIndexCache[folderID]
	if !ok || time.Since(entry.fetchedAt) >= folderIndexTTL {
		return nil, false
	}
	return append([]RemoteFile(nil), entry.files[filename]...), true
}

// rememberFolderFile folds OUR write into the fresh index, so the artifacts
// uploaded earlier in the same run are found without another listing. A stale
// or missing entry is left alone: a partial index would hide sibling files.
func (u *Uploader) rememberFolderFile(folderID string, file RemoteFile) {
	if strings.TrimSpace(folderID) == "" || strings.TrimSpace(file.Name) == "" {
		return
	}
	u.indexMu.Lock()
	defer u.indexMu.Unlock()
	entry, ok := u.folderIndexCache[folderID]
	if !ok || time.Since(entry.fetchedAt) >= folderIndexTTL {
		return
	}
	kept := make([]RemoteFile, 0, len(entry.files[file.Name])+1)
	for _, existing := range entry.files[file.Name] {
		if existing.FileID != file.FileID {
			kept = append(kept, existing)
		}
	}
	entry.files[file.Name] = append(kept, file)
}

// listFolderFiles lists every non-trashed file in the folder, following
// pagination. It is the ONLY folder-wide read the index performs.
func (u *Uploader) listFolderFiles(ctx context.Context, folderID string) ([]RemoteFile, error) {
	query := fmt.Sprintf("'%s' in parents and trashed = false", strings.ReplaceAll(folderID, "'", "\\'"))
	files := make([]RemoteFile, 0, 64)
	pageToken := ""
	for {
		call := u.Service.Files.List().
			Q(query).
			Fields("nextPageToken, files(id, name, webViewLink, md5Checksum)").
			PageSize(1000).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		list, err := call.Do()
		if err != nil {
			return nil, err
		}
		for _, file := range list.Files {
			files = append(files, RemoteFile{
				FileID:      file.Id,
				Name:        file.Name,
				WebViewLink: file.WebViewLink,
				MD5Checksum: file.Md5Checksum,
			})
		}
		pageToken = list.NextPageToken
		if pageToken == "" {
			return files, nil
		}
	}
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
