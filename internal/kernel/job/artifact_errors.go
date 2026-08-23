// Package job — artifact_errors.go (FASE 1, sub-passo (c) + close-out, July 2026).
//
// Sentinel errors for the artifact manifest extraction path on the
// worker side and the artifact persistence/validate path on the
// handler side. Per the audit 2026-07-03 P0 #4 closure + FASE 1
// spec close-out:
//
//	c) Worker: errori tipizzati ErrArtifactManifestMissing,
//	   ErrArtifactManifestInvalid, ErrRequiredArtifactMissing —
//	   mai interpretazione "errore ⇒ lista vuota".
//
// FASE 1 close-out: ErrRequiredArtifactMissing is now ALSO
// reachable from the job package as a typed alias. The canonical
// pointer remains in internal/domain/finalization (godlike/06
// SSOT — the publisher-side atomic commit is what enforces the
// invariant). The alias below re-exports the SAME *errors.errorString
// so errors.Is against either name resolves identically.
//
// godlike/06 SSOT (one canonical owner per fact): the *missing*
// and *invalid* sentinels live here because they originate on the
// producer side (worker.js / handler). The *required-missing*
// sentinel lives in finalization because the publisher-side atomic
// commit is what enforces that invariant (a missing required
// artifact cannot be silently skipped at commit time). The
// alias in this file is a re-export, NOT a duplicate creation.
//
// godlike/07 typed-error contract: callers use errors.Is against
// these sentinels to branch on manifest-shape failures — no
// string-matching. The worker's CompleteWithArtifacts pipeline
// wraps the sentinel with the job ID + job type context so the
// audit timeline carries both the typed code and the operator-
// readable message.
package job

import (
	"errors"

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ── Sentinel errors (FASE 1 c — typed manifest contract) ─────────────

var (
	// ErrArtifactManifestMissing is returned when a handler declared
	// it would produce an ArtifactManifest (registry.ArtifactPolicy.
	// RequireManifest=true) but the result map does not contain the
	// canonical __artifact_manifest key. Per the audit P0 #4
	// criterion, a missing manifest is a hard handler failure: the
	// job MUST NOT reach SUCCEEDED. The worker fails the job with
	// this sentinel wrapped (errors.Is probe), the finalizer
	// rejects an absent manifest in the same atomic surface, and
	// the operator timeline records both signals for forensics.
	ErrArtifactManifestMissing = errors.New("artifact manifest: missing (no __artifact_manifest key in handler result)")

	// ErrArtifactManifestInvalid is returned when an ArtifactManifest
	// IS present in the handler result but fails Decode, Validate, or
	// downstream shape coercion. Sub-modes (decode error, validate
	// error, marshal error on PublishedArtifact conversion) all
	// surface this single sentinel — callers branch on errors.Is;
	// the wrapped error carries the sub-mode in the message.
	//
	// Per the audit P0 #4 criterion "il manifest non è
	// decodificabile", any decode/validate failure is a hard job
	// failure. A malformed manifest is a programming bug in the
	// handler; surfacing it as a typed sentinel (vs. a `[]` silent
	// drop) eliminates the false-success failure mode where the
	// broker declared SUCCEEDED despite the handler producing an
	// undecodable artifact manifest.
	ErrArtifactManifestInvalid = errors.New("artifact manifest: invalid (decode, validate, or marshal failure)")
)

// ── FASE 1 close-out: required-missing alias to finalization ────────

// ErrRequiredArtifactMissing is the canonical typed sentinel for a
// required artifact that is missing from the publisher-side atomic
// commit surface. The CANONICAL pointer is owned by
// internal/domain/finalization (publisher-side SSOT per godlike/06
// one-canonical-owner-per-fact); this file re-exports the SAME
// *errors.errorString pointer so worker-side callers can probe
// errors.Is(err, job.ErrRequiredArtifactMissing) without importing
// the finalization package directly.
//
// Identity guarantee: the assignment below shares the pointer —
// both names refer to the same runtime error value. errors.Is
// checks pointer equality FIRST, so errors.Is(err, X) == true
// regardless of which name is queried.
//
// This declaration is the FASE 1 close-out step the spec mandates:
// "Aggiungi sentinels tipizzati nel package job: ErrArtifact-
// ManifestMissing, ErrArtifactManifestInvalid, ErrRequiredArtifact-
// Missing." Without this alias the third sentinel would only be
// reachable from internal/domain/finalization, forcing every
// producer-side code path to dual-import the two packages.
var ErrRequiredArtifactMissing = finalization.ErrRequiredArtifactMissing

// ErrManifestTypedErrors is the package-level compile-time guard
// that the typed-error contract is in place. Callers may use this
// sentinel in test assertions to verify that all manifest-error
// sites propagate through the typed chain without falling back to
// the legacy silent-drop behaviour.
//
// godlike/06 SSOT: the legacy `result == nil || manifest == nil ||
// len(manifest.Artifacts) == 0` short-circuit at extractStagedArtifacts
// is preserved for legitimately-empty artifacts manifests (a
// handler can produce zero files and the audit-permitted zero-count
// path is folded into ErrArtifactManifestInvalid's "marshal
// failure" sub-mode). The strict-missing-key path is the typed
// ErrArtifactManifestMissing above.
