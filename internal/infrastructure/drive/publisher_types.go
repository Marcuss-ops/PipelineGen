// Package drive — publisher_types.go: composition-time typed contracts for the Publisher.
//
// 2026-07-06 (Pattern 5 split): extracted from publisher.go. Owns the canonical
// fail-fast sentinels, Pattern 0 port interfaces, PutAction/Request/Result types,
// and ResolvedDriveDestination. These are shared across publisher.go,
// publisher_resolve.go, and all callers that wire the uploader seam.
package drive

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// ── Composition-time fail-fast sentinels (P0 #3, June 2026) ────────────────

// ErrMissingDestinationRegistry is the fail-fast sentinel when the
// composition root hands a nil DestinationRegistry to NewPublisher.
var ErrMissingDestinationRegistry = errors.New("drive: NewPublisher: DestinationRegistry dependency is required (composition-time fail-fast)")

// ErrMissingFolderManager is the fail-fast sentinel when the
// FolderManagerPort dependency is nil.
var ErrMissingFolderManager = errors.New("drive: NewPublisher: FolderManagerPort dependency is required (composition-time fail-fast)")

// ErrMissingFileUploader is the fail-fast sentinel when the
// FileUploaderPort dependency is nil.
var ErrMissingFileUploader = errors.New("drive: NewPublisher: FileUploaderPort dependency is required (composition-time fail-fast)")

// FolderManagerPort is the narrow port for creating nested Drive folders.
// Satisfied by *DriveFolderManagerAdapter.EnsureFolder.
//
// P1.3 (July 2026): adds ProbeFolderAccess(ctx, rootID) for the
// composition-time StartupDriveRootsValidator.
type FolderManagerPort interface {
	EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error)
	ProbeFolderAccess(ctx context.Context, rootID string) error
}

// PutAction describes what the uploader actually did on Drive.
type PutAction string

const (
	PutActionCreated PutAction = "created" // fresh Drive file (no existing match)
	PutActionUpdated PutAction = "updated" // existing Drive file updated in place
	PutActionSkipped PutAction = "skipped" // existing Drive file preserved (ConflictSkip; no upload performed)
	PutActionRenamed PutAction = "renamed" // new Drive file with conflict-rename suffix
)

// PutFileRequest is the single low-level op the Publisher must route
// conflict-aware uploads through.
type PutFileRequest struct {
	LocalPath      string
	FolderID       string
	Filename       string
	Description    string                  // optional; empty means "no description"
	ConflictPolicy delivery.ConflictPolicy // zero = ConflictOverwrite (legacy default)
	IdempotencyKey string                  // P0.6: Drive appProperties key for conflict detection
	ContentHash    string                  // P0.6: hex-encoded SHA-256 of file content
	SourceVersion  int64                   // P0.6: logical source version

	// ExpectedSize (PR-CLIPINGEST-PIPELINE step 9, July 2026) is the
	// pre-computed local-file size. When non-zero, the uploader
	// threads it into VerificationParams.ExpectedSize and the
	// post-upload verifier rejects uploads whose Drive-side size
	// does not match. Zero = skip the size-match check.
	//
	// Distinct from ContentHash: ContentHash is the canonical
	// identity fingerprint (used for IdempotencyKey + Drive
	// appProperties); ExpectedSize is a hard upload-integrity
	// signal at the post-upload verification gate. The two are
	// complementary; the user-spec literal "Upload verificato per
	// size+checksum" calls for BOTH. Callers that pre-compute only
	// the size leave ExpectedSHA256 empty (skip the content-match).
	ExpectedSize int64

	// ExpectedSHA256 (PR-CLIPINGEST-PIPELINE step 9, July 2026) is
	// the pre-computed local-file SHA-256 hex digest. When
	// non-empty, the uploader threads it into
	// VerificationParams.ExpectedSHA256 and the post-upload
	// verifier downloads the Drive-side file + computes its
	// SHA-256 to compare. Mismatch surfaces the typed sentinel
	// ErrDriveFileSHA256Mismatch. Empty = skip the content-match
	// check (back-compat for callers that don't pre-compute
	// SHA-256). The content-match requires a Files.Get +
	// Download round-trip, which costs bandwidth — callers
	// that want only the cheaper size-match leave this empty.
	ExpectedSHA256 string

	// PublicRead is required for assets that Google Docs must fetch directly
	// from Drive (for example entity images inserted as inline images).
	PublicRead bool
}

// PutFileResult is the structured return value.
type PutFileResult struct {
	FileID string
	// Filename is the actual Drive name, including any conflict-rename
	// suffix. It lets post-upload verification validate the name without
	// comparing a renamed upload to the original requested name.
	Filename     string
	WebViewLink  string
	DownloadLink string
	MD5Checksum  string
	Action       PutAction
}

// FileUploaderPort is the narrow port for uploading files to Drive.
// Satisfied by *Uploader.PutFile (see uploader_put.go).
type FileUploaderPort interface {
	PutFile(ctx context.Context, req PutFileRequest) (*PutFileResult, error)
}

// CatalogFolderLookup is the narrow port for consulting the local
// drive_folder_catalog before making Drive API calls (DoD item 6,
// SEMANTIC-LOCATION-API-2026-07-06).
type CatalogFolderLookup interface {
	LookupFolder(ctx context.Context, destination, path string) (string, error)
}

// CatalogFolderWriter records a successfully resolved Drive path in the
// local catalog. It is deliberately separate from CatalogFolderLookup so
// read-only publisher tests and deployments can opt into caching without
// widening the lookup contract.
type CatalogFolderWriter interface {
	RecordFolder(ctx context.Context, destination, path, folderID, parentFolderID string) error
}

// ResolvedDriveDestination is the outcome of the canonical destination
// resolution pipeline shared by Publish and ResolveFolder.
//
// Both Publish AND ResolveFolder MUST go through resolveDestination so
// RequireSubpath is enforced symmetrically across callers. P0 #2 (June
// 2026) identified that ResolveFolder used a near-duplicate of the
// Steps 1-4 block but skipped the RequireSubpath check, allowing a
// caller to "resolve" a folder that Publish would have rejected.
type ResolvedDriveDestination struct {
	Destination  delivery.DestinationKey
	RootFolderID string
	FolderID     string
	PathSegments []string
}
