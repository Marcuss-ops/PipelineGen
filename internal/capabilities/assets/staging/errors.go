// Package staging — errors.go (FASE 3-A, July 2026):
// typed-error sentinels for the durable staging port.
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path in the
// Stager chain wraps a typed sentinel reachable via errors.Is. The
// FS-only sentinels below are package-local (the FS write itself
// has its own failure modes). The quota + disk-space sentinels are
// RE-EXPORTED ALIASES (not redeclarations) of the canonical
// declarations in internal/domain/artifact/stages.go so callers
// can errors.Is-probe them through one path.
//
// godlike/06 SSOT rationale: the FS-only failures (workspace
// missing, fsync failure, rename failure, etc.) are NOT canonical
// to the FASE 3 publication saga state machine — they are
// infrastructure concerns. Future cross-package probes should
// errors.Is-probe the typed sentinel name; deeper envelope
// details are wrapped via fmt.Errorf %w.
//
// godlike/07 fail-closed: an unavailable backend (read-only FS,
// disk full, missing workspace) is NEVER represented as a
// successful no-op. Stager.Stage returns the typed error; the
// caller (a future StageService in FASE 3-B) decides whether to
// retry, dead-letter, or fail the job.
package staging

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── FS-only typed sentinels (staging-package-local) ──────────────────

// ErrStagerNotConfigured is returned by the infrastructure
// constructor when a required dependency is nil (e.g., the
// statfsFn seam). godlike/07 fail-fast-at-init.
var ErrStagerNotConfigured = errors.New("staging: LocalStore not configured (missing required dependency)")

// ErrStagerInvalidInput is the umbrella sentinel for bad inputs at
// the Stager.Stage boundary — nil reader, empty ArtifactID, bad
// MIME format. The exact reason wraps in the message string; the
// sentinel is the umbrella for catch-all errors.Is probes.
var ErrStagerInvalidInput = errors.New("staging: invalid StageInput (nil/empty/bad-format)")

// ErrStagerWorkspaceMissing is returned when the configured
// workspace path does not exist AND MkdirAll failed (e.g., parent
// dir unreadable). Distinct from ErrStagerInvalidInput because
// this is a configuration/deployment error, not a caller bug.
var ErrStagerWorkspaceMissing = errors.New("staging: configured workspace path is missing and MkdirAll failed")

// ErrStagerReadFailed is returned when io.Copy from the inbound
// reader to the tmp file fails (network EOF, mid-stream cancel).
var ErrStagerReadFailed = errors.New("staging: read of input Content failed mid-stream")

// ErrStagerFsyncFailed is returned when file.Sync() (file fsync) or
// the parent-directory fsync failed. Per godlike/07 fail-closed,
// fsync failures are HARD — a non-durable stage is NOT acceptable.
var ErrStagerFsyncFailed = errors.New("staging: fsync of staged file (or parent dir) failed")

// ErrStagerRenameFailed is returned when os.Rename from the .tmp
// path to the canonical path failed. Per godlike/07 fail-closed,
// an incomplete atomic-rename is NOT acceptable — the .tmp file is
// cleaned up by the deferred unlink so the next Stage attempt can
// retry cleanly.
var ErrStagerRenameFailed = errors.New("staging: atomic rename of .tmp to canonical failed")

// ErrStagerRecoveryFailed is returned by RecoverOrphans when
// readdir or unlink fails. The error is informational (boot-time
// recovery is best-effort); the returned counter is partial.
var ErrStagerRecoveryFailed = errors.New("staging: orphan .partial/*.tmp readdir/unlink failed")

// ErrStagerIDCollision is returned when the canonical artifactID
// path already exists AND the on-disk hash differs from the new
// inbound hash. Mirrors canonical artifact_stages semantics: a
// hash mismatch on the same ID is a corruption signal (NOT a
// re-stage).
var ErrStagerIDCollision = errors.New("staging: artifactID collision (existing on-disk hash differs from inbound hash)")

// ── Re-exported canonical sentinels (alias only — NOT redeclared) ───

// ErrQuotaExceeded is the canonical staging-quota sentinel
// (per-artifact or workspace-total). Re-exported from
// internal/domain/artifact/stages.go so FASE 3-A callers can
// errors.Is-probe a single alias without importing the domain
// package directly. Identity preserved (same error value).
//
// godlike/06 SSOT: the canonical declaration lives in
// internal/domain/artifact/stages.go. This alias exists ONLY to
// surface the typed sentinel at the port boundary without forcing
// every caller of staging.LocalStore to import the domain
// package directly.
var ErrQuotaExceeded = artifact.ErrQuotaExceeded

// ErrDiskSpaceLow is the canonical staging-disk-space sentinel.
// Same godlike/06 SSOT rationale as ErrQuotaExceeded above.
var ErrDiskSpaceLow = artifact.ErrDiskSpaceLow

// ErrArtifactStageEmpty is the canonical staging-empty sentinel.
// Returned when the inbound Content produces a 0-byte write (per
// audit FASE 3 (a): empty artifact is invalid).
var ErrArtifactStageEmpty = artifact.ErrArtifactStageEmpty

// ErrArtifactStageHashMismatch is the canonical hash-mismatch
// sentinel. Returned when StageInput.ExpectedSHA256 is non-empty
// and the computed hash differs.
var ErrArtifactStageHashMismatch = artifact.ErrArtifactStageHashMismatch

// ── Format validators (package-internal helpers) ─────────────────────

// mimeFormatRE matches the canonical `type/subtype` IANA shape with
// non-empty halves. Suffix wildcards (`+xml`, `+json`, `;params`)
// are NOT validated here — the audit's FASE 3 user-spec says only
// that we enforce the bare `type/subtype` shape, not the full RFC
// 6838 grammar. Capping at this strictness is intentionally a
// minimum-blast-radius surface: the 3-B SQLite repo will receive
// the validated string verbatim.
var mimeFormatRE = regexp.MustCompile(`^[a-zA-Z0-9!#$&\-^_.+]+/[a-zA-Z0-9!#$&\-^_.+]+$`)

// artifactIDFormatRE matches the canonical `art_<...>` shape used
// elsewhere in the pipeline (e.g., ID format from
// internal/platform/sqlite/operations/repository.go).
// Length cap 256 mirrors the application-side safeguard for
// filesystem corruption (a 256-char filename is the typical
// syscall boundary on most Linux filesystems).
var artifactIDFormatRE = regexp.MustCompile(`^art_[a-zA-Z0-9_\-]{1,256}$`)

// ValidateMIMEFormat returns a wrapped ErrStagerInvalidInput if
// the supplied MIME is not in the canonical `type/subtype` shape.
// The error message names the offending value so an operator can
// fix the producer without grep-arounds.
func ValidateMIMEFormat(mime string) error {
	mime = strings.TrimSpace(mime)
	if mime == "" || !mimeFormatRE.MatchString(mime) {
		return fmt.Errorf("%w: mime=%q (want canonical type/subtype)", ErrStagerInvalidInput, mime)
	}
	return nil
}

// ValidateArtifactIDFormat returns a wrapped ErrStagerInvalidInput
// if the supplied artifactID is not in the canonical `art_<...>`
// shape. Path traversal sequences (`../`), shell metacharacters,
// and full-path injections are rejected here so the FS concrete
// cannot be tricked into writing outside the workspace.
func ValidateArtifactIDFormat(artifactID string) error {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" || !artifactIDFormatRE.MatchString(artifactID) {
		return fmt.Errorf("%w: artifactID=%q (want canonical art_<...> shape, len 1..256, path-traversal-safe)", ErrStagerInvalidInput, artifactID)
	}
	return nil
}
