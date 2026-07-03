// Package drive — uploader_put.go (P0 #1 fix, June 2026; Wave B1+B2, June 2026)
//
// *Uploader.PutFile is the conflict-aware single entry point for Drive
// uploads. It supersedes the pre-refactor UploadFileWithDescription
// method on the Publisher's FileUploaderPort (see publisher.go).
//
// Routing table (ConflictPolicy → action):
//
//	ConflictOverwrite:
//	  - existing match   → Files.Update with Media  → PutActionUpdated
//	  - no match         → Files.Create with Media  → PutActionCreated
//	ConflictSkip:
//	  - existing match   → return existing metadata → PutActionSkipped
//	                        (NO upload performed)
//	  - no match         → Files.Create with Media  → PutActionCreated
//	ConflictRename:
//	  - existing match   → Files.Create with new name (timestamp suffix)
//	                                              → PutActionRenamed
//	  - no match         → Files.Create with Media  → PutActionCreated
//
// P1.1 (July 2026): ConflictOverwrite is no longer the iota-zero
// default — that role moved to ConflictPolicyUnset, which the
// publisher resolves to a registry-driven value before calling
// this seam. The routing table above assumes a non-Unset policy;
// direct PutFile callers MUST NOT pass ConflictPolicyUnset (see
// Publisher.Step0 in publisher.go for the registry-default path).
//
// Retries on transient Drive errors (429, 503, timeouts, network blips)
// via pkg/retry — same exponential backoff policy (3 attempts, 2s → 4s)
// as the legacy UploadFileWithDescription path.
//
// Lookup step (FindFileByName) follows TWO fail-closed rules applied
// in order (Wave B1 + Wave B2, June 2026):
//
//  1. Wave B1 — lookup *error* (rate-limit, timeout, 403, malformed
//     query, ctx.Canceled) is wrapped via fmt.Errorf %w and returned
//     hard. pkg/retry's IsRetryable predicate recognises the inner
//     transient signal and retries; non-retryable errors short-circuit
//     immediately. The previous "warn-and-fall-through" behaviour was
//     a silent-duplicate-create bug: a transient lookup failure
//     produced `existing == nil`, which then took the Create branch
//     on the next line — yielding a Drive file duplicate.
//
//  2. Wave B2 — lookup *ambiguity* (more than one non-trashed match
//     in the same parent folder) is also fail-closed: the typed
//     sentinel ErrAmbiguousDriveFile is returned wrapped with the
//     filename. The pre-Wave B2 first-match truncation silently
//     hid this case, which made overwrite/skip non-deterministic
//     when users manually uploaded sibling copies. Callers can
//     errors.Is(err, drive.ErrAmbiguousDriveFile) to detect the
//     ambiguous-state case specifically (vs a generic lookup error).
//
// The P0 #4 fix on findOrCreateFolder is orthogonal and lands in a
// separate commit.
package drive

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ErrConflictPolicyUnresolved is the typed sentinel a caller of
// PutFile receives when it bypasses the publisher-side registry-
// default path (Publisher.Step0 in publisher.go) and forwards a
// raw ConflictPolicyUnset value into the uploader seam. The publisher
// resolves Unset to a per-destination registry default BEFORE calling
// PutFile, so production never reaches this branch — the guard is
// defense-in-depth so a future admin / migration / test surface that
// constructs PutFileRequest{} directly cannot silently fall through
// to "Plain Create" and lose the registry-driven safety contract
// (P1.1, July 2026). Callers that genuinely want "fresh Drive file on
// collision" must pass ConflictOverwrite explicitly.
var ErrConflictPolicyUnresolved = errors.New("drive: PutFile received ConflictPolicyUnset — caller bypassed Publisher.Step0")

// PutFile is the conflict-aware upload entry point. Implements
// FileUploaderPort.PutFile (used by delivery.Publisher) and
// drive.Admin.PutFile (used by application adapters). Returns a
// structured PutFileResult with an explicit Action so callers do not
// have to infer success semantics from a boolean-style return value.
//
// Lookups use the same FindFileByName surface as the legacy
// path. Reusing the existing helper keeps behaviour consistent and
// avoids re-implementing the query-construction logic.
//
// The retry wrapper covers Create / Update operations AND
// the lookup step (Wave B1 + Wave B2, June 2026). FindFileByName is
// re-run on each retry attempt because race conditions between
// attempt N and attempt N+1 are real — a sibling worker may have
// created the file in between. Lookup errors are FAIL-CLOSED on
// TWO axes: (a) lookup *error* (Wave B1) wrapped via fmt.Errorf %w
// so pkg/retry's IsRetryable predicate surfaces the inner retryable
// signal; non-retryable lookups short-circuit immediately and yield
// the typed error to the caller. (b) lookup *ambiguity* (Wave B2) —
// more than one non-trashed match in the same parent — surfaces
// ErrAmbiguousDriveFile wrapped with the filename; pre-Wave B2 the
// first-match truncation silently hid this case.
//
// P1.1 (July 2026): PutFile rejects ConflictPolicyUnset with the
// typed sentinel ErrConflictPolicyUnresolved. Publisher.Step0
// resolves Unset BEFORE the uploader is called, so production never
// reaches this branch — the guard exists as defense-in-depth so a
// future caller that bypasses Publisher cannot silently fall through
// to "Plain Create" and lose the registry-driven safety contract.
// Callers who genuinely want "create a fresh Drive file on collision"
// must pass ConflictOverwrite explicitly (Overwrite + no-existing
// match → Files.Create, the same end-state Unset would have
// produced here, but explicit).
func (u *Uploader) PutFile(ctx context.Context, req PutFileRequest) (*PutFileResult, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(req.LocalPath) == "" {
		return nil, fmt.Errorf("putFile: local path is required")
	}
	if strings.TrimSpace(req.FolderID) == "" {
		return nil, fmt.Errorf("putFile: folder id is required")
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("putFile: filename is required")
	}
	// P1.1 defense-in-depth: ConflictPolicyUnset (= 0, the iota
	// sentinel for "the caller did not pick a policy") MUST NOT
	// reach the uploader. Publisher.Step0 resolves Unset to a
	// registry-driven value before calling us, so production
	// cannot land here. The guard is a typed-sentinel fail-closed
	// gate for any future caller that bypasses Publisher (e.g. a
	// migration / admin / test surface that constructs
	// PutFileRequest{} directly), paralleling the Wave B1 + Wave B2
	// lookup fail-closed axes. Callers that genuinely want a fresh
	// Drive file on collision must pass ConflictOverwrite
	// explicitly.
	if req.ConflictPolicy == delivery.ConflictPolicyUnset {
		return nil, fmt.Errorf("putFile: %w", ErrConflictPolicyUnresolved)
	}

	result, err := retry.DoWithValue(ctx, func() (*PutFileResult, error) {
		// Step 1: lookup existing. FAIL-CLOSED on BOTH axes (Wave B1 + Wave B2).
		//   - lookup *error* (Wave B1): wrapped via fmt.Errorf %w so
		//     pkg/retry's IsRetryable predicate surfaces the inner
		//     transient signal (429/503/timeout) and retries; non-
		//     retryable errors (context.Canceled, 403 forbidden,
		//     malformed query) short-circuit immediately. The previous
		//     "warn-and-proceed" path was the silent-duplicate-create
		//     bug.
		//   - lookup *ambiguity* (Wave B2): if more than one match
		//     comes back, surface ErrAmbiguousDriveFile wrapped with
		//     the filename. The pre-Wave B2 first-match truncation
		//     silently hid this case, which made overwrite/skip
		//     non-deterministic on sibling copies.
		lookup, lookupErr := lookupFunc(u, ctx, req.FolderID, req.Filename)
		if lookupErr != nil {
			return nil, fmt.Errorf("putFile: lookup existing file %q: %w", req.Filename, lookupErr)
		}
		if len(lookup.Matches) > 1 {
			return nil, fmt.Errorf("putFile: lookup existing file %q: %w", req.Filename, ErrAmbiguousDriveFile)
		}
		var existing *RemoteFile
		if len(lookup.Matches) == 1 {
			existing = &lookup.Matches[0]
		}
		return u.doPutFile(ctx, req, existing)
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		IsRetryable:    retry.IsTransient,
		OnRetry: func(attempt int, err error) {
			u.Log.Warn("transient drive put error, retrying",
				zap.String("filename", req.Filename),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("drive put failed after 3 attempts: %w", err)
	}
	return result, nil
}

// doPutFile performs the actual Files.Create / Files.Update / skip
// branch based on ConflictPolicy + lookup match. Split out from PutFile
// so the retry wrapper can re-invoke it (FindFileByName is re-run on
// each attempt because race conditions between attempt N and attempt
// N+1 are real — a sibling worker may have created the file in between).
func (u *Uploader) doPutFile(ctx context.Context, req PutFileRequest, existing *RemoteFile) (*PutFileResult, error) {
	// ConflictSkip on existing match: short-circuit, no upload, return
	// existing metadata. This is the only branch where the local file
	// is NOT opened.
	if req.ConflictPolicy == delivery.ConflictSkip && existing != nil && existing.FileID != "" {
		return &PutFileResult{
			FileID:       existing.FileID,
			WebViewLink:  existing.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + existing.FileID,
			MD5Checksum:  existing.MD5Checksum,
			Action:       PutActionSkipped,
		}, nil
	}

	// ConflictSkip without existing match: log explicit so callers
	// reading the audit trail see "skip requested but created" rather
	// than a silent fall-through (P0 #1 #Q2 action-vs-policy mismatch).
	if req.ConflictPolicy == delivery.ConflictSkip {
		u.Log.Info("putFile: skip requested but no existing match; creating (no-op skip)",
			zap.String("filename", req.Filename),
			zap.String("folder_id", req.FolderID))
	}

	f, err := openFile(req.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	// ConflictOverwrite on existing match: drive Files.Update (which
	// preserves the file's ID and version history when keepRevisionForever
	// is unset on the file, matching legacy semantics).
	if req.ConflictPolicy == delivery.ConflictOverwrite && existing != nil && existing.FileID != "" {
		updateFile := &driveapi.File{}
		if req.Description != "" {
			updateFile.Description = req.Description
		}
		updated, err := u.Service.Files.Update(existing.FileID, updateFile).
			Fields("id,webViewLink,md5Checksum").
			Media(f).
			Context(ctx).
			Do()
		if err != nil {
			return nil, fmt.Errorf("drive put (update %q): %w", req.Filename, err)
		}
		return &PutFileResult{
			FileID:       updated.Id,
			WebViewLink:  updated.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + updated.Id,
			MD5Checksum:  updated.Md5Checksum,
			Action:       PutActionUpdated,
		}, nil
	}

	// ConflictRename on existing match: Create with timestamp suffix.
	// Drive's API does not provide auto-rename on filename collision
	// (multiple files with the same name + parent get distinct IDs),
	// and visually-duplicate entries are a UI antipattern, so manual
	// suffixing is the canonical workaround. UnixNano is the canonical
	// burst-protection timestamp: UnixSeconds would collide between
	// two renames within the same second (rare but observable) and
	// produce a Drive 409 (not retryable per pkg/retry's isRetryable
	// predicate — only 429/503/timeouts qualify).
	if req.ConflictPolicy == delivery.ConflictRename && existing != nil && existing.FileID != "" {
		newName := renameWithTimestamp(req.Filename, time.Now().UnixNano())
		file := &driveapi.File{Name: newName}
		if req.Description != "" {
			file.Description = req.Description
		}
		file.Parents = []string{req.FolderID}
		created, err := u.Service.Files.Create(file).
			Fields("id,webViewLink,md5Checksum").
			Media(f).
			Context(ctx).
			Do()
		if err != nil {
			return nil, fmt.Errorf("drive put (rename-create %q): %w", newName, err)
		}
		u.Log.Info("putFile: renamed to avoid collision",
			zap.String("original", req.Filename),
			zap.String("renamed", newName),
			zap.String("file_id", created.Id),
		)
		return &PutFileResult{
			FileID:       created.Id,
			WebViewLink:  created.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + created.Id,
			MD5Checksum:  created.Md5Checksum,
			Action:       PutActionRenamed,
		}, nil
	}

	// Plain Create: covers (a) all policies with no existing match,
	// and (b) ConflictRename/ConflictSkip with no existing match.
	file := &driveapi.File{Name: req.Filename}
	if req.Description != "" {
		file.Description = req.Description
	}
	file.Parents = []string{req.FolderID}
	created, err := u.Service.Files.Create(file).
		Fields("id,webViewLink,md5Checksum").
		Media(f).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("drive put (create %q): %w", req.Filename, err)
	}
	return &PutFileResult{
		FileID:       created.Id,
		WebViewLink:  created.WebViewLink,
		DownloadLink: "https://drive.google.com/uc?id=" + created.Id,
		MD5Checksum:  created.Md5Checksum,
		Action:       PutActionCreated,
	}, nil
}

// renameWithTimestamp inserts a UnixNano suffix into the filename
// preserving the extension. Example:
//
//	clip.mp4               → clip_1719612345123456789.mp4
//	complex.name.v2.tar.gz → complex.name.v2_1719612345123456789.tar.gz
func renameWithTimestamp(name string, ts int64) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == "" {
		return fmt.Sprintf("%s_%d", base, ts)
	}
	return fmt.Sprintf("%s_%d%s", base, ts, ext)
}

// keep zap + retry imported — both used by the PutFile + doPutFile
// bodies above. The Package import references are intentional and
// survive compiler-side import pruning.
var _ = zap.NewNop
var _ retry.Options

// lookupFunc is the test seam for *Uploader.PutFile. Production
// code delegates to Uploader.FindFileByName; tests inject overrides
// to simulate lookup failures and ambiguous-match responses without
// spinning up a fake Drive HTTP server. Mirrors the openFile seam
// in uploader.go (line ~188) and TestOpenFileInjection in
// uploader_test.go.
//
// Wave B2 (June 2026) changed the return type from (*RemoteFile, error)
// to (ExistingFileLookup, error) so the seam can carry the full match
// set including the >1 ambiguity case. The default implementation
// still delegates to Uploader.FindFileByName — the signature change
// is the only thing that needs to propagate.
var lookupFunc = func(u *Uploader, ctx context.Context, folderID, filename string) (ExistingFileLookup, error) {
	return u.FindFileByName(ctx, folderID, filename)
}
