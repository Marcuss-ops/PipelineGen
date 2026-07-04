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

import (
	"errors"
	"time"
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

// ErrLedgerStateConflict is the monitor-owned sentinel returned by
// YoutubeDiscoveriesPort.CommitEnqueueOutbox (and friends) when the
// underlying SQLite row is in an unexpected state for the requested
// transition. Replaces the previous `sqlassets.ErrStateConflict`
// import in enqueue.go.
//
// Callers use errors.Is(err, monitor.ErrLedgerStateConflict) to
// distinguish a state-precondition failure (terminal — operator must
// reconcile) from a transient SQLite I/O error (retryable — surfaced
// via the canonical retry.IsTransient predicate).
//
// The infra-side `assets.ErrStateConflict` sentinel remains the
// authoritative return value; the concrete adapter wraps the error
// via fmt.Errorf("...: %w", assets.ErrStateConflict) so
// errors.Is(monitor-side-err, monitor.ErrLedgerStateConflict) is
// true. This is the canonical pattern for cross-package sentinel
// translation (see AGENTS.md Pattern 0 + godlike/06 "one owner per
// fact" — the monitor package is the application-layer owner of the
// port contract; the infra package owns the SQLite implementation).
var ErrLedgerStateConflict = errors.New("monitor: youtube_discoveries ledger state conflict — row state does not match expected source state")

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
