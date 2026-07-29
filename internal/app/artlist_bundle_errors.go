// Single canonical owner of ErrArtlistConsumerRegistrationFailed + ErrArtlistDepMissing + DepKind constants (godlike/06 SSOT Commit A; gates fail-closed at composition).
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"errors"
	"fmt"
)

// ErrArtlistConsumerRegistrationFailed is the typed sentinel the
// composition caller (registerArtlist) reads to abort boot when the
// Artlist job handler fails to bind to the jobs dispatcher. The
// sentinel wraps the underlying RegisterHandler error so operator
// log-lines and tests can branch on intent (godlike/06 SSOT).
//
// PR-P2-FAILCLOSED-JOB (July 2026): the previous wire-bond step
// silently log.Warn'd + continued (a godlike/07 fake-availability
// violation — media.artlist jobs would have queued to dead-letter
// forever). The composition caller MUST abort on this error rather
// than mask it; defining the sentinel here keeps the SSOT single-
// source for both the gate error and the abort-contract test.
var ErrArtlistConsumerRegistrationFailed = errors.New("artlist: consumer-job registration failed at composition — production must abort boot (godlike/07 no-fake-availability)")

// ════════════════════════════════════════════════════════════════════
//  ErrArtlistDepMissing — typed per-dep fail-closed sentinel (Fase 1)
//
//  godlike/06 SSOT: this file is the SINGLE canonical owner of the
//  typed sentinel + DepKind constant set. Every WireArtlist mandatory
//  gate returns an instance so orchestrators (registerArtlist) can:
//   1. Branch on intent via `errors.As(err, &missing)` — structured logs
//      (zap.String("missing_dep", missing.Kind.String())).
//   2. Surface the missing field name (Field) verbatim so operators can
//      map to the upstream wiring.ComposeRoot / runtime receipt.
//   3. Avoid the godlike/07 fake-availability anti-pattern of
//      `log.Warn + skip-route + return-nil` (previous behavior) — the
//      composition caller now aborts boot with a typed-wrapped error.
//
//  Phase 1 (DoD §1) maps 6 of the 10 user-listed deps to hard gates.
//  Indexer (Qdrant indexer) / FFmpeg processor / Downloader are
//  intentionally NOT gated by design: their prod-bit-state is verified
//  at runtime via the canonical PostValidator + IsLiveProbe + ffprobe
//  binary-detection paths surfaced via WireArtlist's composition-time
//  construction (NOT composition-time fail-closed). A Fase 1.5 follow-
//  up may promote them to typed gates if the operator-only battery
//  requires composition-time visibility — for now per-dep telemetry is
//  surfaced via /api/artlist/diagnostics (Fase 2 follow-up).
// ════════════════════════════════════════════════════════════════════

// DepKind enumerates the canonical Artlist composition dependency
// kinds. The string value is the canonical log/diagnostic tag — tests
// branch on errors.As depth matching; operators grep on these strings.
type DepKind string

const (
	// DepKindRunRepo gates `bundle == nil` (the wiring.ArtlistBundle itself).
	DepKindRunRepo DepKind = "wiring.ArtlistBundle"
	// DepKindPublisher gates `bundle.Publisher == nil` (canonical delivery.Publisher).
	DepKindPublisher DepKind = "DrivePublisher"
	// DepKindDispatcher gates `dispatcher == nil` (canonical outbox.Dispatcher).
	DepKindDispatcher DepKind = "OutboxDispatcher"
	// DepKindClipsRepo gates `bundle.ClipsRepo == nil` (canonical *assets.ClipsRepository).
	DepKindClipsRepo DepKind = "ClipsRepository"
	// DepKindJobsService gates `bundle.Jobs.Service == nil` (composition-time wiring.JobsBundle.Service).
	DepKindJobsService DepKind = "JobsService"
	// DepKindScraperURL gates the (cfg.Features.ArtlistEnabled &&
	// cfg.External.ArtlistScraperServerURL=="") pair via validateArtlistScraperURL.
	DepKindScraperURL DepKind = "ArtlistScraperServerURL"
	// DepKindIndexer gates `bundle.ClipIndexerService == nil` (Qdrant clipindexer port).
	// The pre-Fase-1 service thread would silently nil-deref at first
	// outbox dispatch — composition-time rejection turns a runtime 500
	// into a typed boot abort.
	DepKindIndexer DepKind = "ClipIndexerService"
	// DepKindFinalizer gates the assetfinalizer.NewAssetTxFinalizer(log)
	// nil-discard path. The constructor today always returns non-nil;
	// the gate pins the contract at composition time so a future
	// implementation that conditionally returns nil (e.g., early
	// config-validation failure) cannot silently regress the
	// fail-closed invariant.
	DepKindFinalizer DepKind = "AssetTxFinalizer"
)

// String makes DepKind satisfy fmt.Stringer so zap.String fields
// (zap.String("missing_dep", missing.Kind.String())) render cleanly
// without explicit casts.
func (k DepKind) String() string { return string(k) }

// ErrArtlistDepMissing is the typed per-dep sentinel WireArtlist
// returns at every mandatory gate. errors.As(err, &missing) lets
// orchestrators programmatically branch on the missing dep. The Detail
// field optionally carries the original verbose message (scraper-URL
// gate uses it to retain the operator env-var hint verbatim; simple
// gates leave it empty).
type ErrArtlistDepMissing struct {
	Kind   DepKind
	Field  string
	Detail string
}

// Error satisfies the error interface; the format is greedy-named so
// operators grepping for `mandatory dependency missing:` land on the
// canonical diagnostic string. Detail (when non-empty) is appended
// after the godlike/07 marker for the operator-fix hint paths.
func (e ErrArtlistDepMissing) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("artlist: mandatory dependency missing: %s (field: %s) — godlike/07 fail-closed; %s", e.Kind, e.Field, e.Detail)
	}
	return fmt.Sprintf("artlist: mandatory dependency missing: %s (field: %s) — godlike/07 fail-closed", e.Kind, e.Field)
}
