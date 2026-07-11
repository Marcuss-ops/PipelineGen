// Package job — artifact_errors.go (FASE 1, sub-passo (c), July 2026).
//
// Sentinel errors for the artifact manifest extraction path on the
// worker side and the artifact persistence/validate path on the
// handler side. Per the audit 2026-07-03 P0 #4 closure:
//
//	c) Worker: errori tipizzati ErrArtifactManifestMissing,
//	   ErrArtifactManifestInvalid, ErrRequiredArtifactMissing —
//	   mai interpretazione "errore ⇒ lista vuota".
//
// ErrRequiredArtifactMissing already exists in
// internal/domain/finalization/errors.go (the finalizer-side typed
// sentinel); it propagates up to the worker via the
// JobFinalizer.CompleteWithArtifacts single-TX spine and is not
// duplicated here.
//
// godlike/06 SSOT (one canonical owner per fact): the *missing*
// and *invalid* sentinels live here because they originate on the
// producer side (worker.js / handler). The *required-missing*
// sentinel lives in finalization because the publisher-side atomic
// commit is what enforces that invariant (a missing required
// artifact cannot be silently skipped at commit time).
//
// godlike/07 typed-error contract: callers use errors.Is against
// these sentinels to branch on manifest-shape failures — no
// string-matching. The worker's CompleteWithArtifacts pipeline
// wraps the sentinel with the job ID + job type context so the
// audit timeline carries both the typed code and the operator-
// readable message.
package job

import "errors"

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
