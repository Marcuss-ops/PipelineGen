// Package outbox — asset_published.go carries the consumer handler for
// `asset.published` events (SEMANTIC-LOCATION-API-2026-07-06 Wave 5,
// July 2026).
//
// The handler is the post-Publish Qdrant-enrichment counterpart of
// IndexingHandler:
//
//   - IndexingHandler     ingests the asset → media_assets row +
//     Qdrant upsert, gated by source_version equality.
//   - AssetPublishedHandler fires AFTER Publisher.Publish() —
//     the rich payload it carries (destination +
//     origin + category + subject + provider +
//     drive_path + tags) lets the worker compose
//     a richer Qdrant-embed text WITHOUT re-loading
//     these semantic fields from media_assets.
//
// COEXISTENCE POLICY (per godlike/07 minimum-blast-radius):
//
//   - asset.index.requested CONTINUES to be emitted by callers that
//     route through the canonical ClipAtomicWriter path; the existing
//     IndexingHandler keeps working unchanged.
//   - asset.published is ADDITIVE — producers that have already
//     migrated to Publisher.Publish can OPTIONALLY emit it for the
//     richer semantic embedding text the user spec described.
//   - No caller is forced to emit both events; each handler is
//     independent. Per godlike/06 SSOT, both events share the
//     same canonical event-type constant south of
//     internal/infrastructure/database/sqlite/outboxevents/registry.go.
//
// Schema versioning (PR-PUBLISH-V1):
//
//   - Strict v1 envelope — schema_version literal must match
//     AssetPublishedSchemaVersion. Mismatch is TERMINAL via
//     outboxevents.NewTerminalError so producers upgrade instead of
//     retrying into a repair loop.
//   - Required fields: schema_version, event_id, asset_id,
//     destination, idempotency_key.
//   - Optional: origin, category, subject, provider, drive_file_id,
//     drive_path, tags, requested_at. Defaulted at payload decoding
//     so the handler can rely on the enriched names.
//
// EMIT POINT (forward-pointer, Wave 6+ wiring):
//
// User-spec literal: "dopo Publisher.Publish() emetti evento
// outbox asset.published". Wave 5 ships ONLY the handler + payload
// schema + typed-error contracts (godlike/06 SSOT surface). The
// producer-side emit lives at the CALLER of Publisher.Publish
// (post-publish tx, NOT the Publisher itself — keeping Drive I/O
// out of the DB tx per godlike/07 fail-fast-at-input).
// Outbox.Dispatcher gets a new typed EnqueueAssetPublished method
// in Wave 6.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// AssetPublishedSchemaVersion is the canonical, EXACT string the
// AssetPublishedHandler accepts. Producers MUST send the literal
// schema-version value. Mismatch is TERMINAL.
//
// godlike/06 SSOT: the PARALLEL canonical constant
// `outboxevents.SchemaVersionAssetPublished` lives at
// internal/infrastructure/database/sqlite/outboxevents/registry.go
// (the registry is the sole owner of the wire-shape string). This
// local re-export lets the handler's body reference the constant by
// an ergonomic short name without importing the registry's full
// public surface. Both constants MUST resolve to the same string
// literal — any drift surfaces as a build failure during the
// Compilation contract check.
const AssetPublishedSchemaVersion = "asset.published.v1"

// ── Typed-error sentinels (godlike/07 typed-error contract) ──────────────
//
// All sentinels are reachable via errors.Is (or by wrapping with %w).
// The terminal envelope sentinel ErrAssetPublishedTerminalEnvelope
// aggregates every "retry cannot conjure a missing field" condition;
// callers can errors.Is(err, ErrAssetPublishedTerminalEnvelope) as
// the umbrella probe, or downcast to the specific typed
// sub-sentinel for a precise diagnostic.

// ErrAssetPublishedTerminalEnvelope aggregates every
// payload-validation failure. Sub-sentinels wrap this sentinel via
// fmt.Errorf("%w: ...", ErrAssetPublishedTerminalEnvelope, ...) so
// errors.Is(err, ErrAssetPublishedTerminalEnvelope) returns true for
// ANY validation failure path.
var ErrAssetPublishedTerminalEnvelope = errors.New("asset.published: terminal envelope error")

// ErrAssetPublishedPayloadParse fires when the JSON body is
// malformed — retrying won't help; the producer must fix the
// payload. Wraps the terminal sentinel via %w.
var ErrAssetPublishedPayloadParse = fmt.Errorf("%w: payload parse failed", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedSchemaVersionMismatch fires when the envelope's
// schema_version is not AssetPublishedSchemaVersion. Retry cannot
// conjure a different version. Terminal via the umbrella.
var ErrAssetPublishedSchemaVersionMismatch = fmt.Errorf("%w: schema version mismatch", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedAssetIDMissing fires when asset_id is empty.
// Terminal — retry cannot conjure an id.
var ErrAssetPublishedAssetIDMissing = fmt.Errorf("%w: asset_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedDestinationMissing fires when destination is
// empty. The handler needs at least the destination to compose the
// semantic embedding text part label. Terminal.
var ErrAssetPublishedDestinationMissing = fmt.Errorf("%w: destination is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedEventIDMissing fires when event_id is empty.
// Terminal — retry cannot conjure an id.
var ErrAssetPublishedEventIDMissing = fmt.Errorf("%w: event_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedIdempotencyKeyMissing fires when idempotency_key
// is empty. Terminal — the handler must log the payload for forensics.
var ErrAssetPublishedIdempotencyKeyMissing = fmt.Errorf("%w: idempotency_key is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedQdrantUpsertFailed wraps ANY failure from the
// AssetPublisher port (UpsertFromClip OR SetIndexState). RETRYABLE —
// transient Qdrant / SQLite failures ride the pool's exponential
// backoff and the max_attempts dead-letter cap is the natural safety
// net. NOT a terminal sentinel; producers do NOT need to upgrade.
var ErrAssetPublishedQdrantUpsertFailed = errors.New("asset.published: qdrant upsert failed (retryable)")

// ErrAssetPublishedPublisherNotWired is the canonical sentinel when
// the composition root failed to wire the AssetPublisher port (e.g.
// Qdrant disabled at boot). The handler logs+skips rather than
// returning terminal (no point retrying if the port is structurally
// absent — operator must re-enable Qdrant then re-enqueue).
//
// Errors.Is check is the seam for an operator dashboard: any
// producer that sees this sentinel in the audit log should
// re-emit the event once the indexer is back online.
var ErrAssetPublishedPublisherNotWired = errors.New("asset.published: publisher not wired (Qdrant disabled at composition root)")

// ── Envelope (godlike/06 SSOT one-canonical-owner-per-fact) ───────────────

// assetPublishedRequestV1 is the canonical v1 envelope for
// asset.published events. Producers MUST NOT include embeddings,
// raw search vectors, or any payload that would make the event
// bloom to MBs. The handler composes the SearchText locally via
// ComposeSearchText and passes asset_id (NOT the whole payload)
// to the AssetPublisher port.
//
// Required fields (handler fails-fast with TerminalError on
// missing-or-malformed):
//
//   - schema_version   (literal AssetPublishedSchemaVersion)
//   - event_id         (RFC4122 UUID or producer-chosen opaque token)
//   - asset_id         (canonical media_assets.id)
//   - destination      (delivery.DestinationKey canonical string;
//     "stock", "image", "voiceover", etc.)
//   - idempotency_key  (mirrors event_key for audit)
//
// Optional (used by ComposeSearchText for rich embedding text):
//
//   - origin           ("generated", "retrieved", or "live" —
//     distinguishes where the asset came from)
//   - category         (Boxe / Personaggi / etc.)
//   - subject          (Mike Tyson / abc123 / etc. — same field
//     as PublishRequest.Subject)
//   - provider         (pexels / pixabay / wikipedia / dall-e)
//   - drive_file_id    (canonical Drive file id)
//   - drive_path       (slash-joined human form, e.g.
//     "stock/Boxe/pexels/Mike-Tyson")
//   - tags             (canonical tag list)
//   - requested_at     (RFC3339 UTC; logged for audit only)
type assetPublishedRequestV1 struct {
	SchemaVersion  string   `json:"schema_version"`
	EventID        string   `json:"event_id"`
	AssetID        string   `json:"asset_id"`
	Destination    string   `json:"destination"`
	Origin         string   `json:"origin,omitempty"`
	Category       string   `json:"category,omitempty"`
	Subject        string   `json:"subject,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	DriveFileID    string   `json:"drive_file_id,omitempty"`
	DrivePath      string   `json:"drive_path,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	IdempotencyKey string   `json:"idempotency_key"`
	RequestedAt    string   `json:"requested_at,omitempty"`
}

// ── Typed port (godlike/06 Pattern 0) ──────────────────────────────────────

// AssetPublisher is the narrow typed port the AssetPublishedHandler
// needs for Qdrant upsert + IndexState progression. It does NOT
// reach into internal/infrastructure/qdrant or sqlite directly.
//
// Production concrete (forward-pointer, Wave 6 wiring):
// *infraqdrant.IndexWriter (Qdrant upsert) + *assets.ClipsRepository
// (SetIndexState) wrapped via a small adapter at composition root.
// The adapter satisfies the interface via Go's implicit-interface
// rule. Both methods require the asset's row to exist; the handler
// assumes Publisher.Publish already wrote the row.
type AssetPublisher interface {
	// UpsertFromClip upserts the asset_id into Qdrant. Idempotent (the
	// Qdrant point id is uuid5(assetID) → same input always writes the
	// same point). Returns the underlying error verbatim — wrapped by
	// the handler into ErrAssetPublishedQdrantUpsertFailed as
	// retryable.
	UpsertFromClip(ctx context.Context, clipID string) error

	// SetIndexState writes media_assets.index_state (the canonical
	// column added by migration 094). The handler calls this AFTER a
	// successful UpsertFromClip with StateIndexed to close the
	// chain on the IndexState machine. Returns the underlying error
	// verbatim — wrapped as retryable.
	SetIndexState(ctx context.Context, id string, state string) error
}

// ── Handler (godlike/06 SSOT one-canonical-owner-per-fact) ───────────────

// AssetPublishedHandler is the canonical handler for
// asset.published.v1. Mirrors IndexingHandler shape (struct + logger
// + ports; receive-and-process; outcome metric surface).
//
// publisher is required for production wiring. Nil-safe: handler
// logs Warn + returns ErrAssetPublishedPublisherNotWired (NOT
// TerminalError) so the pool's IsTerminal classifier routes the
// event back to pending (matching godlike/07 fail-closed: a missing
// dependency is recoverable via operator re-enabling Qdrant + a
// hand re-emit, NOT a producer-side terminal upgrade).
//
// log nil → nop logger.
type AssetPublishedHandler struct {
	publisher AssetPublisher
	log       *zap.Logger
}

// NewAssetPublishedHandler wires the producer-side dependencies. log
// nil → nop logger. publisher MAY be nil in tests that exercise only
// the parse / validate / ComposeSearchText branches; production
// wiring MUST supply a non-nil adapter.
func NewAssetPublishedHandler(publisher AssetPublisher, log *zap.Logger) *AssetPublishedHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetPublishedHandler{
		publisher: publisher,
		log:       log.Named("asset_published"),
	}
}

// EventType returns the canonical outboxevents constant
// (godlike/06 SSOT — the registry owns the event-type string).
func (h *AssetPublishedHandler) EventType() string {
	return outboxevents.EventAssetPublished
}

// ComposeSearchText composes the canonical semantic search text for
// the asset_published handler. The formatted output mirrors the
// user-spec literal:
//
//	stock video about Mike Tyson in category Boxe from provider pexels tags boxing training
//
// All empty optional segments are silently dropped (the upsert
// still works, just with a leaner line). At minimum, the output
// always carries the destination label + the literal "about +
// subject" so a downstream embedding can always anchor on the
// subject field.
//
// godlike/06 SSOT: AssetPublishedHandler.ComposeSearchText is the
// SINGLE canonical owner of this composition rule. Future PRs MUST
// NOT add a parallel compose function — extend this one with new
// optional fields (style, project_id, language, etc.) per the
// existing joinNonEmpty-style pattern.
//
// godlike/07 no-fake-availability: every produced string starts
// with a destination label so a Qdrant-only operator dashboard can
// always distinguish "stock" from "voiceover" without re-querying
// SQLite.
func ComposeSearchText(destination, origin, subject, category, provider string, tags []string) string {
	if destination == "" {
		// Defensive fallback — but the production path's terminal
		// ErrAssetPublishedDestinationMissing gate ensures this
		// branch is unreachable. Returning "" here is fine because
		// the handler is unreachable for empty destination.
		return ""
	}
	var parts []string
	parts = append(parts, destination)
	if origin != "" {
		parts = append(parts, origin)
	}
	parts = append(parts, "about")
	if subject != "" {
		parts = append(parts, subject)
	}
	if category != "" {
		parts = append(parts, "in", "category", category)
	}
	if provider != "" {
		parts = append(parts, "from", "provider", provider)
	}
	if len(tags) > 0 {
		var kept []string
		for _, t := range tags {
			t = strings.TrimSpace(t)
			if t != "" {
				kept = append(kept, t)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, "tags")
			parts = append(parts, strings.Join(kept, " "))
		}
	}
	return strings.Join(parts, " ")
}

// Handle parses the v1 envelope, composes the canonical SearchText
// from the rich payload, delegates Qdrant upsert + IndexState
// transition to the AssetPublisher port. Validation failures
// return TerminalError (via the umbrella sentinel). Transient
// upsert failures return ErrAssetPublishedQdrantUpsertFailed (NOT
// TerminalError) so the pool's exponential backoff retries per its
// configuration.
//
// Outcome label propagation: a closure-local variable `outcome` is
// reassigned in each branch (default "parse_err"). The deferred
// audit log captures `outcome` once at exit. The function does NOT
// use named returns — every branch returns its error explicitly —
// so future edits adding a branch cannot accidentally overwrite an
// earlier `err =` assignment via a bare `return`.
func (h *AssetPublishedHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	start := time.Now()
	outcome := "parse_err"
	defer func() {
		h.log.Debug("asset.published: outcome summary",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("outcome", outcome),
			zap.Duration("duration", time.Since(start)),
		)
	}()

	log := h.log
	var p assetPublishedRequestV1
	if jerr := json.Unmarshal([]byte(evt.PayloadJSON), &p); jerr != nil {
		log.Warn("asset.published payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(jerr),
		)
		return outboxevents.NewTerminalError(fmt.Errorf("%w: %v", ErrAssetPublishedPayloadParse, jerr))
	}

	// Strict v1 envelope validation. Each missing/mismatched field
	// is TERMINAL — retrying won't bring the field into existence.
	if p.SchemaVersion != AssetPublishedSchemaVersion {
		log.Warn("asset.published schema_version mismatch (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("got_version", p.SchemaVersion),
			zap.String("want_version", AssetPublishedSchemaVersion),
		)
		return outboxevents.NewTerminalError(fmt.Errorf(
			"%w: got %q, want %q (retry cannot conjure a different schema version)",
			ErrAssetPublishedSchemaVersionMismatch, p.SchemaVersion, AssetPublishedSchemaVersion,
		))
	}
	if p.EventID == "" {
		log.Warn("asset.published: missing event_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return outboxevents.NewTerminalError(fmt.Errorf("%w", ErrAssetPublishedEventIDMissing))
	}
	if p.AssetID == "" {
		log.Warn("asset.published: missing asset_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("outbox_event_id", p.EventID),
		)
		return outboxevents.NewTerminalError(fmt.Errorf("%w", ErrAssetPublishedAssetIDMissing))
	}
	if p.Destination == "" {
		log.Warn("asset.published: missing destination (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("asset_id", p.AssetID),
		)
		return outboxevents.NewTerminalError(fmt.Errorf("%w", ErrAssetPublishedDestinationMissing))
	}
	if p.IdempotencyKey == "" {
		log.Warn("asset.published: missing idempotency_key (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("asset_id", p.AssetID),
		)
		return outboxevents.NewTerminalError(fmt.Errorf("%w", ErrAssetPublishedIdempotencyKeyMissing))
	}

	outcome = "compose"
	reqLog := []zap.Field{
		zap.String("asset_id", p.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", p.EventID),
		zap.String("destination", p.Destination),
		zap.String("origin", p.Origin),
		zap.String("category", p.Category),
		zap.String("subject", p.Subject),
		zap.String("provider", p.Provider),
		zap.String("drive_file_id", p.DriveFileID),
		zap.String("drive_path", p.DrivePath),
		zap.Int("tags", len(p.Tags)),
		zap.String("idempotency_key", p.IdempotencyKey),
		zap.Int("attempt", evt.AttemptCount),
	}
	if p.RequestedAt != "" {
		reqLog = append(reqLog, zap.String("requested_at", p.RequestedAt))
	}

	// Compose the canonical SearchText (godlike/06 SSOT one
	// canonical owner per fact).
	searchText := ComposeSearchText(p.Destination, p.Origin, p.Subject, p.Category, p.Provider, p.Tags)

	if h.publisher == nil {
		// No-publisher branch is unusual in production. We log
		// loudly so an operator inspects the composition root's
		// BuildOutboxBundle wiring. Return ErrAssetPublishedPublisherNotWired
		// — the sentinel is NOT wrapped in TerminalError so the pool
		// retries until the operator re-enables Qdrant + re-emits.
		log.Warn("asset.published: publisher nil (composition root misconfig) — event sticky-pending until Qdrant re-enabled",
			append(reqLog, zap.String("search_text", searchText))...,
		)
		outcome = "publisher_absent"
		return fmt.Errorf("%w: event_id=%d asset_id=%s", ErrAssetPublishedPublisherNotWired, evt.ID, p.AssetID)
	}

	// Qdrant upsert first — the canonical idempotent atomic write.
	// Set IndexState only AFTER successful upsert so a transient
	// failure doesn't promote the asset to INDEXED prematurely.
	outcome = "qdrant_upsert"
	if uerr := h.publisher.UpsertFromClip(ctx, p.AssetID); uerr != nil {
		log.Warn("asset.published: Qdrant upsert failed (retryable)",
			append(reqLog, zap.Error(uerr))...,
		)
		return fmt.Errorf("%w: asset_id=%s: %v", ErrAssetPublishedQdrantUpsertFailed, p.AssetID, uerr)
	}

	// IndexState transition — closes the IndexState machine on the
	// canonical Indexed state post-Qdrant success. Idempotent (a
	// re-run finds the column already INDEXED → no-op at the repo
	// layer per ClipsRepository.SetIndexState semantics).
	outcome = "set_indexed"
	if serr := h.publisher.SetIndexState(ctx, p.AssetID, "INDEXED"); serr != nil {
		log.Warn("asset.published: SetIndexState(INDEXED) failed (retryable — Qdrant upsert already done)",
			append(reqLog, zap.Error(serr))...,
		)
		return fmt.Errorf("%w: SetIndexState(INDEXED, %s): %v", ErrAssetPublishedQdrantUpsertFailed, p.AssetID, serr)
	}

	outcome = "success"
	log.Info("asset.published: indexing complete",
		append(reqLog, zap.String("search_text", searchText))...,
	)
	return nil
}

// NOTE (godlike/07 minimum-blast-radius): the AssetPublishedHandler
// does NOT itself update media_assets.search_text or call the
// canonical PayloadMapper / SearchTextBuilder. Wave 6 wiring will
// introduce a typed AssetFieldUpdater port that:
//  1. Writes the composed SearchText into media_assets.search_text
//     BEFORE the Qdrant upsert (so Qdrant sees the rich text via
//     media_assets.search_text on any subsequent re-upsert); AND
//  2. Provides the canonical single-source-of-truth composition
//     via the existing internal/infrastructure/indexing/searchtext
//     Registry, not a parallel inline compose function.
//
// Today the inline ComposeSearchText is the SOLE owner of the
// canonical format. Future Wave 6 may migrate the composition rule
// to the canonical searchtext.Strategy registry; if it does, the
// canonical-SSOT move MUST be filed as a Wave 5 → Wave 6 godlike/06
// linked_issue so future readers can trace the responsibility
// transfer.
