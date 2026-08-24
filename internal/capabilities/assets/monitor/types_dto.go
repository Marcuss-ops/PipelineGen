// Package monitor — types_dto.go: monitor-owned DTOs + typed sentinels +
// helpers that used to be imported from internal/infrastructure/* before
// FASE 3.7.
//
// FASE 3.7 (June 2026, "Cutover porte monitor"): zero import infrastruttura
// da monitor/. Every type / helper / sentinel in this file is the canonical
// monitor-owned projection of a fact that previously lived in an
// infrastructure package. Infra-side concrete adapters (e.g.
// *downloader.YTDLPDownloader, *youtubediscoveries.YoutubeDiscoveriesRepository) now
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
package assets

// assetsdb is the canonical monitor-package alias for
// `internal/platform/sqlite/assets` — matches the
// convention used in `ports_discoveries.go` (the canonical port
// definition for the youtube_discoveries ledger). The monitor
// package re-exports ports / sentinels from this package under its
// own canonical names so production callers don't need to import
// the infra-side package directly (per godlike/06 SSOT, app-layer
// callers consume canonical port names; infra is injected via the
// composition root).
import (
	"errors"
	"fmt"
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
// Concrete adapter (*youtubediscoveries.YoutubeDiscoveriesRepository.DrainPendingOutbox
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

// ErrLedgerStateConflict is the canonical monitor-side sentinel for
// the youtube_discoveries ledger state-precondition failure fact.
// Per godlike/06 SSOT (one canonical owner per fact) + godlike/07
// no-fake-availability, the monitor package owns this sentinel as
// a fresh `*errors.errorString` value — the application layer is
// the authoritative source of "what outcome means for the
// application", and the database layer is the authoritative source
// of "what SQL actually returned an error".
//
// The infra-side repository method (`assets.MarkEnqueued` etc.)
// returns `youtubediscoveries.ErrStateConflict` for the same SQL transition
// failure. Production callers in the monitor package pattern-match
// via `errors.Is(err, monitor.ErrLedgerStateConflict)` — this
// resolves to true ONLY after the composition-root adapter
// (`internal/app/lifecycle.go::monitorDiscoveriesAdapter`) translates
// `youtubediscoveries.ErrStateConflict` → `monitor.ErrLedgerStateConflict` via
// `fmt.Errorf("%w: %w", monitor.ErrLedgerStateConflict, err)` (multi-%w
// wrap chain — Go 1.20+). The adapter is the canonical Hexagonal
// bridge between infra (SQLite layout detail) and app (canonical
// application outcome).
//
// FASE 3.7 Commit 1b (2026-07-04) replaces the previous pre-fix
// bridge (commit 60a61808 declared monitor.ErrLedgerStateConflict
// as a distinct sentinel value from youtubediscoveries.ErrStateConflict;
// callers relied on a fragile `fmt.Errorf("...: %w",)` wrap chain
// to bridge the two — any unwrap error path silently invalidated
// the comparison) and the intermediate thin-alias shape (parallel
// agent commit `052a3bd7` collapsed to `var ErrLedgerStateConflict =
// assetsdb.ErrStateConflict` — which created a Go package cycle
// because infra also needed to import monitor for the new
// `[]monitor.OutboxEntry` return type).
//
// The adapter pattern resolves the cycle without inverting the
// layering: monitor owns canonical sentinel locally (no infra import
// in this file), infra owns `youtubediscoveries.ErrStateConflict` locally (no
// monitor import), and the only place BOTH come together is the
// composition root where the adapter translates between them. This
// mirrors the existing `monitorYtdlpAdapter` precedent
// (lifecycle.go) for the down-loader surface — same composition-
// root adapter pattern, constrained to a single structural layer.
//
// The other typed sentinels returned by the same repository
// methods (`ErrNotFound` = row missing; `ErrAlreadyApplied` =
// transition already applied in a prior call) remain
// assets-owned and are not re-exported here. Monitor callers that
// need them add thin-alias sentinels below + a mapDiscoveriesErr
// case in lifecycle.go (mirroring the canonical pattern). Future
// extensions MUST preserve the adapter-only bridge so the monitor
// package's zero-infra-import contract stays intact.
var ErrLedgerStateConflict = errors.New("monitor: youtube_discoveries ledger state conflict — row state does not match expected source state")

// TranslateLedgerSentinel wraps an infra-side state-precondition error
// with `ErrLedgerStateConflict` while preserving the original error
// chain via Go 1.20+ multi-%w formatting. The composition-root adapter
// in `internal/app/lifecycle.go::monitorDiscoveriesAdapter` (see
// `mapDiscoveriesErr` there) calls this helper when the input error's
// chain contains `youtubediscoveries.ErrStateConflict`; this helper itself does
// NOT detect the infra sentinel — it only does the wrap so the
// adapter's `errors.Is(err, youtubediscoveries.ErrStateConflict)` gate decides
// whether to translate.
//
// nil → nil (canonical pass-through).
// non-nil → `fmt.Errorf("%w: %w", ErrLedgerStateConflict, err)` so
// the resulting chain contains BOTH `ErrLedgerStateConflict` and
// whatever the original chain contained (e.g. `youtubediscoveries.ErrStateConflict`,
// plus any additional message text from the infra SQL layer).
//
// Exporting this helper from the monitor package keeps the
// composition-root adapter trivial (single `errors.Is` check +
// delegate to this helper) AND keeps the wrap semantics unit-
// testable from the monitor package without an infra import
// (test fixtures wrap a manually-constructed error and assert
// both sentinels resolve through the chain).
func TranslateLedgerSentinel(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrLedgerStateConflict, err)
}

// defaultNowFn is the lazy default clock used by DateAfterFromCursor
// when the caller passes nil for the now parameter. Production
// resolves to time.Now; tests can swap via SetDefaultNowForTests.
//
// Lazy-default rationale — mirrors the AGENTS.md Pattern 0 (port
// abstraction layer) precedent: a package-level function-typed seam
// (var X = func() time.Time) gives test fixtures a typed injection
// point without touching the production call sites. SetDefaultNowForTests
// is the ONLY mutation surface so the swap is auditable at code review;
// raw mutations from tests bypassing the helper would be a code-review
// violation.
//
// Thread-safety note: defaultNowFn is read+write guarded by Go's data
// race detector (CI runs with -race). Tests that mutate it must not
// run in parallel with other monitor tests; production code never
// mutates it.
var defaultNowFn = time.Now

// SetDefaultNowForTests replaces the lazy-default clock for
// DateAfterFromCursor. Pass nil to restore the production default
// (time.Now). Intended for test fixtures that need a deterministic
// calendar day without real-time waits.
//
// Pair the swap with t.Cleanup(func() { SetDefaultNowForTests(nil) })
// to avoid cross-test pollution. NOT safe to call from production code.
func SetDefaultNowForTests(fn func() time.Time) {
	if fn == nil {
		defaultNowFn = time.Now
		return
	}
	defaultNowFn = fn
}

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
// Clock injection (PR-DETERMINISTIC-CLOCK-INJECTION, 2026-07-04):
// the now parameter is the canonical clock seam. Pass nil for the
// lazy default (production = time.Now via defaultNowFn) or pass an
// explicit func() time.Time for deterministic test fixtures.
// Mirrors the pkg/retry lookupFunc/openFile struct-field precedent
// (post PR-P2.1): tests can swap the clock at the function boundary
// without rewriting production callers.
//
// Replaces the previous `sqlassets.ResolveDateAfter` import in
// discovery.go::discoverChannelVideos. The semantics are byte-equivalent
// to the pre-FASE-3.7 infra-side implementation (with the additional
// contract that production callers should pass nil for the lazy
// default rather than an explicit time.Now literal).
func DateAfterFromCursor(lastCursorRFC3339 string, lookbackDays int, now func() time.Time) string {
	if now == nil {
		now = defaultNowFn
	}
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
		t := now().UTC().Add(-time.Duration(lookbackDays) * 24 * time.Hour)
		return t.Format("20060102")
	}
	return ""
}
