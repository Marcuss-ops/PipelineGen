package cliprender

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ChrononTimingFetcher retrieves the verbatim `<output>.timing.json` Chronon
// timing sidecar referenced by a render outcome's content-addressed locator
// (Outcome.ChrononTimingStorageKey / ChrononTimingURL).
//
// It is a PORT, not a concrete: the render capability must not learn the
// object store's wire shape. The composition root binds the HTTP client that
// speaks the store contract.
type ChrononTimingFetcher interface {
	FetchChrononTiming(ctx context.Context, storageKey, url string) ([]byte, error)
}

// chrononTimingFetchTimeout bounds the sidecar fetch. The sidecar is
// diagnostics: a slow or hanging object store must not extend a certified
// render's completion, so the fetch is capped well below any render timeout.
const chrononTimingFetchTimeout = 15 * time.Second

// SetChrononMetrics wires the canonical Chronon phase projection: the adapter
// that writes measured exclusive-wall phases into performance_operations
// through the OperationReportProjectionRecorder seam, plus the fetcher that
// retrieves the sidecar bytes it consumes.
//
// Nil-safe and both-halves-required: a nil adapter (no performance store) or a
// nil fetcher (no object store) leaves the projection off, which is exactly
// the state before this wiring existed — the timing reference is still
// recorded on the job result, it is simply not projected. Never a partial
// wiring that would fetch bytes nobody records or record facts nobody fetched.
func (w *Worker) SetChrononMetrics(adapter *ChrononMetricsAdapter, fetcher ChrononTimingFetcher) {
	if w == nil {
		return
	}
	if adapter == nil || fetcher == nil {
		w.chrononMetrics = nil
		w.chrononTimingFetcher = nil
		return
	}
	w.chrononMetrics = adapter
	w.chrononTimingFetcher = fetcher
}

// publishChrononTiming is the single owner of "sidecar bytes → canonical
// phase rows". It fetches the referenced sidecar, parses it once
// (ParseChrononSidecar) and publishes every measured phase.
//
// FAIL-OPEN, deliberately: a render that produced certified bytes is complete
// whether or not its metrics could be recorded. Every failure path logs and
// returns; the caller's result is unaffected. A no-op when the projection is
// unwired or the outcome carries no timing reference (a legacy/test renderer).
func (w *Worker) publishChrononTiming(ctx context.Context, outcome *RenderOutcome, prepared *Prepared, probe *OutputProbe) {
	if w == nil || w.chrononMetrics == nil || w.chrononTimingFetcher == nil || outcome == nil {
		return
	}
	if strings.TrimSpace(outcome.ChrononTimingStorageKey) == "" && strings.TrimSpace(outcome.ChrononTimingURL) == "" {
		return
	}

	fetchCtx, cancel := context.WithTimeout(ctx, chrononTimingFetchTimeout)
	defer cancel()
	raw, err := w.chrononTimingFetcher.FetchChrononTiming(fetchCtx, outcome.ChrononTimingStorageKey, outcome.ChrononTimingURL)
	if err != nil {
		w.log.Warn("chronon timing sidecar fetch failed; phase projection skipped",
			zap.String("storage_key", outcome.ChrononTimingStorageKey),
			zap.Error(err),
		)
		return
	}
	sidecar, err := ParseChrononSidecar(raw)
	if err != nil {
		w.log.Warn("chronon timing sidecar parse failed; phase projection skipped",
			zap.String("storage_key", outcome.ChrononTimingStorageKey),
			zap.Error(err),
		)
		return
	}

	// Only facts that are actually known are passed; the adapter omits
	// unknown metadata keys rather than fabricating them. The certified
	// probe supplies the output geometry, the materialized source supplies
	// the input identity.
	opts := ChrononMetricsPublishOptions{OutputSizeBytes: outcome.SizeBytes}
	if probe != nil {
		opts.Width = probe.Width
		opts.Height = probe.Height
		opts.FPS = probe.FPS
	}
	if prepared != nil && prepared.Source != nil {
		opts.SourceSHA256 = prepared.Source.SHA256
		opts.SourceDurationMS = prepared.Source.DurationMS
	}
	w.chrononMetrics.Publish(ctx, sidecar, opts)
}
