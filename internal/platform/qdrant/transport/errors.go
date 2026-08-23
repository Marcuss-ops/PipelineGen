package transport

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── Wire-level error DTO (PR1 — fix/qdrant-wire-contracts) ───────────
//
// APIError is the canonical typed wrapper for any non-2xx response from
// the Qdrant REST surface. PR1 eliminates the previous `fmt.Errorf`
// pattern (which lost status, retryability, and operation context) and
// replaces it with a single typed value every call site can route
// through. Retryable is computed once at parse time from the Status +
// operation kind; callers should use apiError.Retryable rather than
// re-deriving retry classification from error strings.
//
// Contract:
//   - Operation: client method name (e.g. "GetCollection", "UpsertPoints").
//   - Status: raw HTTP status code returned by Qdrant.
//   - Message: human-readable summary (typically Qdrant's status + message
//     fields extracted from the response body, or a body-truncation if the
//     body was unreadable).
//   - Body: full or truncated response body for diagnostics. NEVER includes
//     the api-key header — the http transport strips it before populating
//     this value.
//   - Retryable: true iff a caller should retry this exact error. Default
//     policy:
//   - 5xx (500/502/503/504)        → retryable
//   - 408 / 429                    → retryable
//   - 4xx (except 408/429)         → not retryable (client error)
//   - Network/IO failures upstream → retryable (we record Status=0).
//
// The retry policy is intentionally conservative: the wire-level truth
// is the *status*, not any heuristic from the body. Retry decisions
// downstream (proxy retry, jobs.Service, etc.) should consult
// APIError.Retryable rather than parsing the message string.
type APIError struct {
	Operation string
	Status    int
	Message   string
	Body      string
	Retryable bool
}

// Error implements the error interface. Format mirrors the pre-PR1
// behaviour so call-site log lines stay recognisable, but adds the
// operation + status fields for grep-friendly diagnostics:
//
//	qdrant.GetCollection: HTTP 404: collection "media_assets_v3" not found
func (e *APIError) Error() string {
	if e == nil {
		return "<nil APIError>"
	}
	op := e.Operation
	if op == "" {
		op = "qdrant"
	}
	if e.Status == 0 {
		return fmt.Sprintf("%s: %s", op, e.Message)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", op, e.Status, e.Message)
}

// Unwrap returns the underlying cause (always nil here — APIError is a
// leaf value). Retained so future revisions can wrap an inner error
// without breaking errors.Is/errors.As chains.
func (e *APIError) Unwrap() error { return nil }

// IsRetryable is variadic overload for the typed-error world: callers
// can do `if err := errors.As(err, &apiErr); apiErr != nil && apiErr.Retryable { ... }`
// OR can keep using the package-level IsRetryable(err) helper that also
// covers the lowercase sentinel errors (ErrCollectionNotFound, etc.).
func (e *APIError) IsRetryable() bool {
	if e == nil {
		return false
	}
	return e.Retryable
}

// classifyRetryability maps the Qdrant HTTP status to the wire-level
// retryability hint populated into APIError.Retryable.
//
// Network/timeout failures upstream of Qdrant report Status=0 and are
// unconditionally retryable — the request never reached the server, so
// the only failure mode is transient.
func classifyRetryability(status int) bool {
	switch {
	case status == 0: // upstream network/timeout — request never reached Qdrant
		return true
	case status == http.StatusRequestTimeout: // 408
		return true
	case status == http.StatusTooManyRequests: // 429
		return true
	case status >= 500 && status <= 599:
		return true
	default:
		return false
	}
}

// readBounded caps the body bytes we retain in APIError.Body. Raw Qdrant
// error responses are typically <1KB but a misconfigured proxy or an
// attacker pushed body could be MBs — caps prevent log-cardinality
// surprises and accidental PII capture.
const maxAPIBodyBytes = 1024 * 1024 // 1 MiB (was 4 KiB; hybrid search payloads with full metadata exceed 4 KiB)

func readAPIBody(r io.Reader) string {
	if r == nil {
		return ""
	}
	limited := io.LimitReader(r, maxAPIBodyBytes)
	data, _ := io.ReadAll(limited)
	return string(data)
}

// ── Sentinel errors ──────────────────────────────────────────────────

// ErrSchemaIncompatible is returned when the collection schema doesn't match expectations.
type ErrSchemaIncompatible struct {
	Diff *schema.SchemaDiff
}

func (e *ErrSchemaIncompatible) Error() string {
	return fmt.Sprintf("qdrant schema incompatible: %d missing vectors, %d dimension mismatches",
		len(e.Diff.MissingVectors), len(e.Diff.DimensionMismatches))
}

// NewErrSchemaIncompatible is the canonical constructor for
// ErrSchemaIncompatible. Use this in call sites that pass the typed
// error to a fmt.Errorf %w wrap (e.g. VerifyCandidate) so the
// pointer-vs-value type contract is fixed at the constructor
// boundary rather than re-litigated at every wrap site. Defensive
// against future drift: if Diff is ever migrated to schema.SchemaDiff
// value-type, only this constructor changes — the wrap sites
// continue to compile.
func NewErrSchemaIncompatible(diff *schema.SchemaDiff) *ErrSchemaIncompatible {
	return &ErrSchemaIncompatible{Diff: diff}
}

// ErrCollectionNotFound is returned when a collection doesn't exist.
// Implements retry.RetryableError — collection/alias absence is an
// operator-fixable transient condition (pending schema init).
type ErrCollectionNotFound struct {
	Name string
}

func (e *ErrCollectionNotFound) Error() string {
	return fmt.Sprintf("qdrant collection %q not found", e.Name)
}

func (e *ErrCollectionNotFound) IsRetryable() bool { return true }

// ErrAliasNotFound is returned when an alias doesn't exist.
// Implements retry.RetryableError — same rationale as ErrCollectionNotFound.
type ErrAliasNotFound struct {
	Alias string
}

func (e *ErrAliasNotFound) Error() string {
	return fmt.Sprintf("qdrant alias %q not found", e.Alias)
}

func (e *ErrAliasNotFound) IsRetryable() bool { return true }

// ErrVectorDimensionMismatch is returned when a vector has wrong dimensions.
type ErrVectorDimensionMismatch struct {
	Channel  string
	Expected int
	Actual   int
	AssetID  string
}

func (e *ErrVectorDimensionMismatch) Error() string {
	return fmt.Sprintf("qdrant vector %q dimension mismatch for asset %q: expected %d, got %d",
		e.Channel, e.AssetID, e.Expected, e.Actual)
}

// ErrNaNOrInf is returned when a vector contains NaN or Inf values.
type ErrNaNOrInf struct {
	Channel string
	AssetID string
}

func (e *ErrNaNOrInf) Error() string {
	return fmt.Sprintf("qdrant vector %q contains NaN or Inf for asset %q", e.Channel, e.AssetID)
}

// ErrEmptyVector is returned when a required vector is present but has
// zero elements (len==0). Distinct from ErrMissingRequiredVector which
// covers the nil (absent) case.
type ErrEmptyVector struct {
	Channel string
	AssetID string
}

func (e *ErrEmptyVector) Error() string {
	return fmt.Sprintf("qdrant vector %q is empty (zero-length) for asset %q", e.Channel, e.AssetID)
}

// ErrMissingRequiredVector is returned when a REQUIRED vector channel
// is nil (absent entirely). Task 4 (July 2026): distinct from
// ErrEmptyVector to help operators distinguish "never generated" from
// "generated but corrupted".
type ErrMissingRequiredVector struct {
	Channel string
	AssetID string
}

func (e *ErrMissingRequiredVector) Error() string {
	return fmt.Sprintf("qdrant required vector %q is missing (nil) for asset %q", e.Channel, e.AssetID)
}

// ErrChannelUnavailable is returned when a vector channel is requested but
// the model is not yet available (e.g. audio without CLAP).
type ErrChannelUnavailable struct {
	Channel string
}

func (e *ErrChannelUnavailable) Error() string {
	return fmt.Sprintf("qdrant vector channel %q is unavailable: no real model configured", e.Channel)
}

// ErrAliasSwitchNotReady is returned when pre-switch verification hasn't passed.
// Implements retry.RetryableError — pre-switch state is transient.
type ErrAliasSwitchNotReady struct {
	Report *schema.SwitchReport
}

func (e *ErrAliasSwitchNotReady) Error() string {
	return "qdrant alias switch not ready: pre-switch verification failed"
}

func (e *ErrAliasSwitchNotReady) IsRetryable() bool { return true }

// ErrReindexRequired is returned when a collection exists with matching
// schema but has zero points. A backfill via the admin reindex command
// is required before the collection can be promoted to the runtime alias.
// This is a startup-level sentinel — the server should fail to start
// until the operator runs `go run ./cmd/admin reindex-qdrant --apply`.
type ErrReindexRequired struct {
	Collection string
}

func (e *ErrReindexRequired) Error() string {
	return fmt.Sprintf("qdrant collection %q requires reindex: schema exists but point count is zero — run 'go run ./cmd/admin reindex-qdrant --apply'", e.Collection)
}

// ErrSparseRequired is returned when the schema has a sparse BM25 channel
// configured but the caller did not supply a schema.SparseQueryVector for the
// hybrid search request. Per QDRANT-004 closure: hybrid search is a
// HARD promise — when the schema has a BM25 channel, the caller must
// send a sparse query vector. Falling back to dense-only is a regression
// and the caller must surface this as a 4xx to the client (handler maps
// it to 400 Bad Request).
type ErrSparseRequired struct {
	Channel string // sparse vector channel that should have been supplied
}

func (e *ErrSparseRequired) Error() string {
	ch := e.Channel
	if ch == "" {
		ch = "bm25_text"
	}
	return "qdrant hybrid search: sparse query vector required for channel " + ch +
		" — schema has sparse BM25 configured; dense-only is a regression"
}

// ── Partial upsert error (HIGH #4, July 2026) ───────────────────────

// AssetUpsertFailure records a single asset's failure during
// IndexWriter.UpsertFromClips. The Phase discriminator (fetch/map)
// lets callers decide whether to retry or dead-letter the event; Cause
// preserves the original error for errors.Is/errors.As chains.
type AssetUpsertFailure struct {
	AssetID string `json:"asset_id"`
	Phase   string `json:"phase"` // "fetch" or "map" (batch upsert to Qdrant is all-or-nothing)
	Cause   error  `json:"-"`     // original error (errors.Is/errors.As compatible)
}

// Error returns the canonical per-failure message.
func (f *AssetUpsertFailure) Error() string {
	if f == nil {
		return "<nil AssetUpsertFailure>"
	}
	return fmt.Sprintf("%s %s: %v", f.Phase, f.AssetID, f.Cause)
}

// Unwrap exposes Cause to errors.Is/errors.As chains.
func (f *AssetUpsertFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

// PartialUpsertError is the aggregated error returned by
// IndexWriter.UpsertFromClips when one or more assets fail during the
// fetch → map → upsert pipeline. Successful points are already committed
// to Qdrant; the failures are recorded per-asset with their phase and
// cause so callers can re-route retryable failures without losing the
// classification of the underlying error.
//
// Retryable is true when at least one failure's cause is transient
// (classified via qdrant.IsRetryable / pkg/retry.IsTransient). Callers
// SHOULD consult .Retryable rather than re-deriving retry decisions
// from the failure slice.
type PartialUpsertError struct {
	SuccessfulIDs []string             `json:"successful_ids"`
	Failures      []AssetUpsertFailure `json:"failures"`
	Retryable     bool                 `json:"retryable"`
}

// Error returns the canonical aggregated error message.
func (e *PartialUpsertError) Error() string {
	if e == nil {
		return "<nil PartialUpsertError>"
	}
	return fmt.Sprintf("upserted %d points but %d assets failed",
		len(e.SuccessfulIDs), len(e.Failures))
}

// IsRetryable exposes the pre-computed Retryable flag so the retry
// decision is centralised at construction time rather than re-derived
// by every caller.
func (e *PartialUpsertError) IsRetryable() bool {
	if e == nil {
		return false
	}
	return e.Retryable
}

// ── Helpers ──────────────────────────────────────────────────────────

// IsRetryable returns true for errors that should be retried
// (HTTP timeout, 5xx, network blip). PR1 extended the helper to
// consult APIError.Retryable when the error carries the typed
// wire-level shape so the centralised status-to-retryability map
// (classifyRetryability) is the single source of truth.
//
// Decision order:
//  1. nil → false
//  2. errors.As into *APIError → use APIError.Retryable verbatim
//  3. errors.As into the lower-case sentinel errors mapped via
//     isPermanent (schema mismatches, NaN, dimension mismatches,
//     channel-unavailable, empty vector) → false
//     3a. sentinel errors matching isRetryableSentinel (collection/alias
//     not found, switch not ready) → true
//  4. everything else → false (unknown errors are terminal)
//     — Blocco 4d (July 2026): pre-fix returned true, treating any
//     unrecognised error as retryable. Per godlike/07 (no unknown
//     retryability), the default is now terminal: only explicitly
//     classified errors trigger retry.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr != nil {
		return apiErr.Retryable
	}
	if isPermanent(err) {
		return false
	}
	if isRetryableSentinel(err) {
		return true
	}
	// Blocco 4d (July 2026, updated): delegate non-Qdrant errors to
	// the canonical pkg/retry.IsTransient for substring-fallback
	// classification. Pre-fix returned false (terminal), which made
	// raw transport timeouts from the Qdrant HTTP client invisible
	// to the retry loop. pkg/retry already has the Qdrant-specific
	// typed checks above (isPermanent + isRetryableSentinel), and
	// its own RetryableError + TransientInfrastructureError checks
	// are a strict superset of the old Blocco 4d terminal-default.
	return retry.IsTransient(err)
}

func isPermanent(err error) bool {
	switch err.(type) {
	case *ErrSchemaIncompatible, *ErrVectorDimensionMismatch, *ErrNaNOrInf,
		*ErrEmptyVector, *ErrMissingRequiredVector, *ErrChannelUnavailable:
		return true
	}
	return false
}

// isRetryableSentinel returns true for sentinel errors that represent
// transient/operator-fixable conditions (collection/alias not found,
// pre-switch verification not ready). These are explicitly retryable
// so the catch-all terminal default doesn't swallow them.
// Blocco 4d follow-up (July 2026).
func isRetryableSentinel(err error) bool {
	switch err.(type) {
	case *ErrCollectionNotFound, *ErrAliasNotFound, *ErrAliasSwitchNotReady:
		return true
	}
	return false
}
