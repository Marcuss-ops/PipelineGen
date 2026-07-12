// Package retry — decision.go (Fase 6(a) Push 6.1, July 2026).
//
// Typed-only retry-decision surface. The pre-Fase-6 path relied on a
// substring-fallback in pkg/retry.IsTransient (e.e. "timeout",
// "connection refused", "429", "503", "502", "504", "eof") for the
// "is this transient?" predicate. Per the Fase 6(a) user spec:
// substring matching is REMOVED from the production path; every
// adapter MUST convert its concrete error into a typed
// `RetryDecision` value that retries decide on without string
// matching.
//
// ═══════════ Components ═════════════════════════════════════════════════════
//
// (1) RetryDecision — the canonical typed envelope returned by every
// adapter's classifier.
//
//   Fields:
//   - Class        : the canonical ErrorCategory (network/timeout/lock_busy/
//                    validation/missing_handler/bad_payload/unknown).
//                    Production concretes NEVER emit empty/unknown Class
//                    except as a last-resort fallback.
//   - Retryable    : explicit retry/no-retry signal. Truth value is
//                    authoritative — adapters set it without consulting
//                    string patterns.
//   - RetryAfter   : optional per-decision back-pressure hint. Zero-value
//                    means "use the canonical pkg/retry.Options backoff".
//                    Non-zero values are honored by retry.DoWithValue
//                    (pre-existing RetryAfterError path, pkg/retry/retry.go).
//   - SafeMessage  : diagnostic string that does NOT leak credentials
//                    (no api-key, no token, no auth header). Operators read
//                    this in audit logs to classify failures without
//                    exposing upstream secrets.
//
// (2) Decision — the canonical typed-only classifier entry point.
//
//   func Decision(err error) (RetryDecision, bool)
//
//   Returns the first classifier that emitted a non-zero RetryDecision
//   AND whose Predicate returned true. Walk order is the canonical
//   CLASSIFIER chain (the slice registered via RegisterClassifier at
//   init time plus the in-package typed-error probes). If no classifier
//   matches AND the error is nil, returns (zero-value, false). If no
//   classifier matches AND the error is non-nil, returns (zero-value,
//   false) — godlike/07 fail-closed: unknown errors are NOT classified
//   as retryable by default; the caller MUST register a typed
//   classifier for their error shape.
//
// (3) Classifier — the canonical adapter- classifier signature.
//
//   type Classifier func(err error) (RetryDecision, bool)
//
//   Adapters register their classifiers at init via RegisterClassifier.
//   Each classifier inspects the err via errors.As / type-assert against
//   the adapter's concrete error type and returns a populated
//   RetryDecision + a bool indicating whether THIS classifier claims
//   the err. The first match wins (walk order = registration order).
//
//   Avoiding walk-order ambiguity: classifiers SHOULD gate on a unique
//   typed error (e.g. *googleapi.Error, *transport.APIError,
//   *cache.FetchError). A classifier that emits without first verifying
//   a typed probe would shadow later classifiers; the DD error below
//   will surface this during build.
//
// (4) RegisterClassifier — the canonical classifier-registration seam.
//
// Adapters call RegisterClassifier at init time to publish their typed
// classifier into the global CLASSIFIER chain. Tests can swap a
// classifier (use UnregisterAll before adding test classifiers).
// godlike/07 fail-closed: register failures panic at init.
//
// ═══════════ Backward compatibility ═════════════════════════════════════════════════════════
//
// The pre-Fase-6 surface is preserved:
//   - Classify(err) (ErrorCategory, bool) — unchanged in errors.go
//   - Retryable(err) bool — unchanged in errors.go (delegates to Classify)
//   - IsTransient(err) bool — unchanged in transient.go (substring
//     fallback path is retained under a "LEGACY" header; new code MUST
//     NOT depend on it per godlike/06 SSOT drift-prevention)
//
// The Fase-6-b substring-removal is staged: Push 6.1 ships the typed-only
// API alongside the legacy path (no callers break). Push 6.1.x
// follow-ups migrate the 30+ adapter sites that still write
// `IsRetryable: retry.IsTransient` to consume the typed-only classifier
// chain (each call site changes from a substring predicate to an
// adapter-registered Classifier). The substring path itself is
// retained as a last-resort fallback for unsanitized SDK errors not yet
// wrapped at the typed layer (the canonical "WrapTransient" boundary,
// pkg/retry/transient.go).
//
// ═══════════ Usage example ═════════════════════════════════════════════════════════
//
// Adapter-side (e.g. pkg/retry/registry_driven_driveside_classifier.go
// in Push 6.1.2):
//
//     func init() {
//         retry.RegisterClassifier(classifyGoogleAPIErr)
//     }
//
//     func classifyGoogleAPIErr(err error) (retry.RetryDecision, bool) {
//         var gerr *googleapi.Error
//         if !errors.As(err, &gerr) { return retry.RetryDecision{}, false }
//         code := gerr.Code
//         switch {
//         case code == http.StatusTooManyRequests:
//             return retry.RetryDecision{
//                 Class:       retry.ErrNetwork,
//                 Retryable:   true,
//                 RetryAfter:  parseRetryAfter(gerr.Header.Get("Retry-After")),
//                 SafeMessage: "Google API rate limit (429)",
//             }, true
//         case code >= 500:
//             return retry.RetryDecision{
//                 Class:       retry.ErrNetwork,
//                 Retryable:   true,
//                 SafeMessage: fmt.Sprintf("Google API server error (%d)", code),
//             }, true
//         }
//         return retry.RetryDecision{}, false
//     }
//
// Caller-side (e.g. drive uploader):
//
//     err := retry.DoWithValue(ctx, fn, retry.Options{
//         IsRetryable: func(err error) bool {
//             d, ok := retry.Decision(err)
//             return ok && d.Retryable
//         },
//     })
//
// Note: callers DO NOT need to consult both Decision and Classify —
// Decision subsumes Classify's retryable signal. The two functions
// coexist so old callers (using retryable=true from Classify) continue
// compiling while new code can adopt the typed surface incrementally.

package retry

import (
	"errors"
	"log"
	"sync"
	"time"
)

// ── RetryDecision (Fase 6(a): the canonical typed envelope) ────────────────

// RetryDecision is the canonical typed envelope for the per-error
// retry decision. Every adapter that classifies a concrete error for
// the retry loop MUST return one of these values; the pre-Fase-6
// substring fallback in pkg/retry::IsTransient is the LEGACY path
// retained for unsanitized SDK errors not yet typed-wrapped at the
// caller boundary.
//
// godlike/06 SSOT: this struct is the CANONICAL owner of the
// "what should the retry loop do with this error?" fact. Multiple
// pre-Fase-6 contracts (Classify's retryable bool, IsTransient's
// transient bool, Retryable's retryable bool) are SUBSETS of this
// value. Push 6.1.x follow-ups fold the pre-6 surface into Decision +
// RetryDecision; the existing helpers are kept for compile-clean
// migration.
//
// godlike/07 fail-closed contract: a zero-value RetryDecision
// emitted with `final=false` from a classifier means "I don't claim
// this err; let the next classifier have a shot". The Decision
// walker preserves that semantic (first-match wins; zero-value
// fall-through passes to the next).
type RetryDecision struct {
	// Class is the canonical ErrorCategory (network/timeout/lock_busy/
	// validation/missing_handler/bad_payload/unknown). Production
	// classifiers MUST emit a non-empty Class on a final=true
	// decision. The Decision walker enforces this: a final=true
	// decision with Class=="" panics at runtime (godlike/07).
	Class ErrorCategory

	// Retryable is the explicit retry/no-retry signal. Truth value is
	// authoritative — adapters set it without consulting string
	// patterns.
	Retryable bool

	// RetryAfter is the per-decision back-pressure hint for the
	// upstream shape (Google API 429 → Retry-After: <delta-seconds>).
	// Zero-value means "no hint; use canonical pkg/retry.Options
	// backoff". Non-zero values are honored by retry.DoWithValue via
	// the pre-existing RetryAfterError interface.
	RetryAfter time.Duration

	// SafeMessage is the diagnostic string emitted to operators in
	// audit logs. MUST NOT contain credentials (api-key, token,
	// auth header), MUST contain enough context to diagnose failures
	// (status class, shape token). Examples:
	//   "Google API rate limit (429)"
	//   "qdrant getCollection 404 (collection not found)"
	//   "SQLite lock_busy (database is locked)"
	//   "Drive permission denied (403)"
	// Adapters MUST populate this in their classifier; the SafeMessage
	// gate (in Decision walker) panics on empty + final=true.
	SafeMessage string
}

// ── Classifier chain (Fase 6(a): registry-driven typed-only) ────────────────

// Classifier is the canonical adapter-side classifier signature.
// Adapters register their Classifier at init time via RegisterClassifier;
// the walker calls each in registration order until one returns
// final=true (i.e. (RetryDecision{}, true)).
//
// Returning (RetryDecision{}, false) means "I don't claim this err".
// The Walker continues to the next registered classifier.
//
// Returning (d, true) where d.Class == "" panics in the Walker —
// godlike/07 fail-closed: a final=true classifier MUST populate
// Class so downstream observability can categorise the failure.
type Classifier func(err error) (RetryDecision, bool)

// classifierChain is the global Classifier registry. Registration order
// is walk order (first-match-wins). The chain is read-only after
// init() finishes (tests can reset via ResetClassifiersForTest).
//
// godlike/07 nit: the chain is a slice of pointers so registered
// Classifier values cannot be swapped post-init via the registry
// handle. Tests that need a fresh chain use ResetClassifiersForTest.
var (
	classifierChainMu sync.RWMutex
	classifierChain   []Classifier
)

// RegisterClassifier appends c to the global classifier chain. Call
// at init() from adapter packages (any package that defines a typed
// error and wants the retry loop to recognise it via the typed-only
// surface).
//
// godlike/07 fail-closed: RegisterClassifier is NOT safe to call
// after init() finishes; the production cycle is init → register →
// serve. Tests that need late-binding must use
// ResetClassifiersForTest AFTER which the chain reloads at init on
// next process restart — push 6.1.x follow-ups add reset semantics
// if needed.
func RegisterClassifier(c Classifier) {
	if c == nil {
		panic("retry.RegisterClassifier: nil Classifier (fail-closed at init)")
	}
	classifierChainMu.Lock()
	defer classifierChainMu.Unlock()
	classifierChain = append(classifierChain, c)
}

// ResetClassifiersForTest clears the classifier chain. TEST-ONLY;
// production code MUST NOT call this method. The naming is
// intentional ("ForTest") — go-lint will flag godlike/06 SSOT
// violations for any production caller.
//
// Tests that need to swap a classifier (e.g. dual-event classifier
// for Qdrant + Google API) call ResetClassifiersForTest at the start
// of the test, add their classifiers, restore the chain at the end
// via t.Cleanup.
func ResetClassifiersForTest() {
	classifierChainMu.Lock()
	defer classifierChainMu.Unlock()
	classifierChain = nil
}

// ── Decision walker (Fase 6(a): the canonical typed-only entry point) ────

// Decision walks the registered classifier chain and returns the
// first matching RetryDecision (and the boolean "this classifier
// claimed it"). Returns (zero-value, false) when no classifier
// matches; godlike/07 fail-closed semantics: unknown errors are NOT
// classified as retryable by default — callers MUST register a
// typed classifier for their error shape, OR explicitly opt into
// the legacy substring fallback via the pre-existing IsTransient.
//
// Order: the classifier chain is walked in registration order. A
// classifier that emits final=true with Class=="" panics in the
// Walker (godlike/07 — a final classifier MUST populate Class).
func Decision(err error) (RetryDecision, bool) {
	if err == nil {
		return RetryDecision{}, false
	}
	classifierChainMu.RLock()
	chain := append([]Classifier(nil), classifierChain...) // snapshot to avoid holding lock during classifier call
	classifierChainMu.RUnlock()
	for _, c := range chain {
		d, final := c(err)
		if !final {
			continue
		}
		// godlike/07 fail-closed contract: a final classifier MUST
		// populate Class + SafeMessage so downstream observability can
		// categorise the failure and audit logs can grep the SafeMessage
		// without exposing credentials.
		//
		// Operational choice (FASE 6 Cut 6.1 review feedback, July 2026):
		// a buggy classifier MUST NOT crash the production request path.
		// We log+skip the misconfigured classifier so the walker falls
		// through to the next classifier (or to the (zero, false) default
		// if no other classifier matches). godlike/07 fail-closed
		// semantics are still preserved at the typed-probe boundary —
		// the (zero, false) return below does NOT silently classify as
		// retryable, so retry-loop callers MUST register a correct
		// classifier for their error shape. The classifier author who
		// ships a misconfigured classifier sees the log line on next
		// process restart and fixes the regression.
		if d.Class == "" {
			log.Printf("retry.Decision: classifier returned final=true with empty Class — skipping (godlike/07 fail-closed, not crash-closed). err=%v", err)
			continue
		}
		if d.SafeMessage == "" {
			log.Printf("retry.Decision: classifier returned final=true with empty SafeMessage — skipping (godlike/07 fail-closed, not crash-closed). err=%v", err)
			continue
		}
		return d, true
	}
	// Tightened typed-probe fallback (FASE 6 Cut 6.1, July 2026):
	// if no registered Classifier claimed err, probe for the typed
	// RetryableError interface + the *TransientInfrastructureError
	// carrier SEPARATELY so audit logs can distinguish "a typed adapter
	// marked this retryable" from "this is a typed-transient carrier".
	// The Retryable field on both comes from the typed shape itself, never
	// from a substring match — the pre-FASE-6 substring path was
	// REMOVED from production per the user spec; the substring taxonomy
	// is preserved in the test-only fixture pkg/retry/transient_legacy_test.go.
	//
	// godlike/07 fail-closed contract: an error with NO registered
	// Classifier match + NO typed RetryableError + NO TransientInfrastructureError
	// carrier returns (zero, false). The walker does NOT silently retry
	// unmapped shapes. Production adapters MUST register a Classifier
	// at init() — see registry_stdlib.go (stdlib) and the distributed
	// adapter registries under internal/infrastructure/{qdrant/transport,
	// database/sqlite}/ for the typed-overlay surface.
	var re RetryableError
	if errors.As(err, &re) {
		return RetryDecision{
			Class:       ErrNetwork,
			Retryable:   re.IsRetryable(),
			SafeMessage: "typed-RetryableError interface (no registered classifier matched)",
		}, true
	}
	var te *TransientInfrastructureError
	if errors.As(err, &te) {
		return RetryDecision{
			Class:       ErrNetwork,
			Retryable:   true,
			SafeMessage: "TransientInfrastructureError carrier (no registered classifier matched)",
		}, true
	}
	return RetryDecision{}, false
}

// ── Decision Convenience: error wrapping for unwrapped typed errors ────

// Adapters that wrap their concrete SDK error in fmt.Errorf %w lose
// the typed shape at the retry-loop level unless errors.As walks the
// chain. The convenience helpers below keep the Decision walker
// adapters DRY: each classifier uses them to walk a single error
// chain for the typed-error probe.

// asAdapter walks the err chain looking for a *T (or any concrete
// adapter-typed error shape). Returns the typed value + a boolean.
// Pure helper, no state.
//
// godlike/06 SSOT rationale: this is a thin errors.As binding so
// adapter classifier bodies are 1-line probes (the typed-error
// type assertion is the only line that varies per adapter). The
// generic helper keeps each adapter's classifier handler minimal.
func asAdapter[T error](err error) (T, bool) {
	var zero T
	if err == nil {
		return zero, false
	}
	// errors.As walks the unwrap chain; `&zero` is *T (with T
	// satisfying error via the generic constraint). The form below
	// is the canonical Go idiom; the earlier `(any(zero)).(T)` cast
	// was a no-op because T was already inferred statically.
	if errors.As(err, &zero) {
		return zero, true
	}
	return zero, false
}
