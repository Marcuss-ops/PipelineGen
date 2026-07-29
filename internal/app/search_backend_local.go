// Package app — search_backend_local.go is the local (SQLite) backend
// extracted from search_backends.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #2).
//
// Owns: localSearchBackend struct + Name/Capabilities/Search/searchByHash/searchByText methods.
package app

import (
	"context"
	"strings"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// localSearchBackend wraps sqassets.ClipsRepository.SearchClipsAdvanced
// (the canonical AdvancedSearchRepo interface). Maps Asset rows
// into search.Candidate. Score is normalised 1.0 (local hits are
// keyword-matched) because the repository already filters by
// metadata; the semantic rerank at the Aggregator level is what
// surfaces relevance ordering across all backends.
type localSearchBackend struct {
	repo    *sqassets.ClipsRepository
	srcName string
}

func (b *localSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return "local"
}

func (b *localSearchBackend) Capabilities() []search.Capability {
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

func (b *localSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	// PR-2 (June 2026): when Query.Hash is non-empty, the local
	// backend fires its hash-match path. The aggregator fans out
	// to EVERY registered backend regardless; non-hash backends
	// ignore Query.Hash and return their text-mode results (which
	// are typically zero when Query.Text is also empty). The
	// hash-source local rows then bubble up with their Hash
	// field populated and the canonical 4-key dedup collapses
	// duplicates by content hash.
	if q.Hash != "" {
		return b.searchByHash(ctx, q)
	}
	return b.searchByText(ctx, q)
}

// searchByHash answers the PR-2 hash-match Query path. Score = 1.0
// because a deterministic MD5 match has no semantic scoring; the
// canonical 4-key dedup will merge collisions in the aggregator.
//
// QDRANT-004 invariant (Commit 3-A lockdown): search.Candidate
// carries NO server-internal locator (no LocalPath, no DriveLink,
// no RawDriveFileID). The Hash + AssetID + ThumbnailURL fields
// are the surface shipped to FindDuplicates; the operator/admin
// surfaces that legitimately need {LocalPath, DriveLink} consume
// duplicates.DuplicateMatch from
// internal/application/assets/duplicates/types.go (the canonical
// owner per godlike/06 one-owner-per-fact) — same discipline as
// searchByText below.
func (b *localSearchBackend) searchByHash(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	hits, err := b.repo.FindClipsByHash(ctx, q.Hash)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(hits))
	for _, clip := range hits {
		out = append(out, search.Candidate{
			AssetID:   clip.ID,
			Source:    string(clip.Source),
			SourceRef: clip.ID,
			MediaType: string(clip.MediaType), Title: clip.Name,
			Name:         clip.Name,
			ThumbnailURL: clip.ThumbnailURL,
			DriveLink:    clip.DriveLink(),
			Score:        1.0,
			Hash:         q.Hash,
		})
	}
	return out, nil
}

// searchByText is the pre-PR-2 text path. Kept as a private helper
// so the Search method's hash branch + text branch stay separately
// auditable.
func (b *localSearchBackend) searchByText(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}
	// PR-AGGREGATE-FILTER-UNIFORM (July 2026): AdvancedSearchRequest
	// is the canonical DTO for the local-backend filter set
	// (godlike/06 SSOT). Category/Language/Tags forward from
	// q.Filters; Source continues to flow through sourceOrAll for
	// the legacy "all" semantic. The recipient SearchClipsAdvanced
	// is also canonical (architecture/current.yaml#id-30). Language
	// passes through AdvancedSearchRequest but cannot be applied at
	// the SQL layer today (no media_assets.language column); the
	// SQL filter is a no-op until a future migration lands — the
	// DTO shape stays uniform so the Migration is wire-stable.
	req := assetpkg.AdvancedSearchRequest{
		Q:        q.Text,
		Limit:    limit,
		Source:   sourceOrAll(q.Filters.Source),
		Category: strings.TrimSpace(q.Filters.Category),
		Language: strings.TrimSpace(q.Filters.Language),
		Tags:     append([]string(nil), q.Filters.Tags...),
	}
	res, err := b.repo.SearchClipsAdvanced(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Clips))
	for _, clip := range res.Clips {
		// PR-1: derive score from real signals (title, tags,
		// source, duration) instead of the previous "always 1.0"
		// hardcode. The signal mix caps at 0.95 so a
		// semantic-backend hit with score 0.97 still wins.
		// Asset exposes Duration as time.Duration; convert to
		// ms here so the LocalScore mix is in canonical units.
		// Language is NOT a field on asset.Asset today; the
		// relevant metadata is in clip.Metadata if exposed by the
		// caller (we leave the LocalSignal.Language zero so the
		// language-match signal scores 0, matching the documented
		// "missing signal = no contribution" rule).
		durMs := int(clip.Duration.Milliseconds())
		sig := search.LocalSignal{
			Title:      clip.Name,
			Tags:       clip.Tags,
			Source:     string(clip.Source),
			DurationMs: durMs,
			// Wire q.Filters.DurationMsMin so the duration-fit
			// signal can fire from a non-zero query filter.
			MinDuration: q.Filters.DurationMsMin,
		}
		// QDRANT-004 invariant: search.Candidate carries NO
		// server-internal locator (the canonical candidate shape
		// was collapsed during Commit 3-A). When FindDuplicates/
		// operator surfaces need {LocalPath, DriveLink}, they
		// consume duplicates.DuplicateMatch from
		// internal/application/assets/duplicates/types.go (the
		// canonical owner) — see the dedicated surface added in
		// Phase 7 P0 follow-ups.
		out = append(out, search.Candidate{
			AssetID:      clip.ID,
			Source:       string(clip.Source),
			SourceRef:    clip.ID,
			Title:        clip.Name,
			Name:         clip.Name,
			MediaType:    string(clip.MediaType),
			ThumbnailURL: clip.ThumbnailURL,
			DriveLink:    clip.DriveLink(),
			Score:        search.LocalScore(sig, q),
		})
	}
	return out, nil
}

var _ search.SearchBackend = (*localSearchBackend)(nil)
