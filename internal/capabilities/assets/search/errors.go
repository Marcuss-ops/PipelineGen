// Package search — errors.go is the canonical SSOT for every sentinel
// error in the search package per godlike/06 "one owner per fact".
// Every sentinel is declared with errors.New so callers probe with
// errors.Is; the few typed-data envelopes (no current occurrences in
// the search package) would live alongside as exported structs.
//
// History:
//   - pre-Commit-2 (June 2026): sentinels were scattered across ports.go,
//     types.go, errors.go with overlapping promotion comments.
//   - Commit 2 BACKFILL/CUTOVER (July 2026): promoted the typed-error
//     sentinels from mediasearch → search (the search capability owns
//     its own workspace enforcement + hybrid mode + eligibility + all-failed
//     contracts). The legacy mediasearch.ErrXxx sentinels became Go-level
//     pointer-identical aliases of these canonical sentinels (single
//     pointer identity, errors.Is traverses the chain transparently).
//   - PR-SEARCH-PORTS-SPLIT (2026-07-04, pre-deadline 49 days early):
//     consolidated every sentinel from ports.go + types.go + errors.go
//     into this single canonical file. No new symbols; pure reorg.
package assets

import "errors"

var (
	// ── BackendRegistry sentinels ──────────────────────────────────
	// Mirrors providers.Registry so operator muscle memory transfers
	// between the two.

	// ErrAlreadyRegistered is returned by BackendRegistry.Register when
	// a backend with the same Name() already exists. The error is
	// wrapped with the offending name via fmt.Errorf %w at the call
	// site so the operator log surfaces the duplicate.
	ErrAlreadyRegistered = errors.New("search: backend already registered")

	// ErrFrozen is returned by BackendRegistry.Register when the
	// registry has been frozen (composition root has called Freeze()).
	// After Freeze, Register is rejected; reads become wait-free.
	ErrFrozen = errors.New("search: registry frozen")

	// ErrNilBackend is returned by BackendRegistry.Register when the
	// caller passes nil OR a typed-nil pointer (Kind==Ptr && IsNil).
	// Catches both pre-Lock and post-Lock nil-bypass attempts.
	ErrNilBackend = errors.New("search: nil backend")

	// ErrEmptyName is returned by BackendRegistry.Register when the
	// backend's Name() returns "". Checked pre-Lock so a
	// well-behaved backend with a valid Name is never blocked.
	ErrEmptyName = errors.New("search: backend Name() returned empty")

	// ── Search-level sentinels ─────────────────────────────────────

	// ErrMissingWorkspace is returned when the search surface is invoked
	// without a workspace in the auth context. The handler maps this to
	// HTTP 403 — worker principals cannot bypass through the body.
	//
	// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
	// mediasearch.ErrMissingWorkspace to the canonical search package
	// (godlike/06 SSOT — the search capability owns its own workspace
	// enforcement contract). The legacy mediasearch.ErrMissingWorkspace
	// is now a Go-level alias of this canonical sentinel (same pointer,
	// so errors.Is traverses the chain transparently).
	ErrMissingWorkspace = errors.New("search: workspace context required")

	// ErrHybridRequiresSparse is returned when mode=hybrid is requested
	// but the pipeline cannot produce a real dense+sparse retrieval
	// (sparse channel missing from VectorConfig, OR the BM25 tokenizer
	// returns nil for the query — e.g. all tokens <2 chars after
	// punctuation stripping). Handler maps to HTTP 422.
	//
	// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
	// mediasearch.ErrHybridRequiresSparse to the canonical search package.
	// The legacy alias mediasearch.ErrHybridRequiresSparse
	// is now a Go-level pointer-identical re-export of this sentinel.
	ErrHybridRequiresSparse = errors.New("search: hybrid mode requires a configured sparse vector channel and a BM25-tokenizable query")

	// ErrNoBackendAvailable is returned when the BackendRegistry has
	// zero eligible backends for the query (e.g. no backend advertises
	// the requested media type capability). Handler maps to HTTP 503.
	//
	// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
	// mediasearch.ErrNoBackendAvailable to the canonical search package.
	ErrNoBackendAvailable = errors.New("search: no backend available for the requested query")

	// ErrAllBackendsFailed is returned when every eligible backend
	// returned an error (the fan-out produced zero successful results).
	// Handler maps to HTTP 502 (Bad Gateway — upstream backends are
	// reachable but all failed).
	//
	// Commit 2 BACKFILL/CUTOVER (July 2026): promoted from
	// mediasearch.ErrAllBackendsFailed to the canonical search package.
	// Canonical declaration lives here in errors.go (NOT ports.go, which
	// carries only a godoc comment block about the promotion).
	ErrAllBackendsFailed = errors.New("search: all eligible backends failed — check ProviderErrors for per-backend diagnostics")

	// ── Channel sentinels (PR-EMBEDDING-CHANNEL-REGISTRY, July 2026) ──
	// Registry impls MUST return these sentinels (errors.New(...)) or
	// wrap them via fmt.Errorf("%w") so callers can errors.Is the
	// specific failure mode.

	// ErrChannelUnknown is returned when the channel name is not in
	// the canonical closed set. Surfaces a programming error at the
	// orchestrator rather than a misconfiguration at composition root.
	ErrChannelUnknown = errors.New("search: unknown embedding channel")

	// ErrChannelNotConfigured is returned when the channel is in the
	// canonical closed set but no adapter has been wired at the
	// composition root. Distinguishes "we don't yet support this
	// channel" from "the channel doesn't even exist".
	ErrChannelNotConfigured = errors.New("search: channel recognized but no adapter wired")

	// ErrChannelNotApplicable is returned when the channel accepts
	// file-path index-time inputs (visual/audio) but the caller is
	// invoking EmbedQuery (a text-input port). The visual/audio
	// channels are NOT query-time text-encodable in the canonical
	// surface until PR-CROSS-MODAL-TEXT-TO-VISUAL lands a SigLIP-text
	// / CLAP-text encoder. The sparse channel returns this sentinel
	// because Qdrant handles BM25 inference server-side; no Go-side
	// encoder is needed.
	ErrChannelNotApplicable = errors.New("search: channel does not support text-query encoding (use index-time file input instead)")

	// ── Cursor / dedup sentinels (pre-Commit-2) ─────────────────────

	// ErrInvalidCursor is returned by DecodeCursor for malformed input
	// (bad base64, bad JSON, unknown version marker, etc.). Handlers
	// map this to HTTP 422 (semantic error, not a transient failure).
	//
	// PR-SEARCH-PORTS-SPLIT (2026-07-04): relocated from types.go
	// to errors.go (godlike/06 SSOT — every sentinel in one place).
	ErrInvalidCursor = errors.New("search: invalid cursor")

	// ErrEmptyCandidate is returned by dedupKey when a Candidate carries
	// no identity (no AssetID, no SourceRef, no URL, no Hash). The
	// aggregator drops empty-identity candidates silently per dedup
	// policy; callers see ErrEmptyCandidate only when a builder
	// callback tries to mint an empty cursor from them.
	//
	// PR-SEARCH-PORTS-SPLIT (2026-07-04): relocated from types.go
	// to errors.go (godlike/06 SSOT).
	ErrEmptyCandidate = errors.New("search: empty candidate")

	// ── Aggregator sentinels (Fase 6, July 2026) ────────────────────
	// Replaces the pre-Fase-6 silent-degrade paths.

	// ErrSemanticBackendUnavailable is returned by the Aggregator
	// when Query.Mode == SearchModeHybrid but no semantic backend
	// is registered in the BackendRegistry. Handlers map this
	// to HTTP 422 (semantic error: the client requested a mode
	// that the current deployment does not support).
	//
	// Callers MUST NOT silently degrade hybrid → ANN on this
	// error. The client explicitly requested hybrid retrieval;
	// falling back to ANN would violate the contract.
	ErrSemanticBackendUnavailable = errors.New("search: semantic backend not available for hybrid mode — register a semanticSearchBackend or retry with mode=ann")

	// ErrNoEligibleBackends is returned by the Aggregator when
	// zero backends match the query after source + media-type
	// filtering. The previous behaviour (PR 8/9) returned an
	// empty Result with Partial=false — a silent success that
	// hid misconfiguration. Handlers map this to HTTP 422.
	ErrNoEligibleBackends = errors.New("search: no eligible backends for query — check sources and media_types filters")

	// ErrCursorEncoding is returned when the Aggregator cannot
	// produce a NextCursor from the merged result set. This is
	// distinct from ErrInvalidCursor (malformed input cursor):
	// the input was valid, but the output cursor could not be
	// serialised (base64 or JSON encoding failure). Handlers
	// map this to HTTP 500 (internal error: cursor pipeline
	// broken).
	ErrCursorEncoding = errors.New("search: failed to encode next cursor — internal cursor pipeline error")
)
