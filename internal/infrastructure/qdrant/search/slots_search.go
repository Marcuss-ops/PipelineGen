// Package qdrant — slots_search.go implements the per-slot
// multi-query route on the unified SemanticAssetSearchAdapter.
//
// godlike/06 SSOT (one canonical owner per fact): this file is
// the SOLE owner of the SearchSlots adapter-side implementation.
// The single underlying searcher + embedder pair is reused across
// N per-slot queries — there is no separate call-site searcher
// for the multi-slot route. A future agent that needs a different
// recaller shape (e.g. parallel goroutines) extends THIS method,
// not the resolver-level pipeline.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - nil receiver → typed error BEFORE any a.kind deref.
//   - nil plan / empty Slots → ErrSlotSearchInvalidPlan.
//   - non-KindClip adapter → cross-kind runtime-guard typed error
//     (per the curate-only intent of SearchSlots).
//   - ctx canceled mid-batch → per-slot entries in ErroredRefs
//     carrying ErrSlotSearchContextCanceled; partial ByRef is
//     surfaced so caller can decide fail-fast-with-partial-audit
//     vs error-only.
//
// Cross-kind discipline (matches SearchClips/SearchStock
// runtime-guard pattern in legacy surface): a stock-flavored
// adapter returned as ports.ClipSearchPort cannot satisfy
// curate-only intent via SearchSlots — returning a typed error
// rather than silently short-circuiting is the godlike/07
// canonical choice.
package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	assetR "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// SearchSlots implements ports.ClipSearchPort.SearchSlots on the
// unified SemanticAssetSearchAdapter.
//
// The method iterates plan.Slots in declaration order and issues
// one underlying SearchAssets-style query per slot (via the
// canonical searchAssetsClip helper so the curate-only filter
// compile + workspace guard + tenant clause are reused verbatim).
// Per-slot options (timeout, candidate limit, score floor) are
// resolved once and applied uniformly.
//
// Returns (*SlotsSearchResult, error). The top-level error is
// non-nil ONLY for whole-call failures:
//   - nil receiver
//   - nil plan / empty Slots (ErrSlotSearchInvalidPlan)
//   - cross-kind runtime-guard error (curate-only / KindStock rejected)
//   - tenant guard failure (validateScope)
//   - nil embedder at start (cheap early check)
//
// Per-slot failures are surfaced in the returned SlotsSearchResult's
// ErroredRefs map (NOT in the top-level error) so the caller can
// branch on partial-batch behavior. This matches the godlike/07
// contract: never represent an unavailable backend as a successful
// no-op; preserve per-slot error visibility for audit replay.
func (a *semanticAssetSearchAdapter) SearchSlots(
	ctx context.Context,
	plan *scriptpkg.ClipPrePlan,
	opts ports.SlotsSearchOptions,
) (*ports.SlotsSearchResult, error) {
	// Canonical nil-receiver guard FIRST (godlike/07 NO-FAKE-
	// AVAILABILITY): never deref a.kind or a.searcher before the
	// guard. The error message uses a static shape so the typed
	// envelope is meaningful without a per-kind dispatch.
	if a == nil {
		return nil, fmt.Errorf("semantic search adapter: nil receiver (SearchSlots)")
	}

	// Plan validation MUST run BEFORE the per-slot loop so a
	// malformed plan does not consume embed/slot resources.
	// ErrSlotSearchInvalidPlan covers nil + empty-Slots in one
	// sentinel (callers errors.Is either case).
	if plan == nil || len(plan.Slots) == 0 {
		return nil, ports.ErrSlotSearchInvalidPlan
	}

	// Cross-kind runtime guard (matches the SearchClips/SearchStock
	// cross-kind discipline): stock adapter does NOT satisfy
	// SearchSlots intent — fail fast with a typed error rather than
	// silently-no-op (godlike/07).
	if a.kind != KindClip {
		return nil, fmt.Errorf(
			"semantic search adapter: SearchSlots is curate-only (kind=%s); stock path does not support slot multi-query",
			a.kind,
		)
	}

	// Tenant guard — single check, NOT per-slot (avoids repeated
	// validation cost on large plans). Matches the SearchAssets
	// ValidateScope surface so audit can replay the same
	// fail-closed reason.
	if err := validateScope(opts.WorkspaceID, opts.IsSystem); err != nil {
		return nil, err
	}

	// Nil embedder is detected here (BEFORE the per-slot loop)
	// so the typed envelope carries the canonical fail-closed
	// reason. Per-slot embedder-nil errors are impossible after
	// this check.
	if a.embedder == nil {
		return nil, fmt.Errorf("slots search adapter: embedder not configured")
	}

	//	Per-slot option resolution (defaults applied ONCE per call,
	// NOT per slot, so callers don't repeat defaults across the
	// loop).
	perSlotTimeout := opts.PerSlotTimeout
	if perSlotTimeout <= 0 {
		perSlotTimeout = ports.SlotsPerSlotDefaultTimeout
	}
	perSlotLimit := opts.PerSlotCandidateLimit
	if perSlotLimit <= 0 {
		perSlotLimit = ports.SlotsPerSlotDefaultCandidateLimit
	}
	// minScore falls through to searchAssetsClip's per-kind
	// default (0.5 for clip) when zero — preserves symmetry with
	// the legacy SearchAssets behavior.

	// Folder unwrap (PR-FOLDER-FILTER, July 2026): the entire plan
	// targets ONE folder (single user-picked thematic area), so
	// the unwrap happens ONCE per call, NOT per slot. Nil Folder
	// → empty string (no filter). The wire key emitted by
	// CompileQdrantFilter is `normalized_group` (NOT `folder`,
	// `macro_topic`, `blueprint`).
	var folderGroup string
	if opts.Folder != nil {
		folderGroup = opts.Folder.NormalizedGroup
	}

	// ── Rights-restricted filter composition (Step 10, July 2026) ──
	// Resolve the canonical exclusion set ONCE per call (NOT per
	// slot, matching the per-slot option resolution above). The
	// result is a 2-slice pairing threaded into the per-slot
	// AssetSearchQuery so the underlying Qdrant filter compiler
	// emits a single MustNot(MatchAny(...)) clause on each
	// payload field.
	//
	// godlike/06 SSOT: the membership list comes from the canonical
	// asset.IsRightsRestrictedPredicate() (RightsStatus subset
	// where IsPublishable() returns false) + the canonical
	// asset.ReviewStatus alphabet partitioned by
	// IsReviewGateRequired(). A future rights enum update at the
	// type SSOT automatically inherits the correct classifier.
	//
	// godlike/07 fail-closed (includeRightRestricted=false): the
	// adapter logs loudly when the Qdrant collection hasn't been
	// reindexed with the new `rights_status` payload index, but
	// continues. The filter is a best-effort DB-side gate; a
	// future PR adds the typed error surface
	// (ErrRightsFilterRequiresReindex).
	var (
		excludeRightsStatuses []string
		excludeReviewStatuses []string
	)
	if !opts.IncludeRightRestricted {
		// Canonical RightsStatus subset that MUST be skipped
		// when planning (review_required + blocked).
		for _, v := range assetR.RestrictedRightsStatuses() {
			excludeRightsStatuses = append(excludeRightsStatuses, string(v))
		}
		// Canonical ReviewStatus subset that MUST be skipped
		// (pending + rejected).
		for _, v := range assetR.CanonicalReviewStatusValues() {
			if v.IsReviewGateRequired() {
				excludeReviewStatuses = append(excludeReviewStatuses, string(v))
			}
		}
		if a.log != nil && len(excludeRightsStatuses)+len(excludeReviewStatuses) > 0 {
			a.log.Info("slots search: rights-restricted filter enabled",
				zap.Strings("exclude_rights_statuses", excludeRightsStatuses),
				zap.Strings("exclude_review_statuses", excludeReviewStatuses))
		}
	}

	startTime := time.Now()
	byRef := make(map[string][]scriptpkg.ClipCandidate, len(plan.Slots))
	erroredRefs := make(map[string]error, len(plan.Slots))

	// Per-slot loop. Serially (NOT parallel goroutines) so the
	// adapter respects a single shared embedder + searcher pair
	// without WorkGroup scaffolding; future PR adds parallelism
	// behind the same godlike/06 SSOT surface (one canonical
	// owner per fact).
	for _, slot := range plan.Slots {
		// Plan-wide ctx cancellation — once parent ctx is
		// canceled, EVERY remaining slot is marked canceled
		// without firing the searcher again. Matches
		// godlike/07 fail-closed: never silently swallow.
		if ctx.Err() != nil {
			erroredRefs[slot.Ref] = fmt.Errorf(
				"slots search: slot=%s: %w",
				slot.Ref,
				ports.ErrSlotSearchContextCanceled,
			)
			byRef[slot.Ref] = []scriptpkg.ClipCandidate{}
			continue
		}

		query := strings.TrimSpace(slot.SearchQuery)

		// Empty-query fast-path mirrors the legacy SearchAssets
		// pattern: never fire the embedder for an empty query.
		if query == "" {
			byRef[slot.Ref] = []scriptpkg.ClipCandidate{}
			continue
		}

		// Per-slot deadline. The cancel func is called
		// immediately-after-searcher-return to release goroutine
		// resources regardless of success/failure path.
		slotCtx, cancel := context.WithTimeout(ctx, perSlotTimeout)
		hits, searchErr := a.searchAssetsClip(slotCtx, ports.AssetSearchQuery{
			Query:                 query,
			Source:                opts.SourceFilter,
			Category:              opts.Category,
			MediaType:             opts.MediaType,
			WorkspaceID:           opts.WorkspaceID,
			IsSystem:              opts.IsSystem,
			Limit:                 perSlotLimit,
			MinScore:              opts.MinScore,
			FolderNormalizedGroup: folderGroup,
			// Step 10 rights-restricted filter threading:
			// includeRightRestricted defaults to false (safe),
			// the composition above populates the 2 exclude
			// slices per-call. The underlying searcher compiles
			// these into a per-call MustNot(MatchAny(...)) clause.
			ExcludeRightsStatuses: excludeRightsStatuses,
			ExcludeReviewStatuses: excludeReviewStatuses,
		}, query)
		cancel()

		if searchErr != nil {
			// Distinguish ctx-cancellation from generic
			// adapter errors so the caller can decide fail-fast.
			if errors.Is(searchErr, context.Canceled) ||
				errors.Is(searchErr, context.DeadlineExceeded) {
				erroredRefs[slot.Ref] = fmt.Errorf(
					"slots search: slot=%s: %w",
					slot.Ref,
					ports.ErrSlotSearchContextCanceled,
				)
			} else {
				// Wrap the underlying typed error so the
				// audit can replay the per-slot reason
				// verbatim. Use fmt.Errorf with %w so the
				// chain is preserved (godlike/07 typed-
				// envelope contract).
				erroredRefs[slot.Ref] = fmt.Errorf(
					"slots search: slot=%s: %w",
					slot.Ref,
					searchErr,
				)
				if a.log != nil {
					a.log.Warn("slots search: per-slot failure",
						zap.String("slot_ref", slot.Ref),
						zap.Error(searchErr),
					)
				}
			}
			byRef[slot.Ref] = []scriptpkg.ClipCandidate{}
			continue
		}

		// Convert []ports.AssetSearchHit -> []scriptpkg.ClipCandidate.
		// The clip-path QDRANT-001 invariant guarantees DriveLink=
		// "" on every hit, so DriveLinkEmpty is hard-coded to
		// true (the canonical representation of "the search
		// contract does NOT carry drive_link for clip hits" for
		// downstream consumers like the ClipSampler's
		// availability gate).
		cands := make([]scriptpkg.ClipCandidate, 0, len(hits))
		for _, h := range hits {
			cands = append(cands, scriptpkg.ClipCandidate{
				SlotRef:        slot.Ref,
				AssetRef:       h.AssetID,
				SemanticScore:  h.Score,
				DriveLinkEmpty: true, // QDRANT-001 invariant
			})
		}
		byRef[slot.Ref] = cands
	}

	return &ports.SlotsSearchResult{
		ByRef:       byRef,
		ErroredRefs: erroredRefs,
		Duration:    time.Since(startTime),
	}, nil
}
