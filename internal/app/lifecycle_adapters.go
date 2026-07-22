// Package app — lifecycle composition-root adapters (PR-LIFECYCLE-SPLIT-BY-CAPABILITY, July 2026).
//
// Extracted from internal/app/lifecycle.go per AGENTS.md Pattern 5.
// Owns ALL 4 composition-root adapter structs that bridge canonical
// monitor/voiceover/outbox ports to their concrete infra-side concretes:
//
//   - outboxMonitorAdapter        (outbox.MonitorPort ← outboxevents.Repository)
//   - monitorYtdlpAdapter         (monitor.MonitorDownloaderPort ← *downloader.YTDLPDownloader)
//   - monitorDiscoveriesAdapter   (monitor.YoutubeDiscoveriesPort ← *youtubediscoveries.YoutubeDiscoveriesRepository)
//   - uploadIntentsAdapter        (voiceover.UploadIntentsRepository ← *scripts.UploadIntentsRepository)
//
// Plus the FASE 3.7 Commit 2 monitor.MetricsRecorder compile-time
// assertion (pinned here because lifecycle_adapters.go is the ONLY
// composition-root file that imports both monitor + observability
// without creating an import cycle).
//
// The adapters exist ONLY because the build has a few mid-refactoring
// type-shape mismatches between ports (declared in canonical
// cross-package locations) and the concrete adapters in the
// composition root. Each adapter:
//
//   - is defined inline at the composition root (no new shared
//     package surface, no leak into the application-layer domain);
//   - uses field-level assignment rather than any reflection or
//     unsafe casts (Go-strict, easy to audit);
//   - is documented with the upstream contract it satisfies so a
//     future port evolution can be tracked here as the canonical
//     Bridge layer.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/youtubediscoveries"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// ── outboxMonitorAdapter ──────────────────────────────────────

// outboxMonitorAdapter (moved from outbox_monitor_adapter.go, Phase 5
// consolidation, June 2026) wraps *outboxevents.Repository so the
// outbox.MonitorPort (application-layer) is satisfied.
type outboxMonitorAdapter struct {
	repo *outboxevents.Repository
}

var _ outbox.MonitorPort = (*outboxMonitorAdapter)(nil)

func newOutboxMonitorAdapter(repo *outboxevents.Repository) outbox.MonitorPort {
	if repo == nil {
		return nil
	}
	return &outboxMonitorAdapter{repo: repo}
}

func (a *outboxMonitorAdapter) CountByStatus(ctx context.Context, status string) (int64, error) {
	if a == nil || a.repo == nil {
		return 0, nil
	}
	return a.repo.CountByStatus(ctx, status)
}

func (a *outboxMonitorAdapter) ListPending(ctx context.Context) ([]outbox.EventDTO, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	events, err := a.repo.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	dtos := make([]outbox.EventDTO, len(events))
	for i, e := range events {
		dtos[i] = eventToDTO(e)
	}
	return dtos, nil
}

func (a *outboxMonitorAdapter) ListByStatus(ctx context.Context, status string) ([]outbox.EventDTO, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	events, err := a.repo.ListByStatus(ctx, status)
	if err != nil {
		return nil, err
	}
	dtos := make([]outbox.EventDTO, len(events))
	for i, e := range events {
		dtos[i] = eventToDTO(e)
	}
	return dtos, nil
}

func eventToDTO(e outboxevents.Event) outbox.EventDTO {
	return outbox.EventDTO{
		ID:            e.ID,
		EventType:     e.EventType,
		AggregateID:   e.AggregateID,
		AggregateType: e.AggregateType,
		PayloadJSON:   e.PayloadJSON,
		Status:        e.Status,
		AttemptCount:  e.AttemptCount,
		MaxAttempts:   e.MaxAttempts,
		LastError:     e.LastError,
		EventKey:      e.EventKey,
		WorkerID:      e.WorkerID,
		LeaseID:       e.LeaseID,
		LeaseExpiry:   e.LeaseExpiry,
		CompletedAt:   e.CompletedAt,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
	}
}

// ── monitorYtdlpAdapter ───────────────────────────────────────

// monitorYtdlpAdapter wraps *downloader.YTDLPDownloader (the infra
// DTO producer) so the channel-monitor's typed port
// `monitor.MonitorDownloaderPort` (the domain DTO consumer) is
// satisfied. The mismatches are:
//   - request shape:  `downloader.ListChannelVideosRequest` (infra)
//     → `monitor.ListChannelVideosQuery` (domain)
//   - return slice:   `[]downloader.VideoInfo` → `[]monitor.VideoInfo`
//
// FASE 3.7 Commit 1b (2026-07-04): the request-shape translation is
// added because `monitor.MonitorDownloaderPort.ListChannelVideos` was
// migrated to `monitor.ListChannelVideosQuery` to drop the downloader
// import from `internal/application/assets/monitor/ports_downloader.go`.
// The composition root is the canonical bridge between the two
// request shapes — no monitor-side caller now needs to import
// `internal/infrastructure/downloader`.
type monitorYtdlpAdapter struct {
	inner *downloader.YTDLPDownloader
}

// ListChannelVideos satisfies monitor.MonitorDownloaderPort. The
// request shape is translated verbatim (struct field names are
// stable across both infra / domain shapes); the response-slice
// element is projected field-by-field as before.
func (a *monitorYtdlpAdapter) ListChannelVideos(ctx context.Context, query monitor.ListChannelVideosQuery) ([]monitor.VideoInfo, error) {
	if a == nil || a.inner == nil {
		return nil, nil
	}
	req := downloader.ListChannelVideosRequest{
		ChannelURL:  query.ChannelURL,
		DateAfter:   query.DateAfter,
		PlaylistEnd: query.PlaylistEnd,
	}
	rawList, err := a.inner.ListChannelVideos(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]monitor.VideoInfo, len(rawList))
	for i, v := range rawList {
		out[i] = monitor.VideoInfo{
			ID:       v.ID,
			Title:    v.Title,
			Views:    v.Views,
			Duration: v.Duration,
		}
	}
	return out, nil
}

// Path satisfies monitor.MonitorDownloaderPort. Forwarded verbatim.
func (a *monitorYtdlpAdapter) Path() string {
	if a == nil || a.inner == nil {
		return ""
	}
	return a.inner.Path()
}

// newMonitorYtdlpAdapter constructs the inline ytdlp→monitor bridge.
func newMonitorYtdlpAdapter(ytdlp *downloader.YTDLPDownloader) monitor.MonitorDownloaderPort {
	return &monitorYtdlpAdapter{inner: ytdlp}
}

// Compile-time assertion: monitorYtdlpAdapter satisfies the
// canonical monitor MonitorDownloaderPort.
var _ monitor.MonitorDownloaderPort = (*monitorYtdlpAdapter)(nil)

// ── monitorDiscoveriesAdapter ─────────────────────────────────

// monitorDiscoveriesAdapter wraps *youtubediscoveries.YoutubeDiscoveriesRepository
// (the infra-side ledger DB producer) so the channel-monitor's typed
// port `monitor.YoutubeDiscoveriesPort` (the domain DTO / sentinel
// consumer) is satisfied.
//
// FASE 3.7 Commit 1b (2026-07-04): the mismatch is two-fold:
//   - Return slice (DrainPendingOutbox/DrainDispatched): infra returns
//     `[]assets.OutboxEntry`; the monitor port expects
//     `[]monitor.OutboxEntry`.
//   - Sentinel error (`MarkEnqueued` / `MarkRejected` /
//     `CommitEnqueueOutbox`): infra wraps `youtubediscoveries.ErrStateConflict`;
//     the monitor port's callers pattern-match against
//     `monitor.ErrLedgerStateConflict`.
//
// Per godlike/06 (one owner per fact) + godlike/07 (no-fake-availability)
// + the FASE 3.7 commitment (zero infra imports in
// `internal/application/assets/monitor/`), the canonical resolution is
// the composition-root adapter pattern: monitor owns canonical
// sentinels + DTOs locally; infra owns its own sentinels + row shapes;
// the ONLY point where both come together is the composition root
// where this adapter translates between them.
type monitorDiscoveriesAdapter struct {
	*youtubediscoveries.YoutubeDiscoveriesRepository
}

// DrainPendingOutbox translates `[]assets.OutboxEntry` →
// `[]monitor.OutboxEntry` (struct fields are stable: ID, DiscoveryID,
// IdempotencyKey, PayloadJSON, State, RetryCount, NextRetryAt —
// element-wise copy preserves order).
func (a *monitorDiscoveriesAdapter) DrainPendingOutbox(ctx context.Context, limit int, leaseID, leaseUntil string) ([]monitor.OutboxEntry, error) {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil, nil
	}
	rows, err := a.YoutubeDiscoveriesRepository.DrainPendingOutbox(ctx, limit, leaseID, leaseUntil)
	if err != nil {
		return nil, mapDiscoveriesErr(err)
	}
	out := make([]monitor.OutboxEntry, len(rows))
	for i, e := range rows {
		out[i] = monitor.OutboxEntry{
			ID:             e.ID,
			DiscoveryID:    e.DiscoveryID,
			IdempotencyKey: e.IdempotencyKey,
			PayloadJSON:    e.PayloadJSON,
			State:          e.State,
			RetryCount:     e.RetryCount,
			NextRetryAt:    e.NextRetryAt,
		}
	}
	return out, nil
}

// DrainDispatched translates `[]assets.OutboxEntry` →
// `[]monitor.OutboxEntry` (same element-wise copy as
// DrainPendingOutbox — both paths read the same `monitor_enqueue_outbox`
// row shape).
func (a *monitorDiscoveriesAdapter) DrainDispatched(ctx context.Context, limit int, leaseID, leaseUntil string) ([]monitor.OutboxEntry, error) {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil, nil
	}
	rows, err := a.YoutubeDiscoveriesRepository.DrainDispatched(ctx, limit, leaseID, leaseUntil)
	if err != nil {
		return nil, mapDiscoveriesErr(err)
	}
	out := make([]monitor.OutboxEntry, len(rows))
	for i, e := range rows {
		out[i] = monitor.OutboxEntry{
			ID:             e.ID,
			DiscoveryID:    e.DiscoveryID,
			IdempotencyKey: e.IdempotencyKey,
			PayloadJSON:    e.PayloadJSON,
			State:          e.State,
			RetryCount:     e.RetryCount,
			NextRetryAt:    e.NextRetryAt,
		}
	}
	return out, nil
}

// MarkEnqueued translates `youtubediscoveries.ErrStateConflict` →
// `monitor.ErrLedgerStateConflict` (multi-%w wrap chain — Go 1.20+).
func (a *monitorDiscoveriesAdapter) MarkEnqueued(ctx context.Context, id, enqueuedAt string) error {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil
	}
	return mapDiscoveriesErr(a.YoutubeDiscoveriesRepository.MarkEnqueued(ctx, id, enqueuedAt))
}

// MarkRejected translates `youtubediscoveries.ErrStateConflict` →
// `monitor.ErrLedgerStateConflict` (same multi-%w wrap shape).
func (a *monitorDiscoveriesAdapter) MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil
	}
	return mapDiscoveriesErr(a.YoutubeDiscoveriesRepository.MarkRejected(ctx, id, rejectionReason, retryable))
}

// CommitEnqueueOutbox translates `youtubediscoveries.ErrStateConflict` and
// `monitor_outbox.ErrDuplicateOutboxKey` (the latter is infra-side
// idempotency sentinel, not yet re-exported in monitor — the adapter
// just passes the error through with the SSOT wrap). Duplicate-key
// errors do NOT match `monitor.ErrLedgerStateConflict` (they are not
// state preconditions), so callers continue to treat them as
// idempotent and not as terminal failures.
func (a *monitorDiscoveriesAdapter) CommitEnqueueOutbox(ctx context.Context, discoveryID, enqueuedAt, idempotencyKey, payloadJSON string) error {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil
	}
	return mapDiscoveriesErr(a.YoutubeDiscoveriesRepository.CommitEnqueueOutbox(ctx, discoveryID, enqueuedAt, idempotencyKey, payloadJSON))
}

// mapDiscoveriesErr is the canonical sentinel-translator between
// the infra-side `youtubediscoveries.ErrStateConflict` and the monitor-side
// `monitor.ErrLedgerStateConflict`. nil → nil. errors.Is(err,
// youtubediscoveries.ErrStateConflict) → delegates to `monitor.TranslateLedgerSentinel`
// (the public monitor-package helper that does the actual multi-%w
// wrap). Any other error → passed through unchanged.
func mapDiscoveriesErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, youtubediscoveries.ErrStateConflict) {
		return monitor.TranslateLedgerSentinel(err)
	}
	return err
}

// newMonitorDiscoveriesAdapter constructs the inline assets→monitor
// bridge. Returns a valid monitor.YoutubeDiscoveriesPort even when
// repo is nil (no-op: every method surfaces nil-or-empty so a
// missing wire silently fails-soft).
func newMonitorDiscoveriesAdapter(repo *youtubediscoveries.YoutubeDiscoveriesRepository) monitor.YoutubeDiscoveriesPort {
	if repo == nil {
		return nil
	}
	return &monitorDiscoveriesAdapter{YoutubeDiscoveriesRepository: repo}
}

// Compile-time assertion: monitorDiscoveriesAdapter satisfies the
// canonical monitor YoutubeDiscoveriesPort.
var _ monitor.YoutubeDiscoveriesPort = (*monitorDiscoveriesAdapter)(nil)

// ── uploadIntentsAdapter ──────────────────────────────────────

// uploadIntentsAdapter wraps *scripts.UploadIntentsRepository so the
// voiceover.OrphanSweeper's typed port
// `voiceover.UploadIntentsRepository` is satisfied. The only mismatch
// is InsertTx's options type:
//
//   - scripts.InsertUploadIntentOptions{ VoiceoverID, Attempts }
//   - voiceover.UploadIntentInsertOptions{ VoiceoverID, Attempts }
//
// Struct fields are stable and identical; the adapter re-binds them
// inline. No other Repository methods differ — all other methods
// (MarkUploaded / MarkFinalized / MarkCompleted / MarkFailed /
// ListPending / BeginTx) are inherited from the embedded
// *scripts.UploadIntentsRepository via Go's struct embedding promotion.
type uploadIntentsAdapter struct {
	*scripts.UploadIntentsRepository
}

// InsertTx satisfies voiceover.UploadIntentsRepository. The option
// struct is re-bound with identical field values so the SQLite
// repository sees the canonical infra type it expects.
func (a *uploadIntentsAdapter) InsertTx(ctx context.Context, tx *sql.Tx, opts *voiceover.UploadIntentInsertOptions) (int64, error) {
	if a == nil || a.UploadIntentsRepository == nil {
		return 0, fmt.Errorf("uploadIntentsAdapter: nil repository")
	}
	return a.UploadIntentsRepository.InsertTx(ctx, tx, &scripts.InsertUploadIntentOptions{
		VoiceoverID: opts.VoiceoverID,
		Attempts:    opts.Attempts,
	})
}

// ListPending satisfies voiceover.UploadIntentsRepository. The
// scripts.UploadIntent row type is converted element-wise to the
// voiceover.UploadIntent domain shape. UpdatedUnix is computed from
// UpdatedAt so the wire stays a single int64 (avoiding leaking
// time.Time into the application-layer port).
func (a *uploadIntentsAdapter) ListPending(ctx context.Context, olderThan time.Time) ([]voiceover.UploadIntent, error) {
	if a == nil || a.UploadIntentsRepository == nil {
		return nil, nil
	}
	rows, err := a.UploadIntentsRepository.ListPending(ctx, olderThan)
	if err != nil {
		return nil, err
	}
	out := make([]voiceover.UploadIntent, 0, len(rows))
	for _, r := range rows {
		out = append(out, voiceover.UploadIntent{
			ID:          r.ID,
			VoiceoverID: r.VoiceoverID,
			DriveFileID: r.DriveFileID,
			Status:      r.Status,
			Reason:      r.Reason,
			Attempts:    r.Attempts,
			UpdatedUnix: r.UpdatedAt.Unix(),
		})
	}
	return out, nil
}

// newUploadIntentsAdapter constructs the inline scripts→voiceover
// bridge. Returns a valid voiceover.UploadIntentsRepository even when
// repo is nil so partial-deploy paths log+skip cleanly.
func newUploadIntentsAdapter(repo *scripts.UploadIntentsRepository) voiceover.UploadIntentsRepository {
	if repo == nil {
		return nil
	}
	return &uploadIntentsAdapter{UploadIntentsRepository: repo}
}

// MarkUploaded overrides the promoted method to translate the infra
// sentinel (scripts.ErrUploadIntentNotFound) to the application-layer
// sentinel (voiceover.ErrUploadIntentNotFound) so callers in the
// voiceover package never import the infra package directly.
func (a *uploadIntentsAdapter) MarkUploaded(ctx context.Context, voiceoverID, driveFileID string) error {
	if a == nil || a.UploadIntentsRepository == nil {
		return fmt.Errorf("uploadIntentsAdapter: nil repository")
	}
	return translateUploadIntentErr(a.UploadIntentsRepository.MarkUploaded(ctx, voiceoverID, driveFileID))
}

// MarkFinalized overrides the promoted method to translate the infra
// sentinel (scripts.ErrUploadIntentNotFound) to the application-layer
// sentinel (voiceover.ErrUploadIntentNotFound).
func (a *uploadIntentsAdapter) MarkFinalized(ctx context.Context, voiceoverID string) error {
	if a == nil || a.UploadIntentsRepository == nil {
		return fmt.Errorf("uploadIntentsAdapter: nil repository")
	}
	return translateUploadIntentErr(a.UploadIntentsRepository.MarkFinalized(ctx, voiceoverID))
}

// MarkCompleted overrides the promoted method to translate the infra
// sentinel (scripts.ErrUploadIntentNotFound) to the application-layer
// sentinel (voiceover.ErrUploadIntentNotFound).
func (a *uploadIntentsAdapter) MarkCompleted(ctx context.Context, voiceoverID string) error {
	if a == nil || a.UploadIntentsRepository == nil {
		return fmt.Errorf("uploadIntentsAdapter: nil repository")
	}
	return translateUploadIntentErr(a.UploadIntentsRepository.MarkCompleted(ctx, voiceoverID))
}

// MarkFailed overrides the promoted method to translate the infra
// sentinel (scripts.ErrUploadIntentNotFound) to the application-layer
// sentinel (voiceover.ErrUploadIntentNotFound).
func (a *uploadIntentsAdapter) MarkFailed(ctx context.Context, voiceoverID, reason string) error {
	if a == nil || a.UploadIntentsRepository == nil {
		return fmt.Errorf("uploadIntentsAdapter: nil repository")
	}
	return translateUploadIntentErr(a.UploadIntentsRepository.MarkFailed(ctx, voiceoverID, reason))
}

// translateUploadIntentErr translates the infra-level
// scripts.ErrUploadIntentNotFound sentinel to the application-layer
// voiceover.ErrUploadIntentNotFound so the voiceover package never
// imports the infra package directly. The multi-%w wrap preserves the
// original error for audit while surfacing the application-layer
// sentinel for errors.Is matching.
//
// Intentionally narrow: only ErrUploadIntentNotFound is translated.
// Other infra sentinels (SQL errors, constraint violations) pass
// through unchanged because the voiceover package's callers only
// pattern-match on ErrUploadIntentNotFound for idempotent-skip
// decisions.
func translateUploadIntentErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, scripts.ErrUploadIntentNotFound) {
		return fmt.Errorf("%w: %w", voiceover.ErrUploadIntentNotFound, err)
	}
	return err
}

// Compile-time assertion: uploadIntentsAdapter satisfies the
// canonical voiceover UploadIntentsRepository.
var _ voiceover.UploadIntentsRepository = (*uploadIntentsAdapter)(nil)

// ── FASE 3.7 Commit 2 monitor.MetricsRecorder compile-time assertion ──

// Compile-time assertion (FASE 3.7 Commit 2, 2026-07-04):
// *observability.ObservabilityMetricsRecorder satisfies the
// canonical monitor.MetricsRecorder port. The structural identity
// between the observability-side adapter and the application-side
// port is pinned HERE (and only here) because lifecycle_adapters.go
// imports both monitor + observability without creating an import
// cycle — the production-time pinning location. Drift between adapter
// methods and port methods is a build-time failure at this line.
//
// (Alternative pin locations and why they're wrong:
//   - metrics_adapter.go: production import of monitor from
//     observability would create an infra→app circular import.
//   - metrics_adapter_test.go: an earlier draft of the test file
//     imported monitor for the assertion + tests, but pulled in the
//     monitor → channels → assets → outbox → observability chain,
//     creating a Go package cycle in TEST scope. The test file was
//     simplified to drop the monitor import; the assertion lives
//     here at the composition root instead.)
var _ monitor.MetricsRecorder = (*observability.ObservabilityMetricsRecorder)(nil)
