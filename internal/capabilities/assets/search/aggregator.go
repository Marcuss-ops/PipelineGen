// Package search — aggregator.go is the Wave 21 PR 9 BACKFILL
// implementation of the canonical SearchAggregator. It replaces
// the PR 8 NOOP stub with the real fanout + dedup + ranking
// pipeline described by the project plan.
//
// Pipeline (PR 9):
//  1. Decode cursor  → SkipSet (dedup.go::SkipSetFromCursor).
//  2. Trim text + clamp limit (q.Limit; default DefaultLimit;
//     capped MaxLimit).
//  3. Pick eligible backends (BackendRegistry.Eligible(q)) —
//     filtered by Query.MediaTypes ∩ Backend.Capabilities.
//  4. Fan-out: pkg/concurrent errgroup + per-backend timeout via
//     context.WithTimeout. Per PR 9 spec the defaults are:
//     provider-style backends: 5s
//     "local" backend:          2s
//     "semantic" backend:       8s
//     The composition root can override by populating
//     a.perBackendTimeouts before calling Search.
//  5. Per-backend Search errors DO NOT cancel the whole search;
//     they land in Result.ProviderErrors[name] and set
//     Result.Partial = true. "Partial preferred" per Wave 21
//     survey of the search subsystem: one slow backend must
//     never starve the others or kill the response.
//  6. Pool candidates across backends → Merge(skips dedup by
//     4-key). Then RankByScore (Score DESC, Source ASC,
//     AssetID ASC).
//  7. Trim to q.Limit. Build NextCursor from the last served
//     items via EncodeCursorFromItems + EncodeCursor (wire
//     base64 form).
//  8. Return Result{Items, NextCursor, ProviderErrors, Partial}.
package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Per-backend timeout defaults (PR 9 spec): provider = 5s,
// local = 2s, semantic = 8s. Export as constants so tests + the
// composition root can pin them. Composition-root overrides flow
// through Aggregator.perBackendTimeouts (constructor or
// WithPerBackendTimeouts).
const (
	DefaultProviderBackendTimeout = 5 * time.Second
	DefaultLocalBackendTimeout    = 2 * time.Second
	DefaultSemanticBackendTimeout = 8 * time.Second
)

// PerBackendTimeout returns the per-backend timeout for a given
// backend Name. Overrides in overrides take precedence; otherwise
// the canonical buckets ("local", "semantic") get their dedicated
// defaults and any other name (provider-style) falls back to
// DefaultProviderBackendTimeout.
//
// Composition root: populate overrides via:
//
//	a.SetPerBackendTimeouts(map[string]time.Duration{"artlist": 7*time.Second})
//
// before exposing the Aggregator to handlers.
func PerBackendTimeout(name string, overrides map[string]time.Duration) time.Duration {
	if d, ok := overrides[name]; ok {
		return d
	}
	switch name {
	case "local":
		return DefaultLocalBackendTimeout
	case "semantic":
		return DefaultSemanticBackendTimeout
	default:
		return DefaultProviderBackendTimeout
	}
}

// Aggregator fans out a Query across registered SearchBackends
// and merges the results per the Wave 21 PR 9 pipeline above.
type Aggregator struct {
	backends           *BackendRegistry
	log                Logger
	perBackendTimeouts map[string]time.Duration
}

// NewAggregator constructs the Aggregator. backends may be nil —
// a fresh in-memory registry is substituted (matches patterns in
// the rest of internal/capabilities/*). log may be nil — a
// noopLogger is substituted so Search never panics on a missing
// logger in tests.
func NewAggregator(backends *BackendRegistry, log Logger) *Aggregator {
	if log == nil {
		log = noopLogger{}
	}
	if backends == nil {
		backends = NewBackendRegistry()
	}
	return &Aggregator{
		backends:           backends,
		log:                log,
		perBackendTimeouts: make(map[string]time.Duration),
	}
}

// SetPerBackendTimeouts wires composition-root overrides into the
// aggregator. Safe to call before any Search; concurrent reads
// during Search see a partially-applied map but the lookup is
// idempotent (worst case a single request uses a partially-loaded
// override set). For PR 9 the call is performed once at
// composition wiring.
func (a *Aggregator) SetPerBackendTimeouts(overrides map[string]time.Duration) {
	if overrides == nil {
		return
	}
	a.perBackendTimeouts = overrides
}

// Backends returns the registry the aggregator was constructed
// with. Useful for diagnostics + tests.
func (a *Aggregator) Backends() *BackendRegistry {
	return a.backends
}

// Search runs the full PR 9 pipeline with Fase 6 fail-closed
// hardening:
//   - Cursor decode errors → propagate (handlers map to 422).
//   - No eligible backends → typed error (ErrNoEligibleBackends
//     or ErrSemanticBackendUnavailable for hybrid mode).
//   - All backends failed → ErrAllBackendsFailed with
//     ProviderErrors preserved in the returned Result.
//   - Partial (some backends failed, some succeeded) →
//     Result.Partial = true, ProviderErrors populated.
//   - Cursor encoding failure → ErrCursorEncoding.
//
// Single-backend errors are demoted to Result.ProviderErrors +
// Result.Partial (unchanged from PR 9).
func (a *Aggregator) Search(ctx context.Context, q Query) (*Result, error) {
	cur, err := DecodeCursor(q.Cursor)
	if err != nil {
		return nil, err
	}
	skipSet := SkipSetFromCursor(cur)

	q.Text = strings.TrimSpace(q.Text)
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	// T4 fix: when a cursor is present, backends must return MORE
	// items than the final page size so that after skip-set dedup
	// removes previously-served items, there are still enough
	// candidates to fill the page. Without this, backends return
	// exactly `limit` items — if all of them were on the previous
	// page, the skip-set removes them all and page 2 is empty.
	if len(skipSet) > 0 {
		effectiveBackendLimit := limit + len(skipSet)
		if effectiveBackendLimit > MaxLimit {
			effectiveBackendLimit = MaxLimit
		}
		q.Limit = effectiveBackendLimit
	}

	eligible := a.backends.Eligible(q)
	if len(eligible) == 0 {
		if len(a.backends.All()) == 0 {
			a.log.Debug("search.Aggregator.Search: no registered backends",
				"text_len", len(q.Text),
				"media_types", q.MediaTypes,
				"mode", q.Mode,
			)
			return &Result{
				Items:          []Candidate{},
				NextCursor:     "",
				ProviderErrors: map[string]string{},
				Partial:        false,
			}, nil
		}
		a.log.Debug("search.Aggregator.Search: no eligible backends",
			"text_len", len(q.Text),
			"media_types", q.MediaTypes,
			"mode", q.Mode,
		)
		// Fase 6 fail-closed: never return an empty-but-valid
		// Result when zero backends matched. Hybrid mode gets
		// a dedicated sentinel so callers can distinguish
		// "semantic backend missing" from "no backend at all."
		if q.Mode == SearchModeHybrid {
			return nil, ErrSemanticBackendUnavailable
		}
		return nil, ErrNoEligibleBackends
	}

	// Per-backend outcomes pre-allocated by index so goroutines
	// can write to their own slot without contention. The simple
	// "first-error-wins" policy of concurrent.Group is overridden:
	// we IGNORE the first error so a single backend failure does
	// not cancel other backends. Partial preferred.
	outcomes := make([]backendOutcome, len(eligible))

	g, fanoutCtx := concurrent.WithContext(ctx)
	for i := range eligible {
		i, b := i, eligible[i]
		timeout := PerBackendTimeout(b.Name(), a.perBackendTimeouts)
		// context.WithTimeout inside the fanout context: backend
		// completes within timeout OR the per-backend context is
		// cancelled and the backend sees ctx.Done(). Defer cancel
		// inside the goroutine to free the timer early on fast
		// completion.
		perCtx, perCancel := context.WithTimeout(fanoutCtx, timeout)
		g.Go(b.Name(), func() error {
			items, berr := b.Search(perCtx, q)
			perCancel()
			outcomes[i] = backendOutcome{
				backendName: b.Name(),
				items:       items,
				err:         berr,
			}
			return nil // see comment above
		})
	}
	_ = g.Wait() // first-error is intentionally swallowed; partial preferred.

	pending := make([]Candidate, 0, len(eligible)*DefaultLimit)
	providerErrors := make(map[string]string)
	partial := false
	failedCount := 0
	for _, o := range outcomes {
		if o.err != nil {
			providerErrors[o.backendName] = o.err.Error()
			failedCount++
			partial = true
			continue
		}
		if len(o.items) > 0 {
			pending = append(pending, o.items...)
		}
	}

	// Fase 6 fail-closed: when EVERY backend errored, return
	// a Result with Partial=true AND ProviderErrors populated
	// alongside the typed error. Callers inspect ProviderErrors
	// for per-backend diagnostics; the error signals "all failed"
	// for status-code routing (502 Bad Gateway).
	if failedCount == len(outcomes) {
		return &Result{
			Items:          []Candidate{},
			NextCursor:     "",
			ProviderErrors: providerErrors,
			Partial:        true,
		}, fmt.Errorf("%w: %d backend(s) failed", ErrAllBackendsFailed, failedCount)
	}

	merged := Merge(pending, skipSet)
	if len(merged) > limit {
		merged = merged[:limit]
	}

	var next string
	if len(merged) > 0 {
		cursorObj, cerr := EncodeCursorFromItems(merged)
		if cerr != nil {
			return nil, fmt.Errorf("%w: %v", ErrCursorEncoding, cerr)
		}
		wire, werr := EncodeCursor(cursorObj)
		if werr != nil {
			return nil, fmt.Errorf("%w: %v", ErrCursorEncoding, werr)
		}
		next = wire
	}

	a.log.Info("search.Aggregator.Search completed",
		"text_len", len(q.Text),
		"eligible_backends", len(eligible),
		"partial", partial,
		"items", len(merged),
	)
	return &Result{
		Items:          merged,
		NextCursor:     next,
		ProviderErrors: providerErrors,
		Partial:        partial,
	}, nil
}

// backendOutcome is per-backend fan-out result captured into a
// pre-allocated slice slot to avoid mutex contention. Naming is
// stable; tests inspect it via the public Result.
type backendOutcome struct {
	backendName string
	items       []Candidate
	err         error
}
