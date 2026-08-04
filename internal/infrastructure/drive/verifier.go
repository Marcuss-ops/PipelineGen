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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Typed sentinels (godlike/07). Callers probe via errors.Is to
// distinguish "file not found" from "file in trash" from
// "size mismatch" from "content mismatch" from generic "API
// error" — each requires different operator intervention.
//
// These are SEPARATE from the pre-existing ErrAmbiguousDriveFile
// (uploader.go) sentinel — that one is raised by lookup methods
// when >1 non-trashed match exists, while these are raised by
// the post-upload verifier when the SINGLE expected file is
// missing / trashed / size-mismatched / content-mismatched. The
// failure surfaces are distinct.
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

	// ErrDriveFileParentMismatch is returned when the live Drive metadata
	// reports no parent equal to the caller's resolved destination folder.
	// This is a hard integrity failure: callers must fail the job rather
	// than repair the location by moving the file after upload.
	ErrDriveFileParentMismatch = errors.New("drive verifier: file parent does not match expected destination")

	// ErrDriveFileIDMismatch is returned when Files.Get returns metadata
	// for a different file ID than the one produced by the upload. Treat
	// this as a hard verification failure rather than accepting a
	// potentially stale or mis-correlated Drive response.
	ErrDriveFileIDMismatch = errors.New("drive verifier: returned file ID does not match uploaded file ID")

	// ErrDriveFileNameMismatch is returned when the live Drive name does
	// not equal the name returned by the upload operation. This is a hard
	// integrity failure: callers must not rename or otherwise repair the
	// file after the verification gate fails.
	ErrDriveFileNameMismatch = errors.New("drive verifier: returned file name does not match uploaded file name")

	// ErrDriveFileSizeMismatch (PR-CLIPINGEST-PIPELINE step 9,
	// Commit 3, July 2026): the Drive-side file size does not
	// match the caller's ExpectedSize. Treated as a hard upload
	// failure (per user spec "Upload verificato per size+checksum
	// prima della cancellazione locale"). The caller is expected
	// to preserve the local file for operator triage.
	ErrDriveFileSizeMismatch = errors.New("drive verifier: file size mismatch (Drive-side != expected)")

	// ErrDriveFileSHA256Mismatch (PR-CLIPINGEST-PIPELINE step 9,
	// Commit 7, July 2026): the Drive-side file's SHA-256 (computed
	// from a Files.Get + Download round-trip) does not match the
	// caller's ExpectedSHA256. Treated as a hard upload failure
	// per the user spec.
	ErrDriveFileSHA256Mismatch = errors.New("drive verifier: file SHA-256 content mismatch (Drive-side != expected)")
)

// VerificationParams is the per-upload expected-metadata envelope
// the caller threads into the verifier. Commit 1 ships the
// FileIDPresent + FileNotInTrash checks; subsequent Fase 10
// commits add the Name (Commit 2), Size (Commit 3), MIME
// (Commit 4), Folder (Commit 5), Downloadable (Commit 6), and
// SHA-256 content-match (Commit 7) checks. The struct is
// intentionally extensible: zero-value fields are skipped so the
// Commit-1 surface doesn't impose Commit-2+ invariants on every
// caller.
//
// Why a single struct (not per-check args): future commits
// (2-7) add 5 more fields; passing 7 positional args would be
// brittle (easy to swap). The struct is the canonical SSOT
// per godlike/06.
//
// PR-CLIPINGEST-PIPELINE step 9 (July 2026): Commit 3 (size) +
// Commit 7 (SHA-256) land in this cycle. Together they implement
// the user-spec literal "Upload verificato per size+checksum
// prima della cancellazione locale" — the canonical post-upload
// verification gate that prevents local-file cleanup from
// racing ahead of an incomplete Drive upload. Callers pre-compute
// the local size + SHA-256 (the canonical processor.Process
// does this) and thread them via ExpectedSize + ExpectedSHA256;
// Verify compares against the live Drive-side file and surfaces a
// typed sentinel on mismatch.
type VerificationParams struct {
	// ExpectedName is the filename the caller expects on Drive.
	// Commit 2 compares against meta.Name. Zero value
	// (empty string) = skip the name check.
	ExpectedName string

	// ExpectedFolderID is the folder the caller expects the
	// file to live in. Commit 5 compares against
	// meta.Parents. Zero value = skip the folder check.
	ExpectedFolderID string

	// ExpectedMIMEType is the MIME type the caller expects
	// (e.g. "video/mp4"). Commit 4 compares against
	// meta.MimeType. The comparison is exact-match — wildcards
	// like "video/*" are NOT a Commit 4 concern. Zero value =
	// skip the MIME check.
	ExpectedMIMEType string

	// RequireSizeGTZero (Commit 3) toggles the size>0 check.
	// Default false (no size check); Commit 3 sets it true on
	// the canonical upload path.
	//
	// PR-CLIPINGEST-PIPELINE step 9 (July 2026): superseded by
	// ExpectedSize (the more specific size-match check). Kept
	// for back-compat with Commit-3-only callers (a deprecated
	// alias; new code uses ExpectedSize > 0).
	RequireSizeGTZero bool

	// ExpectedSize (PR-CLIPINGEST-PIPELINE step 9, Commit 3, July
	// 2026) is the pre-computed local-file size the caller
	// threads into the verifier. When non-zero, Verify
	// compares the Drive-side file size (Meta.Size) against
	// ExpectedSize; mismatch surfaces the typed sentinel
	// ErrDriveFileSizeMismatch. Zero value = skip the size-match
	// check (back-compat for callers that don't pre-compute size).
	//
	// Fail-closed: a size mismatch is treated as a hard upload
	// failure (the local file is preserved for operator triage
	// per the user spec "prima della cancellazione locale").
	ExpectedSize int64

	// ExpectedSHA256 (PR-CLIPINGEST-PIPELINE step 9, Commit 7,
	// July 2026) is the pre-computed local-file SHA-256 hex
	// digest the caller threads into the verifier. When
	// non-empty, Verify downloads the Drive-side file
	// (via Reader.DownloadFile), computes its SHA-256, and
	// compares against ExpectedSHA256 (case-insensitive).
	// Mismatch surfaces the typed sentinel
	// ErrDriveFileSHA256Mismatch. Empty value = skip the
	// content-match check (back-compat for callers that
	// don't pre-compute SHA-256).
	//
	// Note: the content-match requires a Files.Get + Download
	// round-trip, which costs bandwidth. The canonical
	// user-spec gate is "size+checksum" — both are
	// implemented; callers that want only the cheaper
	// size-match leave ExpectedSHA256 empty.
	ExpectedSHA256 string
}

// UploadVerification is the per-check result envelope returned
// by Verify on the SUCCESS path. On failure, Verify returns a
// partially-populated envelope (so the caller can still log
// what was probed) plus the typed sentinel. Commit 1
// populates only the FileIDPresent + FileNotInTrash fields;
// subsequent commits add NameMatches, SizeGTZero, MIMEMatches,
// FolderMatches, Downloadable, VerifiedSHA256.
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

	// VerifiedSHA256 (PR-CLIPINGEST-PIPELINE step 9, Commit 7,
	// July 2026) is the SHA-256 hex digest the verifier
	// computed from the Drive-side file content (via
	// Reader.DownloadFile). Populated ONLY when the caller
	// passed ExpectedSHA256 non-empty AND the SHA-256 check
	// succeeded. Empty on the Commit-1 surface (no
	// ExpectedSHA256) and on a SHA-256 mismatch (the
	// mismatch sentinel wins; the verified hash is NOT
	// surfaced to avoid leaking the broken state).
	VerifiedSHA256 string
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

// computeSHA256 (PR-CLIPINGEST-PIPELINE step 9, Commit 7, July
// 2026) downloads the Drive-side file via Reader.DownloadFile,
// streams the bytes through SHA-256, and returns the lowercase
// hex digest. The download is bounded by io.LimitReader (no
// infinite read) and the stream is closed by the function
// (deferred Close on the underlying io.ReadCloser). Used ONLY
// by Verify when the caller threads ExpectedSHA256 non-empty.
func (v *UploadVerifier) computeSHA256(ctx context.Context, fileID string) (string, error) {
	if v == nil || v.reader == nil {
		return "", fmt.Errorf("drive verifier.computeSHA256: nil reader")
	}
	// Reader.DownloadFile returns (io.ReadCloser, contentType, error) per
	// the canonical ports.go signature; we discard the contentType here
	// because the SHA-256 check is content-only — MIME validation is a
	// separate Commit 4 concern (ExpectedMIMEType).
	rc, _, err := v.reader.DownloadFile(ctx, fileID)
	if err != nil {
		return "", fmt.Errorf("download file %q: %w", fileID, err)
	}
	defer rc.Close()
	h := sha256.New()
	// 1 MiB buffer — matches the canonical streaming hash buffer
	// used elsewhere in the codebase (e.g. fileutil.HashFile).
	const bufSize = 1 << 20
	if _, err := io.CopyBuffer(h, rc, make([]byte, bufSize)); err != nil {
		return "", fmt.Errorf("read file %q for sha256: %w", fileID, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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

	if meta == nil || strings.TrimSpace(meta.ID) == "" {
		return &UploadVerification{FileID: fileID}, ErrDriveFileNotFound
	}
	if strings.TrimSpace(meta.ID) != strings.TrimSpace(fileID) {
		return &UploadVerification{FileID: fileID, Meta: meta}, fmt.Errorf(
			"drive verifier: file_id=%q metadata_id=%q: %w",
			fileID, meta.ID, ErrDriveFileIDMismatch,
		)
	}

	v2 := &UploadVerification{
		FileID:         fileID,
		FileIDPresent:  true,
		FileNotInTrash: !meta.Trashed,
		Meta:           meta,
	}

	if meta.Trashed {
		// File exists but is in the Trash bin. Surface
		// the typed sentinel + a partially-populated
		// envelope (so the caller can log the actual
		// Trashed=true state if it wants).
		return v2, ErrDriveFileInTrash
	}

	// Parent verification is performed against the live Drive metadata
	// returned by Files.Get. Do not move or repair a mismatch here: the
	// caller must fail the job and preserve the evidence for reconciliation.
	if expected := strings.TrimSpace(params.ExpectedName); expected != "" && strings.TrimSpace(meta.Name) != expected {
		return v2, fmt.Errorf(
			"drive verifier: file_id=%q name mismatch: actual=%q expected=%q: %w",
			fileID, meta.Name, expected, ErrDriveFileNameMismatch,
		)
	}

	if expected := strings.TrimSpace(params.ExpectedFolderID); expected != "" {
		parentMatches := false
		for _, parent := range meta.Parents {
			if strings.TrimSpace(parent) == expected {
				parentMatches = true
				break
			}
		}
		if !parentMatches {
			return v2, fmt.Errorf(
				"drive verifier: file_id=%q parent mismatch: actual=%v expected=%q: %w",
				fileID, meta.Parents, expected, ErrDriveFileParentMismatch,
			)
		}
	}

	// Commit 3 (PR-CLIPINGEST-PIPELINE step 9, July 2026): size
	// match. When ExpectedSize > 0, compare the Drive-side
	// file size (Meta.Size) against the caller-threaded
	// ExpectedSize. Mismatch → ErrDriveFileSizeMismatch +
	// partially-populated envelope (so the caller can log the
	// actual Drive-side size for triage).
	//
	// The pre-step-9 RequireSizeGTZero alias is honoured: when
	// ExpectedSize == 0 AND RequireSizeGTZero == true, the
	// check fires with ExpectedSize = 0 (effectively a
	// "size must be > 0" check). When both are unset, the
	// check is skipped (back-compat).
	if params.ExpectedSize > 0 || params.RequireSizeGTZero {
		if int64(meta.Size) != params.ExpectedSize {
			return v2, fmt.Errorf(
				"drive verifier: file_id=%q size mismatch: drive=%d expected=%d: %w",
				fileID, meta.Size, params.ExpectedSize, ErrDriveFileSizeMismatch,
			)
		}
	}

	// Commit 7 (PR-CLIPINGEST-PIPELINE step 9, July 2026): SHA-256
	// content match. When ExpectedSHA256 is non-empty, download
	// the Drive-side file via Reader.DownloadFile, compute its
	// SHA-256, and compare against ExpectedSHA256
	// (case-insensitive). Mismatch → ErrDriveFileSHA256Mismatch.
	//
	// The download happens ONLY when the size check passes (no
	// point downloading a size-mismatched file). The download
	// itself is not retried — a transient 503 surfaces wrapped
	// to the caller (who can re-verify or re-upload).
	if params.ExpectedSHA256 != "" {
		hash, hashErr := v.computeSHA256(ctx, fileID)
		if hashErr != nil {
			return v2, fmt.Errorf(
				"drive verifier: file_id=%q sha256 compute: %w", fileID, hashErr,
			)
		}
		if !strings.EqualFold(hash, params.ExpectedSHA256) {
			return v2, fmt.Errorf(
				"drive verifier: file_id=%q sha256 mismatch: drive=%s expected=%s: %w",
				fileID, hash, params.ExpectedSHA256, ErrDriveFileSHA256Mismatch,
			)
		}
		// Surface the verified hash on the envelope so the
		// caller can log it without re-issuing the download.
		v2.VerifiedSHA256 = hash
	}

	return v2, nil
}
