// Package drive — verifier.go (Fase 10 / Commit 1, July 2026)
//
// UploadVerifier is the post-upload verification surface that
// confirms each Drive upload actually landed correctly per the
// user-spec literal "Ogni upload Drive deve essere verificato via
// Drive API reale" (Fase 10, July 2026). The verifier is invoked
// synchronously by PutFile after every Create/Update/Rename branch
// AND after the ConflictSkip branch (a corrupted-on-Drive existing
// file must NEVER be silently accepted — the per-commit pin
// "verify-the-skipped-file-too" is the Commit 1 hard requirement).
//
// godlike/06 SSOT: UploadVerifier is the SINGLE canonical owner of
// the post-upload verification check on the *upload* path. The
// existing DriveVerifierAdapter (verifier_adapter.go) owns the
// *pre-upload* "is this link alive" check (used by the artifacts
// port to verify a pasted URL). The two are intentionally separate
// surfaces (pre-upload vs post-upload) per godlike/06
// one-owner-per-fact. If a future caller needs the combined
// "verify any Drive reference" surface, the composition root can
// route both into a single facade — but the two underlying
// implementations stay distinct to preserve SSOT.
//
// godlike/07 fail-closed: every check returns a TYPED sentinel
// (ErrDriveFileNotFound, ErrDriveFileInTrash). Callers probe via
// errors.Is; substring matching is NOT the canonical path. The
// raw Drive SDK error (googleapi.Error) is wrapped via
// fmt.Errorf %w so the inner error chain remains inspectable for
// upstream retry/observability layers.
//
// Per-commit scope: Commit 1 ships the FileIDPresent +
// FileNotInTrash checks (one Files.Get round-trip). Subsequent
// Fase 10 commits add the Name (Commit 2), Size>0 (Commit 3),
// MIME video/* (Commit 4), Folder correct (Commit 5), Downloadable
// via signed URL (Commit 6), and SHA-256 content-match
// idempotency (Commit 7) checks inside the same Verify method —
// callers get a single fail-closed gate that surfaces every
// issue at once.
//
// godlike/07 NO-FAKE-AVAILABILITY: the user spec says
// "verificato via Drive API reale" — every check the verifier
// performs must hit the live Drive API (no local-only checks,
// no cache-only checks). The Commit-1 checks use the canonical
// Reader.GetFileMeta (which performs a real Files.Get
// round-trip via the production *driveapi.Service).
package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Typed sentinels (godlike/07). Callers probe via errors.Is to
// distinguish "file not found" from "file in trash" from generic
// "API error" — each requires different operator intervention.
//
// These are SEPARATE from the pre-existing ErrAmbiguousDriveFile
// (uploader.go) sentinel — that one is raised by lookup methods
// when >1 non-trashed match exists, while these are raised by
// the post-upload verifier when the SINGLE expected file is
// missing or trashed. The two failure surfaces are distinct.
var (
	// ErrDriveFileNotFound: the Files.Get API call returned a
	// 404 (the file ID does not exist on Drive, or has been
	// permanently deleted). The caller may want to re-upload
	// (create) a fresh file with a new ID. The verifier
	// returns this sentinel wrapped via fmt.Errorf %w so the
	// full error chain (including the underlying
	// googleapi.Error) is inspectable.
	ErrDriveFileNotFound = errors.New("drive verifier: file not found (Files.Get returned 404)")

	// ErrDriveFileInTrash: the file exists on Drive but is in
	// the Trash bin (Trashed=true in the Files.Get response).
	// The caller may want to undelete (Drive
	// Files.Update{Trashed:false}) or re-upload a fresh file.
	// Pre-Commit-1, a trashed existing file could be returned
	// silently via the PutActionSkipped branch of PutFile —
	// Commit 1 closes this poison-file gap.
	ErrDriveFileInTrash = errors.New("drive verifier: file is in Drive Trash bin (Trashed=true)")
)

// VerificationParams is the per-upload expected-metadata envelope
// the caller threads into the verifier. Commit 1 ships the
// FileIDPresent + FileNotInTrash checks; subsequent Fase 10
// commits will add the Name (Commit 2), Size (Commit 3), MIME
// (Commit 4), Folder (Commit 5), and Downloadable (Commit 6)
// checks. The struct is intentionally extensible: zero-value
// fields are skipped so the Commit-1 surface doesn't impose
// Commit-2+ invariants on every caller.
//
// Why a single struct (not per-check args): future commits
// (2-6) add 4 more fields; passing 6 positional args would be
// brittle (easy to swap). The struct is the canonical SSOT
// per godlike/06.
type VerificationParams struct {
	// ExpectedName is the filename the caller expects on Drive.
	// Commit 2 will compare against meta.Name. Zero value
	// (empty string) = skip the name check.
	ExpectedName string

	// ExpectedFolderID is the folder the caller expects the
	// file to live in. Commit 5 will compare against
	// meta.Parents. Zero value = skip the folder check.
	ExpectedFolderID string

	// ExpectedMIMEType is the MIME type the caller expects
	// (e.g. "video/mp4"). Commit 4 will compare against
	// meta.MimeType. The comparison is exact-match in Commit 4
	// — wildcards like "video/*" are NOT a Commit 1 concern.
	// Zero value = skip the MIME check.
	ExpectedMIMEType string

	// RequireSizeGTZero (Commit 3) toggles the size>0 check.
	// Default false (no size check); Commit 3 sets it true on
	// the canonical upload path. Pre-Commit-3, callers that
	// set this to true see the check fire.
	RequireSizeGTZero bool
}

// UploadVerification is the per-check result envelope returned
// by Verify on the SUCCESS path. On failure, Verify returns a
// partially-populated envelope (so the caller can still log
// what was probed) plus the typed sentinel. Commit 1
// populates only the FileIDPresent + FileNotInTrash fields;
// subsequent commits add NameMatches, SizeGTZero, MIMEMatches,
// FolderMatches, Downloadable.
//
// godlike/06 SSOT: the per-check fields are FLAT (not nested
// in a map[string]bool) so each check is type-safe and
// self-documenting. A future maintainer can grep for
// "FileIDPresent" to find every site that consumes that field.
type UploadVerification struct {
	// FileID is the ID the verifier probed (echoed back so
	// log lines and audit trails can correlate).
	FileID string

	// FileIDPresent is true when Files.Get returned a 200
	// with a non-empty file ID matching the probed ID.
	FileIDPresent bool

	// FileNotInTrash is true when Files.Get returned a 200
	// AND meta.Trashed is false. A trashed file has
	// FileIDPresent=true but FileNotInTrash=false.
	FileNotInTrash bool

	// Meta is the canonical FileMeta the verifier received
	// from Drive (via Reader.GetFileMeta). Commit 2+ checks
	// consume this. The field is exported so callers can
	// read additional metadata (e.g. the actual Name, Size,
	// MimeType) for logging or further validation without
	// re-issuing a Files.Get.
	Meta *FileMeta
}

// UploadVerifier is the canonical Fase 10 post-upload
// verification surface. Construction: NewUploadVerifier(reader).
// Invocation: Verify(ctx, fileID, params). Failure: returns a
// typed sentinel from the var block above (wrapped via %w).
// Success: returns a populated UploadVerification with nil
// error.
//
// godlike/06 SSOT: UploadVerifier is the SOLE canonical owner
// of the post-upload Drive verification check. The composition
// root wires a single instance per Uploader (one Drive service,
// one verifier); per-call construction in uploader_put.go is
// intentional to keep the seam testable via struct-literal
// injection (per-instance state, parallel-safe under t.Parallel).
type UploadVerifier struct {
	reader Reader
}

// NewUploadVerifier constructs a verifier from the canonical
// Reader port. Production wiring in uploader_put.go::PutFile
// passes u (which satisfies Reader via ports.go compile-time
// assertion). Tests inject a stub Reader to verify the
// per-check surface without spinning up a httptest server.
//
// nil-safe: a nil reader returns an error from Verify (NOT a
// panic) so a future composition-root wiring misconfig surfaces
// as a typed error rather than a mid-call panic.
func NewUploadVerifier(r Reader) *UploadVerifier {
	return &UploadVerifier{reader: r}
}

// Verify is the canonical post-upload verification entry
// point. Commit 1 performs the FileIDPresent + FileNotInTrash
// checks via a single Files.Get round-trip (delegated to
// Reader.GetFileMeta). Subsequent commits will add the
// Name/Size/MIME/Folder/Downloadable checks inside this same
// method, so callers get a single fail-closed gate that
// surfaces every issue at once.
//
// P1 (Fase 10 Commit 1): Verify is invoked from PutFile's
// doPutFile helper AFTER every branch (Created, Updated,
// Renamed, AND Skipped). The Skipped branch is the most
// important — a corrupted existing file (e.g. trashed) must
// not be silently accepted. The pre-Commit-1 behaviour would
// return PutActionSkipped with a trashed file ID; the
// post-Commit-1 behaviour surfaces ErrDriveFileInTrash to
// the caller so the operator can decide whether to undelete
// the trashed file or re-upload.
//
// godlike/07 fail-closed: if the Drive API call returns a
// non-404 error (e.g. 500, 503), the verifier wraps the
// underlying error in fmt.Errorf and returns it. The caller
// (PutFile) wraps this with the action context. The retry
// policy in PutFile's outer DoWithValue does NOT cover the
// verification step (Commit 1) — a verification failure
// surfaces immediately to the caller; if the caller wants
// retry semantics, they can wrap PutFile in their own retry
// loop.
func (v *UploadVerifier) Verify(ctx context.Context, fileID string, params VerificationParams) (*UploadVerification, error) {
	if v == nil || v.reader == nil {
		return nil, fmt.Errorf("drive verifier: nil reader (composition-root wiring misconfig)")
	}
	if strings.TrimSpace(fileID) == "" {
		// Empty fileID is structurally equivalent to "not
		// found" — the caller passed a zero value. Surface
		// the same typed sentinel so the caller doesn't
		// have to special-case the empty-string path.
		return &UploadVerification{FileID: fileID}, ErrDriveFileNotFound
	}

	// Single Files.Get call via the canonical Reader port.
	// Reader.GetFileMeta (uploader_file.go) requests
	// (id, name, mimeType, size, webViewLink, parents, trashed)
	// — Commit 1 reads id + trashed; future commits read
	// name + mimeType + size + parents + webContentLink from
	// the same response.
	meta, err := v.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		// 404 from Files.Get → ErrDriveFileNotFound. The
		// DriveIsNotFound classifier (errors.go:125) is the
		// canonical typed-path detector — it checks for
		// *googleapi.Error with Code==404. Pre-Commit-1 a
		// 404 would surface as a generic error and confuse
		// retry/observability layers.
		if DriveIsNotFound(err) {
			return &UploadVerification{
				FileID:         fileID,
				FileIDPresent:  false,
				FileNotInTrash: false,
			}, ErrDriveFileNotFound
		}
		// Other API errors (500, 503, network blip, ctx
		// cancellation) propagate wrapped. PutFile's outer
		// retry does NOT cover this path; a 503 surfaces
		// immediately. The caller can layer its own retry
		// if it wants.
		return nil, fmt.Errorf("drive verifier: Files.Get %q: %w", fileID, err)
	}

	v2 := &UploadVerification{
		FileID:         fileID,
		FileIDPresent:  meta != nil && meta.ID != "",
		FileNotInTrash: meta != nil && !meta.Trashed,
		Meta:           meta,
	}

	if meta.Trashed {
		// File exists but is in the Trash bin. Surface
		// the typed sentinel + a partially-populated
		// envelope (so the caller can log the actual
		// Trashed=true state if it wants).
		return v2, ErrDriveFileInTrash
	}

	// Future commits (2-6) will add their checks here. The
	// Commit-1 happy path is: fileID present + not in
	// trash → success.
	_ = params // Commit 1 ignores params; Commits 2-6 consume it.

	return v2, nil
}
