// Package monitor — types_dto.go: monitor-owned DTOs + typed sentinels +
// helpers that used to be imported from internal/infrastructure/* before
// FASE 3.7.
//
// FASE 3.7 (June 2026, "Cutover porte monitor"): zero import infrastruttura
// da monitor/. Every type / helper / sentinel in this file is the canonical
// monitor-owned projection of a fact that previously lived in an
// infrastructure package. Infra-side concrete adapters (e.g.
// *downloader.YTDLPDownloader, *assets.YoutubeDiscoveriesRepository) now
// either return monitor-typed DTOs directly (the standard Hexagonal
// Architecture direction: infra → app imports are permitted; the reverse
// is the violation) OR are wrapped by a thin monitor-adapter in
// internal/infrastructure/* that translates the infra DTOs into the
// monitor DTOs.
//
// Layering rule recap (godlike/06 "data and config ownership" + AGENTS.md
// Pattern 0): monitor/ is the application layer; it owns the contract.
// Infrastructure owns the implementation. Infra importing app = OK
// (dependency injection of port types). App importing infra = NOT OK
// (would couple the orchestration layer to a specific adapter shape).
package monitor

// assetsdb is the canonical monitor-package alias for
// `internal/infrastructure/database/sqlite/assets` — matches the
// convention used in `ports_discoveries.go` (the canonical port
// definition for the youtube_discoveries ledger). The monitor
// package re-exports ports / sentinels from this package under its
// own canonical names so production callers don't need to import
// the infra-side package directly (per godlike/06 SSOT, app-layer
// callers consume canonical port names; infra is injected via the
// composition root).
import (
	"time"

	assetsdb "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// ListChannelVideosQuery is the monitor-owned projection of a
// structured yt-dlp channel-listing request. Replaces the previous
// `downloader.ListChannelVideosRequest` import in ports_downloader.go.
//
// NOTE on naming: this is a READ call (DiscoverChannelVideos returns
// []VideoInfo, no state mutation); Go convention favours `Query`
// over `Command` for request shapes that don't mutate state.
// `Command` is reserved for write/mutating requests. The FASE 3.7
// reviewer flagged the original `*Command` name as a Go idiom miss
// — this rename lands BEFORE Commit 1b widens the surface area.
type ListChannelVideosQuery struct {
	ChannelURL  string
	DateAfter   string // YYYYMMDD format, optional
	PlaylistEnd int    // 0 = all videos, >0 = limit
}

// OutboxEntry is the monitor-owned projection of a single row in the
// monitor_enqueue_outbox table. Replaces the previous
// `assets.OutboxEntry` import in ports_discoveries.go + outbox_drainer.go.
//
// Concrete adapter (*assets.YoutubeDiscoveriesRepository.DrainPendingOutbox
// + DrainDispatched) now returns []monitor.OutboxEntry directly — the
// Hexagonal Architecture direction infra → app is permitted.
type OutboxEntry struct {
	ID             int64  `json:"id"`
	DiscoveryID    string `json:"discovery_id"`
	IdempotencyKey string `json:"idempotency_key"`
	PayloadJSON    string `json:"payload_json"`
	State          string `json:"state"`
	RetryCount     int    `json:"retry_count"`
	NextRetryAt    string `json:"next_retry_at,omitempty"`
}

// ErrLedgerStateConflict is a thin alias for the canonical
// assetsdb.ErrStateConflict (the SSOT owner of the youtube_discoveries
// ledger state-precondition failure fact) per godlike/06 SSOT (one
// canonical owner per fact). The infra-side repository methods
// (MarkEnqueued, MarkRejected) return this sentinel directly when
// the underlying SQLite row exists but its state does not match the
// expected source state(s).
//
// Production callers in the monitor package pattern-match via
// `errors.Is(err, monitor.ErrLedgerStateConflict)` — this comparison
// is byte-equivalent to `errors.Is(err, assetsdb.ErrStateConflict)`
// because both names point at the SAME `*errors.errorString` value
// (Go sentinel semantics: `errors.Is` compares the pointer chain).
// The thin re-export eliminates the pre-fix dual-sentinel split
// (commit 60a61808 declared monitor.ErrLedgerStateConflict as a
// distinct sentinel value from sqlassets.ErrStateConflict; callers
// relied on the canonical `fmt.Errorf("...: %w",` wrap chain to bridge
// the two — a brittle pattern that any unwrap error path silently
// invalidates). PR-MONITOR-DUAL-SENTINEL-FIX (2026-07-04) collapsed
// the two sentinels into one with this thin-alias shape; godlike/07
// no-fake-availability is restored.
//
// The other typed sentinels returned by the same repository methods
// (ErrNotFound = row missing; ErrAlreadyApplied = transition was
// already applied in a prior call) remain assetsdb-owned and are not
// re-exported here — monitor callers that need them should import
// assetsdb directly (they are not yet consumed by monitor code, so
// no canonical monitor-package re-export is required). Future
// re-exports MUST be added when a monitor-side caller needs them,
// to keep the SSOT chain enforced.
var ErrLedgerStateConflict = assetsdb.ErrStateConflict

// DateAfterFromCursor bridges channel.LastCursor (an RFC3339
// timestamp stored in category_channels.last_cursor) and channel.LookbackDays
// (the channel's lookback fallback) into a YYYYMMDD string the
// yt-dlp Downloader.ListChannelVideos port accepts in
// ListChannelVideosQuery.DateAfter.
//
// Precedence (caller's intent): LastCursor wins when parseable as
// RFC3339 (the canonical cursor format from migration 113 onward);
// LookbackDays wins as fallback (now - LookbackDays*24h formatted as
// YYYYMMDD). Empty LastCursor + zero LookbackDays → empty DateAfter
// (yt-dlp's no-filter path).
//
// Replaces the previous `sqlassets.ResolveDateAfter` import in
// discovery.go::discoverChannelVideos. The semantics are byte-equivalent
// to the pre-FASE-3.7 infra-side implementation.
func DateAfterFromCursor(lastCursorRFC3339 string, lookbackDays int) string {
	if lastCursorRFC3339 != "" {
		// Truncate RFC3339 to YYYYMMDD. The first 10 characters of
		// "2026-06-30T15:04:05Z" are "2026-06-30" — drop the rest.
		if len(lastCursorRFC3339) >= 10 {
			datePart := lastCursorRFC3339[:10]
			// Sanity-check: all 10 char[0..4] = digits + dashes.
			dash1, dash2 := datePart[4], datePart[7]
			if dash1 == '-' && dash2 == '-' {
				// Re-format YYYY-MM-DD to YYYYMMDD (dash removal).
				return datePart[:4] + datePart[5:7] + datePart[8:10]
			}
		}
	}
	if lookbackDays > 0 {
		t := time.Now().UTC().Add(-time.Duration(lookbackDays) * 24 * time.Hour)
		return t.Format("20060102")
	}
	return ""
}
