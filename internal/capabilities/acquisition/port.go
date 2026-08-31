// Package acquisition — canonical SourceStager port (Stock Cutover §12-4, July 2026).
//
// Per godlike/06 "one canonical owner per fact", the SourceStager
// abstraction lives here (NOT in `internal/capabilities/assets/ports.go`'s
// per-call `assets.SourceStager`). The acquisition port's prepare/release
// lifecycle is persistent across calls: a stage lives until the caller
// releases it (or it expires), with a deterministic CleanupToken for
// idempotent release-on-retry. The legacy `assets.SourceStager` remains
// active only for compatibility consumers that have not yet migrated
// (stock, images, and jobs/assets). YouTube and Artlist now consume
// this port directly.
//
// Why a NEW package? The acquisition namespace encapsulates the
// persistent-staging concern (filesystem-backed registry + JSON
// metadata sidecars + TTL eviction) which the legacy assets.SourceStager
// was deliberately designed NOT to handle (it's per-call, Cleanup-on-the-
// caller). Two ports coexist: `acquisition.SourceStager` for persistent
// staging, `assets.SourceStager` for per-call tmp-download with caller
// owns cleanup.
//
// Pattern 0 (AGENTS.md): the port + types live in application-layer so
// consumer pipelines can declare compile-time dependencies without
// importing infrastructure. Stock, YouTube, and Artlist use this port;
// remaining compatibility consumers are migrated separately.
// Concrete implementations live under `internal/platform/acquisition/`.
package acquisition

import (
	"context"
	"time"
)

// ── Sentinel errors (godlike/07 typed-error contract) ────────────────
//
// Callers MUST use errors.Is(err, ErrAcquisition*) to inspect the
// failure class. Each sentinel names the rule it enforces so a log
// scan can grep by rule-id without parsing human-readable suffix.

var (
	// ErrAcquisitionNotWired is returned when an acquisition.SourceStager
	// concrete is nil at construction time or when a method is called on
	// a nil receiver. Composition-time fail-fast on this sentinel is the
	// single gate that catches "composition root forgot to inject the
	// stager" before the first stock run hits the broker.
	ErrAcquisitionNotWired = errSentinel("acquisition: SourceStager not wired")

	// ErrAcquisitionPrepareFailed is returned when Prepare could NOT
	// fetch / stage the requested source. The inner error (wrapped via
	// %w) names the downloader / network / filesystem failure class.
	// Retryable by callers if the underlying error is transient
	// (network blip); non-retryable if it's a permanent rejection
	// (404, hash mismatch).
	ErrAcquisitionPrepareFailed = errSentinel("acquisition: Prepare failed (network or filesystem layer)")

	// ErrAcquisitionInvalidRequest is returned when PrepareRequest is
	// structurally invalid (empty URL, empty DownloadSection for a
	// section request, malformed time-section). Pre-fail-fast on the
	// validateRequest gate; never surfaces from a successful download.
	ErrAcquisitionInvalidRequest = errSentinel("acquisition: PrepareRequest invalid (URL or section malformed)")

	// ErrAcquisitionAlreadyReleased is returned when Release is called
	// on a CleanupToken whose staged file has already been released
	// (or never existed in the registry). Caller MUST treat this as
	// a soft-idempotent state (NOT an error) for retry semantics —
	// but the typed-error contract preserves the difference so
	// double-release attempts in tests surface via errors.Is, not as
	// a no-op silent path.
	ErrAcquisitionAlreadyReleased = errSentinel("acquisition: CleanupToken already released (or never registered)")

	// ErrAcquisitionInvalidToken is returned when Release is called
	// with a CleanupToken that doesn't match any registered stage.
	// Distinct from AlreadyReleased (which fires when the token WAS
	// registered but has since been released) so tests can disambiguate
	// "I cleaned up the wrong file" from "double cleanup".
	ErrAcquisitionInvalidToken = errSentinel("acquisition: CleanupToken does not match any registered stage")

	// ErrAcquisitionExpired is returned when Release is called on a
	// stage whose ExpiresAt has passed and the underlying file is no
	// longer on disk (TTL GC swept it). The typed-error contract lets
	// callers distinguish "you waited too long" from "never existed".
	ErrAcquisitionExpired = errSentinel("acquisition: stage has expired (TTL GC swept the underlying file)")
)

// ── SourceRef — the canonical "what to stage" descriptor ──────────────

// SourceRef identifies what to acquire / stage. URL is the canonical
// source locator (YouTube URL, Artlist m3u8, Drive file ID, etc. depending
// on the implementation). DownloadSection is an optional time range in
// yt-dlp format ("*00:01:20-00:01:35"); empty means "the full asset".
//
// Extra fields beyond `assets.SourceRef`:
//   - MIMETypeHint: the caller's best guess at what MIME the file
//     should be (e.g. "video/mp4"). The concrete MAY overwrite this
//     from `file --mime-type` after the file is on disk; the field
//     exists so filename + content-disposition can be derived without
//     an IO round-trip on the happy path.
//   - SuggestedFilename: the leaf name the caller wants for the staged
//     file (e.g. "intro_source.mp4"). Empty → concrete derives from
//     URL + section (last path component, or "section_001" for
//     time-sliced fetches).
//   - PolicyVersion: planner-side policy version stamp. Differs from
//     `assets.SourceRef` shape (which has only URL/Section/Force/Format)
//     — the policy-version field is needed so deterministic cleanup
//     tokens survive policy bumps (re-derive on policy change rather
//     than inherit a stale cached file).
type SourceRef struct {
	URL               string
	DownloadSection   string
	ForceKeyframes    bool
	MergeFormat       string
	MIMETypeHint      string
	SuggestedFilename string
	PolicyVersion     string
}

// ── PrepareRequest — the input to SourceStager.Prepare ────────────────

// PrepareRequest is the input shape to SourceStager.Prepare. The Caller
// fields are filled by the caller (orchestrator / stock pipeline /
// downstream run); the IdempotencyKey is OPTIONAL — when empty, the
// concrete derives a deterministic one from SourceRef.PolicyVersion
// + SourceRef.URL + SourceRef.DownloadSection (SHA256 hex).
//
// Timeout bounds the per-call download. Zero → concrete's default
// (10 minutes, matching the legacy `downloader.YTDLPDownloader`).
//
// TTL bounds the staged file's lifetime on disk. Zero → concrete's
// default (24 hours, per §12-4 forward-pointer). The caller can
// re-Prepare the same SourceRef within the TTL and get the cached
// PrepareContext back (idempotent).
type PrepareRequest struct {
	Source         SourceRef
	CallerRef      string // free-form caller-tag for log enrichment
	IdempotencyKey string // optional deterministic key (empty → derived)
	Timeout        time.Duration
	TTL            time.Duration
}

// ── PrepareContext — the canonical "stage receipt" envelope ────────────

// PrepareContext is the OUTPUT of SourceStager.Prepare — a typed
// receipt that identifies the staged file on disk + registry. The
// caller holds this receipt until they call SourceStager.Release
// with the CleanupToken.
//
// Field semantics:
//   - ID: canonical registry entry id (filesystem name + SHA prefix).
//     Stable across calls; safe to log + correlate in dashboards.
//   - SourceRef: verbatim copy of the request's SourceRef for audits.
//     (Not a pointer: the receipt must survive the request's stack
//     unwind so a deferred Release can fire without racy aliasing.)
//   - LocalPath: absolute path to the staged file on local disk.
//     Empty IF StorageURI is non-empty (remote-only staging; future
//     DriveStager / S3Stager concretes).
//   - StorageURI: optional canonical URI when the stage lives on a
//     remote backend. Empty for local-only stages; populated by
//     remote-only concretes. Production stock/Youtube stages are
//     local-only today so StorageURI is the empty string in those paths.
//   - SHA256: hex-encoded SHA-256 of the staged content. Anchor for
//     idempotent release (same content → same CleanupToken) and content-
//     stable addressing (downstream consumers dedup on this hash).
//   - SizeBytes: file size from post-fetch stat. Used by PrepareResult
//     surfaces (chunk-render budgets, asset-record hydration, etc.).
//   - MIMEType: post-fetch detected or hint-derived. The concrete's
//     order: detect-from-file → fall-back-to-MIMETypeHint. Empty
//     only when both detection and hint fail.
//   - ExpiresAt: UTC time after which TTL GC sweeps the staged file.
//     Caller must Release before ExpiresAt for a graceful teardown.
//   - CleanupToken: deterministic SHA256-hex of the SourceRef triple
//     (URL + DownloadSection + PolicyVersion). Same Prepare inputs →
//     same CleanupToken so retries land on the SAME staged file (the
//     staging is naturally idempotent without a cache-key lookup).
type PrepareContext struct {
	ID           string
	SourceRef    SourceRef
	LocalPath    string
	StorageURI   string
	SHA256       string
	SizeBytes    int64
	MIMEType     string
	ExpiresAt    time.Time
	CleanupToken string
}

// ── SourceStager — the canonical port ────────────────────────────────

// SourceStager is the canonical acquisition.SourceStager port. Concrete
// implementations satisfy this interface (compile-time assertion in
// `internal/platform/acquisition/*_stager.go`).
//
// Prepare: fetch + stage the requested source. Idempotent on
// (SourceRef, IdempotencyKey) — repeat calls within the TTL return
// the same PrepareContext. Throws:
//
//   - ErrAcquisitionInvalidRequest — pre-validation failure.
//   - ErrAcquisitionPrepareFailed   — fetch / stage failure (network,
//     subprocess, filesystem). Inner error is wrapped via %w.
//   - ErrAcquisitionNotWired        — receiver-nil guard.
//
// Release: remove the staged file from the registry AND from disk.
// Idempotent on CleanupToken (releasing the same token twice returns
// ErrAcquisitionAlreadyReleased for the second call; mismatched token
// returns ErrAcquisitionInvalidToken; expired-staged file returns
// ErrAcquisitionExpired if the file is already GC'd).
type SourceStager interface {
	Prepare(ctx context.Context, req PrepareRequest) (*PrepareContext, error)
	Release(ctx context.Context, cleanupToken string) error
}

// ── Validate — port-level pre-flight gate ────────────────────────────

// Validate enforces PrepareRequest invariants BEFORE handing the
// request to the concrete. The gate catches:
//   - empty URL (canonical-locator-less call would produce a
//     non-deterministic CleanupToken).
//   - empty IdempotencyKey (the caller MUST opt in to the auto-derived
//     form or supply their own — explicit is better than implicit).
//   - negative Timeout / TTL (clock-arithmetic would underflow).
//
// Returns the FIRST violation's typed-error so callers can errors.Is
// inspect the specific failure. Validated() returns nil when the
// request is acceptable.
func (r PrepareRequest) Validate() error {
	if r.Source.URL == "" {
		return Wrap(ErrAcquisitionInvalidRequest, "URL must be non-empty (canonical locator missing)")
	}
	if r.IdempotencyKey == "" {
		return Wrap(ErrAcquisitionInvalidRequest, "IdempotencyKey must be non-empty (callers MUST ask for the deterministic derived form explicitly if they want auto-derivation — but auto-derivation happens INSIDE the concrete, not Validate)")
	}
	if r.Timeout < 0 {
		return Wrap(ErrAcquisitionInvalidRequest, "Timeout must be non-negative")
	}
	if r.TTL < 0 {
		return Wrap(ErrAcquisitionInvalidRequest, "TTL must be non-negative")
	}
	return nil
}

// ── DeriveIdempotencyKey — deterministic helper ───────────────────────

// DeriveIdempotencyKey computes the canonical SHA-256-hex idempotency
// key from the (SourceRef, PolicyVersion) triple. Same triple →
// same key — the deterministic property that makes the staging
// surface naturally idempotent.
//
// Production callers normally supply explicit IdempotencyKey so the
// audit trail names the caller's intent; this helper exists so
// concrete implementations that lack an explicit key can fall back
// to a stable, byte-stable derivation that is calculated ONCE per
// Prepare (not per stage release) so the OnDisk + InRegistry
// directories do not collide.
//
// Note: the canonical cleanup token in PrepareContext.CleanupToken
// uses ANOTHER derivation (the same triple, but SHA-256 hex of the
// raw bytes — clashing with the IdempotencyKey is impossible because
// the IdempotencyKey is hashed differently in this package).
func DeriveIdempotencyKey(ref SourceRef) string {
	return sha256Hex("acq.prepare:" + ref.URL + "|" + ref.DownloadSection + "|" + ref.PolicyVersion + "|" + ref.SuggestedFilename)
}

// DeriveCleanupToken computes the canonical SHA-256-hex cleanup token
// from the SourceRef triple. Same as DeriveIdempotencyKey but a
// DIFFERENT namespace prefix so the two strings never collide. This
// is the token the caller hands to Release.
func DeriveCleanupToken(ref SourceRef) string {
	return sha256Hex("acq.release:" + ref.URL + "|" + ref.DownloadSection + "|" + ref.PolicyVersion + "|" + ref.SuggestedFilename)
}

// DeriveStageID builds the canonical filesystem-safe stage ID from
// the URL + section. Used as the LocalPath component (without the
// staging-root prefix). The derivation strips path separators
// + control chars so layout-breaking inputs collapse to a safe
// string. Stable across calls (same source → same ID).
func DeriveStageID(ref SourceRef) string {
	id := safeBaseName(ref.URL)
	if ref.DownloadSection != "" {
		id = id + "__" + safeBaseName(ref.DownloadSection)
	}
	if ref.SuggestedFilename != "" {
		id = id + "__" + safeBaseName(ref.SuggestedFilename)
	}
	if id == "" {
		id = "stage"
	}
	return id
}

// ── Draft helper getters — concrete-test surface ─────────────────────

// Validated returns nil when PrepareContext has all required fields
// populated. The concrete MAY consult this in tests to assert that
// the construction path is canonical. Goes hand-in-hand with
// PrepareRequest.Validate() so callers can guard both sides.
func (c *PrepareContext) Validated() error {
	if c == nil {
		return Wrap(ErrAcquisitionInvalidRequest, "PrepareContext nil pointer")
	}
	if c.SourceRef.URL == "" {
		return Wrap(ErrAcquisitionInvalidRequest, "PrepareContext.SourceRef.URL is empty")
	}
	if c.CleanupToken == "" {
		return Wrap(ErrAcquisitionInvalidRequest, "PrepareContext.CleanupToken is empty (callers MUST Release with the same token they received)")
	}
	if c.SHA256 == "" {
		return Wrap(ErrAcquisitionInvalidRequest, "PrepareContext.SHA256 is empty (content fingerprint required for idempotent release)")
	}
	if c.LocalPath == "" && c.StorageURI == "" {
		return Wrap(ErrAcquisitionInvalidRequest, "PrepareContext.LocalPath AND StorageURI are both empty (a stage must live SOMEWHERE)")
	}
	return nil
}

// HasLocal returns true when the stage lives on the local filesystem
// (LocalPath populated, StorageURI empty). The concrete ASSERTS this
// invariant at construction; callers can short-circuit remote-staging
// paths by branching on HasLocal().
func (c *PrepareContext) HasLocal() bool {
	if c == nil {
		return false
	}
	return c.LocalPath != "" && c.StorageURI == ""
}

// Expired returns true when the staged file's ExpiresAt has passed
// (the TTL GC may have already swept it). Caller-side guard so
// Release can produce a typed ErrAcquisitionExpired BEFORE the
// underlying filesystem check (which would return ENOENT as a
// raw OS error — opaque to the typed-error contract).
func (c *PrepareContext) Expired() bool {
	if c == nil {
		return true
	}
	return time.Now().UTC().After(c.ExpiresAt)
}
