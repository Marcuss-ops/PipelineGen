// Package drive — folder_lookup.go: folder-existence lookup types, constants, and helpers.
//
// 2026-07-06 (Pattern 5 split): extracted from folder_manager.go. Owns the
// canonical folder-lookup surface: the seam type (folderLookupFunc), the
// production-retry policy constants, the Drive Files.List query builder,
// and the production-default lookup + firstFolderID extractors.
package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// folderLookupFunc is the seam through which findOrCreateFolder
// resolves "is there already a Drive folder with this name under
// parent?".
//
// The contract:
//   - (id, nil):       folder exists; id is the existing folder ID
//   - ("", nil):       folder does not exist; caller MUST create
//   - ("", err):       lookup failed; findOrCreateFolder propagates err
//     WITHOUT falling through to Create (P0.4 fix)
//
// Production wiring wraps the SDK's Files.List call with pkg/retry
// (3 attempts, ±30% jitter, 200ms initial backoff, IsRetryable gating
// on transient errors). Tests inject a stub via WithLookup to drive
// transient / retry-failure / non-retryable failure modes.
//
// P0.4 (June 2026): the pre-fix implementation called Files.List
// directly and fell through to Files.Create on ANY error, producing
// duplicate folders on Drive when a transient error masked an
// existing-folder match. The seam makes that race structurally
// impossible because the contract puts the "does it exist?" decision
// in a single retry-aware function.
type folderLookupFunc func(ctx context.Context, parent, name string) (id string, err error)

// folderReLookupFunc is the P0.7 seam for post-create duplicate
// detection. Production wiring performs a full Files.List query
// (no retry — the creation just succeeded so Drive is
// known-reachable) and returns count + oldestID. Tests inject a
// stub via WithReLookup.
//
// The contract:
//   - (0, "", nil):           no matching folders (unexpected; treat as ok)
//   - (1, oldestID, nil):     exactly one match (the one we just created)
//   - (>1, oldestID, nil):    duplicate detected → ErrAmbiguousDriveFolder
//   - (0, "", err):           re-lookup failed (transient); return created.ID
//     defensively (same policy as uploader_ops.go Stage 3)
type folderReLookupFunc func(ctx context.Context, parent, name string) (count int, oldestID string, err error)

// folderLookupRetry* constants are the P0.4 production retry policy
// for the lookup pre-create step. Tighter than upload/download
// (200ms vs 2s) because the lookup is a lightweight metadata query
// (1 quota unit) — upload/download backoff must accommodate multi-MB
// content delivery.
const (
	folderLookupInitialBackoff = 200 * time.Millisecond
	folderLookupMaxBackoff     = 2 * time.Second
	folderLookupMaxAttempts    = 3
	folderLookupJitterFraction = 0.3
)

// buildFolderLookupQuery returns the canonical Drive Files.List query
// for folder-existence checks. P0-1 (July 2026): extracted from the
// three duplicated query-construction sites (newDefaultFolderLookup,
// newAdminDefaultLookup, lookupFolderExact) into a single SSOT helper.
//
// The query matches non-trashed folders with the exact name, optionally
// scoped to a parent folder when non-empty. Callers MUST pre-sanitize
// the name — this helper does NOT apply CleanFolderName / SafeFolderName.
// Single quotes in the name are escaped via SQL-style backslash.
func buildFolderLookupQuery(parent, name string) string {
	queryParts := []string{
		fmt.Sprintf("name = '%s'", strings.ReplaceAll(name, "'", "\\'")),
		"trashed = false",
		"mimeType = 'application/vnd.google-apps.folder'",
	}
	if parent != "" {
		queryParts = append(queryParts, fmt.Sprintf("'%s' in parents", parent))
	}
	return strings.Join(queryParts, " and ")
}

// lookupFolderCanonical returns the production-default folderLookupFunc
// wiring the SDK's Files.List through pkg/retry with the P0.4 spec
// (retry-with-jitter on transient errors, abort-on-error-after-retry).
//
// P0-1 (July 2026): this is the canonical SSOT for the folder-existence
// query + retry + firstFolderID pipeline. The three previously-
// duplicated implementations (newDefaultFolderLookup, newAdminDefaultLookup,
// lookupFolderExact) now delegate to this single function.
//
// The closure captures (svc, log) so the seam-signature stays context-only.
// Tests that need to inject custom lookup behaviour use WithLookup;
// this default is what production code runs.
func lookupFolderCanonical(svc *driveapi.Service, log *zap.Logger) folderLookupFunc {
	return func(ctx context.Context, parent, name string) (string, error) {
		if svc == nil {
			return "", fmt.Errorf("drive service not configured")
		}
		query := buildFolderLookupQuery(parent, name)

		id, err := retry.DoWithValue(ctx, func() (string, error) {
			list, lerr := svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
			if lerr != nil {
				return "", retry.WrapTransient(lerr)
			}
			return firstFolderID(list)
		}, retry.Options{
			MaxAttempts:    folderLookupMaxAttempts,
			InitialBackoff: folderLookupInitialBackoff,
			MaxBackoff:     folderLookupMaxBackoff,
			BackoffFactor:  2.0,
			JitterFraction: folderLookupJitterFraction,
			IsRetryable:    retry.IsTransient,
			OnRetry: func(attempt int, err error) {
				if log != nil {
					log.Warn("transient drive list error, retrying (P0.4: no fallback-to-create)",
						zap.String("folder_name", name),
						zap.Int("attempt", attempt+1),
						zap.Error(err))
				}
			},
		})
		if err != nil {
			return "", fmt.Errorf("lookup existing folder %q under %q: %w", name, parent, err)
		}
		return id, nil
	}
}

// newDefaultFolderLookup returns the production-default folderLookupFunc.
// P0-1 (July 2026): delegates to the canonical lookupFolderCanonical SSOT.
func newDefaultFolderLookup(svc *driveapi.Service, log *zap.Logger) folderLookupFunc {
	return lookupFolderCanonical(svc, log)
}

// firstFolderID returns the single folder ID from a Drive List result.
// Returns ("", nil) when the list is nil/empty ("doesn't exist").
// Returns ("", ErrAmbiguousDriveFolder) when more than one non-trashed
// folder matches (fail-closed — never silently pick the first match on
// ambiguous state). The P0.8 upgrade mirrors the ErrAmbiguousDriveFile
// pattern already active for files (uploader.go:FindFileByName returns
// ALL matches; the caller detects len > 1).
//
// Used by newDefaultFolderLookup (folder_manager.go) and
// newAdminDefaultLookup (admin.go) to map a *driveapi.FileList to
// the seam's (id, error) return value.
func firstFolderID(list *driveapi.FileList) (string, error) {
	if list == nil || len(list.Files) == 0 {
		return "", nil
	}
	if len(list.Files) > 1 {
		return "", ErrAmbiguousDriveFolder
	}
	return list.Files[0].Id, nil
}
