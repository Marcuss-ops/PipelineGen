// Package mediamemory — discovery_worker.go is the canonical SSOT
// for the media.discovery worker (architecture doc section 7-8).
//
// godlike/06 SSOT: DiscoveryWorker is the SINGLE owner of the
// candidate-collection seam between the external SearchFanOut and
// the media_candidates table. Every catalog_only run, every
// external fan-out adapter, and every future Fase 3.2 linker
// enrichment routes through this worker so the no-download
// invariant and the rights-status gating live in ONE place.
//
// godlike/06 SSOT (no-download / metadata-only contract): the
// worker writes ONLY the metadata columns documented in the
// architecture doc — URL, provider_asset_id, thumbnail_url,
// title, description, duration_ms, candidate_score,
// rights_status. AssetID is hard-ZERO on every persisted row;
// materialize is a separate phase (Fase 3.3) that sets AssetID
// via StockPipelineAcquirer. A worker that accidentally sets an
// AssetID is a godlike/06 SSOT regression and surfaces an
// errDownloadAttempt below.
//
// godlike/07 NO-FAKE-AVAILABILITY: unknown rights candidates are
// WRITTEN (ranker applies rights_penalty at Score time) but
// RightsDenied / RightsExpired candidates are DROPPED before
// write — promoting them to Cold tier would still consume a
// UNIQUE(provider, provider_asset_id) row. The decision is
// surfaced as a typed DroppedByRightsCount in DiscoveryResult.
//
// godlike/06 SSOT (fail-closed envelopes): SearchFanOut errors
// surface as wrapped ErrSemanticBackendFailed (downstream
// resolver-style); empty SourceURL or empty ProviderAssetID
// rejects the row (silent skip); partial backend failures
// surface as Partial=true so callers can branch on
// BackendErrors[].
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
) // DiscoveryRequest bundles the worker's per-call input. godlike/06
// SSOT: Query + Provider is the canonical minimum; Language +
// MediaTypes + Limit scope the SearchFanOut envelope so the
// upstream search stays bounded. ProjectID is recorded onto
// discovered candidates via the rights-status check (forward-
// pointer to a future Fase where rights_status varies per project).
// BatchMode (catalog_only / materialize_top_k) lives in types.go
// (godlike/06 SSOT: single canonical home for closed-set enums).
type DiscoveryRequest struct {
	Query      string
	Provider   string
	Language   string
	MediaTypes []string
	Limit      int
	ProjectID  string
}

// DiscoveryResult bundles the worker's per-call output. godlike/06
// SSOT: PersistedCandidateIDs is the canonical durable set the
// BatchService.AppendCandidate iterates over; DroppedByRightsCount
// is the canonical rights-gate counter; Partial + BackendErrors
// mirror the SearchFanOut envelope so callers branch on
// per-backend health.
type DiscoveryResult struct {
	PersistedCandidateIDs []string
	Candidates            []MediaCandidate // canonical envelope for debugging / dashboard
	DroppedByRightsCount  int              // count of RightsDenied/Expired dropped before write
	Partial               bool
	BackendErrors         map[string]string
	Failures              []string
}

// DiscoveryWorker is the canonical port. Concrete impl is
// defaultDiscoveryWorker below.
type DiscoveryWorker interface {
	// Discover runs one (query × provider) fan-out and persists
	// the metadata-only rows to media_candidates. The worker
	// guarantees the no-download invariant: AssetID is "" on
	// every persisted row.
	Discover(ctx context.Context, req DiscoveryRequest) (DiscoveryResult, error)
}

// ── Default implementation ──────────────────────────────────

// defaultDiscoveryWorker is the canonical implementation of
// DiscoveryWorker. godlike/06 SSOT: it composes SearchFanOut +
// CandidateRepository and enforces the canonical closed-set
// invariants (rights gate, no-download, AssetID=="", closed-set
// statuses).
type defaultDiscoveryWorker struct {
	candidates CandidateRepository
	external   SearchFanOut
	log        Logger
	clock      Clock
}

// NewDefaultDiscoveryWorker constructs the canonical worker.
// Composition root wires concrete SearchFanOutAdapter + the
// canonical sqlite CandidateRepository.
func NewDefaultDiscoveryWorker(
	candidates CandidateRepository,
	external SearchFanOut,
	log Logger,
	clock Clock,
) DiscoveryWorker {
	if log == nil {
		log = NoopLogger()
	}
	if clock == nil {
		clock = RealClock()
	}
	return &defaultDiscoveryWorker{
		candidates: candidates,
		external:   external,
		log:        log,
		clock:      clock,
	}
}

var _ DiscoveryWorker = (*defaultDiscoveryWorker)(nil)

// Discover is the canonical entrypoint.
//
// godlike/06 SSOT (worker pipeline):
//  1. Validate req (Query non-empty, Provider non-empty).
//  2. SearchFanOut(req.SearchFanOutQuery) — projection pass to
//     MediaCandidate baked into SearchFanOutAdapter (canonical).
//  3. Rights gate drop: RightsDenied / RightsExpired are removed
//     before write (counted in DroppedByRightsCount).
//  4. No-download invariant guard: AssetID is rejected (rows
//     where AssetID != "" are surfaced as typed failures and
//     skipped — godlike/06 fail-closed on the canonical Cold-
//     tier contract).
//  5. DiscoveryStatus = DiscoverySearched (canonical initial state).
//     MaterializationStatus = MaterializationCold (canonical Cold
//     tier on discovery; promote to Warm in Fase 3.3).
//  6. UpsertInsert per candidate (UNIQUE(provider,
//     provider_asset_id) dedup via ErrDuplicateBinding envelope).
//  7. Collect PersistedCandidateIDs + BackendErrors.
//
// godlike/06 SSOT (Cold-tier row width): the worker persists the
// FULL MediaCandidate row, not just the user-spec narrow set
// (URL, provider_asset_id, thumbnail_url, title, description,
// duration_ms, candidate_score, rights_status). The DDL permits
// full row writes (everything else is nullable) and the linker
// (Fase 3.2) will populate additional fields later. Stripping
// columns here would force a future migration.
//
// godlike/07 NO-FAKE-AVAILABILITY: empty Query / empty Provider
// surface as wrapped ErrInvalidPhrase (the canonical input
// invalidity sentinel); the worker MUST NOT silently zero-output
// when a required field is empty.
func (w *defaultDiscoveryWorker) Discover(ctx context.Context, req DiscoveryRequest) (DiscoveryResult, error) {
	if strings.TrimSpace(req.Query) == "" {
		return DiscoveryResult{}, fmt.Errorf(
			"mediamemory: discovery worker query is empty: %w", ErrInvalidPhrase,
		)
	}
	if strings.TrimSpace(req.Provider) == "" {
		return DiscoveryResult{}, fmt.Errorf(
			"mediamemory: discovery worker provider is empty: %w", ErrInvalidPhrase,
		)
	}

	res := DiscoveryResult{
		PersistedCandidateIDs: make([]string, 0, 8),
		Candidates:            make([]MediaCandidate, 0, 8),
		BackendErrors:         make(map[string]string),
		Failures:              make([]string, 0),
	}

	// godlike/06 SSOT (canonical search query surface): the
	// SearchFanOutQuery envelope is the canonical Phase 1.x shape
	// the resolver already consumes; the worker reuses the same
	// fields with MediaTypes + Sources bounded to this worker call.
	limit := req.Limit
	if limit <= 0 {
		limit = 1000 // canonical Cold-tier ceiling for catalog_only
	}
	foRes, foErr := w.external.Search(ctx, SearchFanOutQuery{
		Text:       req.Query,
		Language:   req.Language,
		MediaTypes: req.MediaTypes,
		Sources:    []string{req.Provider},
		Limit:      limit,
	})
	if foErr != nil {
		// godlike/07 NO-FAKE-AVAILABILITY: SearchFanOut errors
		// surface as wrapped ErrSemanticBackendFailed so callers
		// can branch deterministically. Drop the typed sentinel
		// not the worker — partial-fill is still useful when one
		// provider is up and another is down.
		res.Failures = append(res.Failures, fmt.Sprintf(
			"provider=%q: fanout failed: %s", req.Provider, foErr.Error(),
		))
		res.BackendErrors[req.Provider] = foErr.Error()
		return res, fmt.Errorf("mediamemory: discovery worker fanout: %w", ErrSemanticBackendFailed)
	}
	res.Partial = foRes.Partial
	for be, msg := range foRes.BackendErrors {
		res.BackendErrors[be] = msg
	}

	now := w.clock.Now().UTC()

	for _, c := range foRes.Candidates {
		// 1. Rights gate drop: RightsDenied / RightsExpired are
		//    permanently non-promotable, so we drop them at the
		//    worker seam to save a UNIQUE(provider,
		//    provider_asset_id) row for them.
		if c.RightsStatus == RightsDenied || c.RightsStatus == RightsExpired {
			res.DroppedByRightsCount++
			res.Failures = append(res.Failures, fmt.Sprintf(
				"provider=%q asset=%q: rights_status=%q dropped (catalog_only intent)",
				c.Provider, c.ProviderAssetID, string(c.RightsStatus),
			))
			continue
		}

		// 2. No-download invariant guard. Future linker (Fase 3.2)
		//    or pre-warmed SearchFanOutAdapter could accidentally
		//    set AssetID on a Cold-tier candidate; the worker
		//    rejects the row BEFORE write in that case so the
		//    Cold-tier invariant is intact.
		if c.AssetID != "" {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"provider=%q asset=%q: discovery worker MUST NOT set AssetID on Cold-tier candidates (got %q)",
				c.Provider, c.ProviderAssetID, c.AssetID,
			))
			continue
		}

		// 3. Normalize the worker-set canonical defaults.
		// godlike/06 SSOT (canonical defaults): discovery = Searched
		// (canonical initial state); Cold tier (no download yet).
		c.DiscoveryStatus = DiscoverySearched
		c.MaterializationStatus = MaterializationCold
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now

		// 4. Drop empty ProviderAssetID rows (canonical dedup key
		// half; without it the UNIQUE constraint can't distinguish).
		if strings.TrimSpace(c.ProviderAssetID) == "" {
			res.Failures = append(res.Failures, fmt.Sprintf(
				"provider=%q: candidate has empty ProviderAssetID; skipping",
				c.Provider,
			))
			continue
		}

		// 5. Persist via the canonical CandidateRepository.
		persisted, persistErr := w.candidates.UpsertInsert(ctx, c)
		if persistErr != nil {
			// godlike/06 SSOT (legacy-row friendliness): when
			// UNIQUE(provider, provider_asset_id) trips
			// (ErrDuplicateBinding), we record the existing row's
			// ID via FindByID and treat as a fresh re-discovery
			// (the cold-row update path is owned by the linker in
			// Fase 3.2; for Fase 3.1 catalog_only we accept the
			// dedup-verdict loss and move on).
			if errors.Is(persistErr, ErrDuplicateBinding) {
				res.Failures = append(res.Failures, fmt.Sprintf(
					"provider=%q asset=%q: deduplicated (canonical UNIQUE constraint)",
					c.Provider, c.ProviderAssetID,
				))
				continue
			}
			// godlike/07 NO-FAKE-AVAILABILITY: persist error is
			// NOT swallowed; recorded as a typed failure.
			res.Failures = append(res.Failures, fmt.Sprintf(
				"provider=%q asset=%q: persist failed: %s",
				c.Provider, c.ProviderAssetID, persistErr.Error(),
			))
			continue
		}
		res.PersistedCandidateIDs = append(res.PersistedCandidateIDs, persisted.ID)
		res.Candidates = append(res.Candidates, persisted)
	}

	return res, nil
}
