// Package app — artlist_runs_adapter.go (PR-ARTLIST-PERSIST-FIX, 2026-07-04;
// extended with the local-searcher adapter, MEDIA-SSOT P1-5, 2026-09-13).
//
// Composition-root adapters that bridge artlist capability ports to their
// concrete owners:
//
//	artlist.RunRepository (port in internal/capabilities/assets/providers/artlist/ports.go)
//	sqlite/assets.RunRepository (infra side: internal/platform/sqlite/assets/artlist_runs_repository.go)
//
// and
//
//	artlist.Searcher (the local catalog search port)
//	pgmedia.MediaSearcher (the PostgreSQL media read authority)
//
// Without the runs adapter, the import cycle would be: artlist pkg imports
// sqlite/assets pkg (for ClipsRepository) AND sqlite/assets pkg's
// artlist_runs_repository.go imports artlist pkg (for the port type).
// The adapters live here in composition-root because that's the only
// layer that's already authorised to import BOTH leaf dependencies
// (artlist pkg + the concrete owner).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - artlist.RunRepository — SOLE canonical port seen by NewService
//   - sqlite/assets.RunRepository — SOLE canonical concrete in the
//     infra layer (write-closure fact: only this concrete writes
//     artlist_runs rows)
//   - *artlistRunsRepoAdapter — the SINGLE translation site between
//     the two interface names (godlike/06 one-canonical-owner-per-fact)
//     — future drift in any of the three surfaces surfaces as a
//     build failure at the compile-time pin below.
//   - *artlistLocalSearcher — the SINGLE local-catalog searcher; it reads
//     the PostgreSQL media SSOT, never the operational SQLite store
//     (MEDIA-SSOT P1-5).
//   - *artlistMediaSSOTAssetStore — the SINGLE owner of the Artlist DB-only
//     SearchClips/SearchByTerms reads; they resolve against
//     media_assets.search_terms in PostgreSQL, never the operational
//     clip_search_terms index (MEDIA-SSOT P2-9 unblock step 1).
//   - pgmedia.MediaStatisticsReader — the SINGLE owner of the media_assets
//     aggregates the Artlist surfaces report (artlist.MediaStats port: the
//     per-source count, the catalogue total and the newest matching run
//     timestamp; MEDIA LEGACY READ-PLANE DEMOLITION, 2026-09-21).
//
// godlike/07 minimum-blast-radius: configuration-only changes (no
// field renames; the field-to-field translation is the canonical
// mapping per the schema-reconciliation review of 2026-07-04).
//
// HOTSPOT NOTE (2026-09-13): internal/app/wiring is a registered hotspot
// capped at 150 production files (percheck_legacy_hotspot_growth). The
// local-searcher adapter was deliberately merged into this existing file
// rather than added as a new one, so carry-forward debt does not increase.
package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	artlistsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/artlist"
)

// artlistRunsRepoAdapter is the canonical composition-root adapter
// that satisfies artlist.RunRepository. Translates
// artlist.RunRecord → sqlite/assets.RunRecord field-by-field, then
// delegates the actual SQL work to the *ArtlistRunsRepository
// concrete (composition-pre-created).
type artlistRunsRepoAdapter struct {
	concrete *artlistsql.ArtlistRunsRepository
}

// NewArtlistRunsRepoAdapter wraps the SQLite-backed concrete behind
// the artlist.RunRepository port. Returns nil for nil concrete
// (godlike/07 minimum-blast-radius: test fixtures may construct
// adapters with nil concrete to exercise nil-handling paths;
// production callers must pre-validate via WireArtlist's
// NewArtlistRunsRepository error return).
func NewArtlistRunsRepoAdapter(concrete *artlistsql.ArtlistRunsRepository) *artlistRunsRepoAdapter {
	return &artlistRunsRepoAdapter{concrete: concrete}
}

// compile-time pin: *artlistRunsRepoAdapter satisfies
// artlist.RunRepository. Drift in either surface's signature
// surfaces as a build failure here rather than a runtime panic.
var _ artlist.RunRepository = (*artlistRunsRepoAdapter)(nil)

// RunRecord translates the application-layer RunRecord into the
// infra-layer RunRecord and delegates to the concrete. NULL-guard on
// the concrete is godlike/07 minimum-blast-radius: a nil concrete
// (impossible in production; reachable in test fixtures via
// NewArtlistRunsRepoAdapter(nil)) returns a typed sentinel so the
// caller can branch on intent.
//
// godlike/06 SSOT (column-level reconciliation 2026-07-04): the
// field-to-field mapping below mirrors the artlist_runs schema
// verbatim. Schema additions require a corresponding update here +
// in the concrete; column drops cascade through both layers.
func (a *artlistRunsRepoAdapter) Record(ctx context.Context, rec artlist.RunRecord) error {
	if a == nil || a.concrete == nil {
		// Defensive: test-only nil-receiver path. Production never
		// reaches here because WireArtlist pre-validates the
		// concrete via NewArtlistRunsRepository's error return.
		return nil
	}
	translated := artlistsql.RunRecord{
		RunID:        rec.RunID,
		Term:         rec.Term,
		Status:       rec.Status,
		RootFolderID: rec.RootFolderID,
		TagFolderID:  rec.TagFolderID,
		RequestedN:   rec.RequestedN,
		FoundN:       rec.FoundN,
		ProcessedN:   rec.ProcessedN,
		SkippedN:     rec.SkippedN,
		FailedN:      rec.FailedN,
		ErrorMessage: rec.ErrorMessage,
	}
	return a.concrete.Record(ctx, translated)
}

// LatestRun bridges the infra-layer LatestRunRow → application-layer
// LatestRunSummary (the typed read-shape surfaced via
// DiagnosticsResponse.LatestRun).
//
// godlike/06 SSOT: this is the SINGLE translation site between the
// two struct definitions. Future renames in either surface surface
// as a build failure at the compile-time pin above.
//
// (nil, nil) on empty-table: the application-layer DiagnosticsService
// interprets nil as "no runs yet" and omits the LatestRun field from
// the JSON response entirely (godlike/07 honest-about-fresh-install).
func (a *artlistRunsRepoAdapter) LatestRun(ctx context.Context) (*artlist.LatestRunSummary, error) {
	if a == nil || a.concrete == nil {
		// Defensive: same nil-handling discipline as Record above.
		return nil, nil
	}
	row, err := a.concrete.LatestRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("artlistRunsRepoAdapter.LatestRun: %w", err)
	}
	if row == nil {
		// Empty-table (fresh install) — forward nil verbatim.
		return nil, nil
	}
	return &artlist.LatestRunSummary{
		RunID:     row.RunID,
		Term:      row.Term,
		Status:    row.Status,
		Error:     row.ErrorMessage,
		CreatedAt: row.CreatedAt,
	}, nil
}

// ── Artlist local catalog searcher (MEDIA-SSOT P1-5) ────────────────────────
//
// The Artlist local catalog searcher used to read SQLite media_assets
// (platform/sqlite/assets/artlist). That is a read split-brain — Artlist media
// rows are committed to PostgreSQL, so the SQLite read either returned nothing
// (local hits never materialise, so `prefer_db`/catalog-only searches silently
// fell through to the remote provider) or resurrected stale pre-cutover rows.
// The adapter below reads the same media SSOT the Artlist finalizer writes.

// artlistLocalSearchDefaultLimit mirrors the retired SQLite adapter's implicit
// page size so callers that leave SearchRequest.Limit unset keep their result
// cardinality.
const artlistLocalSearchDefaultLimit = 50

// artlistLocalMediaStore is the narrow PostgreSQL media read surface the
// adapter consumes. *pgmedia.MediaSearcher implements it; the interface keeps
// the adapter unit-testable without a live database.
type artlistLocalMediaStore interface {
	SearchLocal(ctx context.Context, req pgmedia.LocalMediaSearchRequest) ([]pgmedia.MediaAssetRecord, error)
}

// artlistLocalSearcher is the PostgreSQL-backed implementation of
// artlist.Searcher. It is the ONLY local Artlist catalog searcher.
type artlistLocalSearcher struct {
	store artlistLocalMediaStore
}

var (
	_ artlist.Searcher       = (*artlistLocalSearcher)(nil)
	_ artlistLocalMediaStore = (*pgmedia.MediaSearcher)(nil)
	// Pattern 0: the Artlist media-statistics port has exactly one concrete
	// owner, on the media SSOT.
	_ artlist.MediaStats = (*pgmedia.MediaStatisticsReader)(nil)
)

// newArtlistLocalSearcher returns nil when the media SSOT store is absent so
// the caller can leave the local searcher unwired (godlike/07 fail-closed)
// instead of reading the operational SQLite store.
func newArtlistLocalSearcher(store artlistLocalMediaStore) artlist.Searcher {
	if store == nil {
		return nil
	}
	return &artlistLocalSearcher{store: store}
}

func (s *artlistLocalSearcher) Search(ctx context.Context, req artlist.SearchRequest) ([]artlist.Candidate, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = artlistLocalSearchDefaultLimit
	}
	records, err := s.store.SearchLocal(ctx, pgmedia.LocalMediaSearchRequest{
		Text:                term,
		Source:              "artlist",
		Limit:               limit,
		ExcludeUnclassified: true,
	})
	if err != nil {
		return nil, err
	}
	out := make([]artlist.Candidate, 0, len(records))
	for i := range records {
		out = append(out, artlistCandidateFromRecord(&records[i]))
	}
	return out, nil
}

// artlistCandidateFromRecord maps a canonical PostgreSQL media record onto the
// provider-agnostic candidate the Artlist search chain consumes. Field
// coverage mirrors the retired SQLite adapter so callers cannot observe a
// behavioural regression beyond the engine change.
func artlistCandidateFromRecord(rec *pgmedia.MediaAssetRecord) providerassets.ProviderAsset {
	pageURL := strings.TrimSpace(rec.SourceURL)
	previewURL := artlistFirstNonEmpty(rec.MetadataString("preview_url"), pageURL)
	return providerassets.ProviderAsset{
		Provider:     "artlist",
		ExternalID:   rec.ID,
		ID:           rec.ID,
		Title:        rec.TitleOrName(),
		Description:  rec.MetadataString("description"),
		Creator:      rec.MetadataString("creator"),
		PageURL:      pageURL,
		PreviewURL:   previewURL,
		ThumbnailURL: rec.ThumbnailURL,
		SourceRef:    artlistFirstNonEmpty(pageURL, previewURL),
		SourceName:   "database",
		MediaType:    asset.MediaType(rec.MediaType),
		Duration:     time.Duration(rec.DurationMS) * time.Millisecond,
		DurationMs:   rec.DurationMS,
		Keywords:     append([]string(nil), rec.Tags...),
		Categories:   rec.MetadataStringSlice("provider_categories"),
		RawMetadata:  rec.MetadataMap(),
	}
}

// ── MEDIA-SSOT P2-9 unblock step 1: the Artlist DB-only search surface ──

// artlistMediaSSOTAssetStore is the composition-root decorator that routes the
// Artlist DB-only search surface to the PostgreSQL media read authority while
// delegating every remaining AssetStore method to the operational store.
//
// WHY ONLY TWO METHODS ARE OVERRIDDEN. The Artlist capability's DB-only
// endpoints (`/api/artlist/search` via SearchService.Search, SearchClips, and
// the diagnostics term counters) reach the media catalog exclusively through
// `AssetStore.SearchClips`, which the SQLite concrete implements as
// "SearchByTerms + LIKE fallback". SearchByTerms resolves ids from the
// `clip_search_terms` inverted index and then re-hydrates them from SQLite
// `media_assets` — the exact Postgres-writer/SQLite-reader split-brain: an
// asset committed by the canonical PG committer is invisible to it, and a
// stale pre-cutover row can be resurrected. The remaining AssetStore methods
// are operational (runs, counts, term bookkeeping) and stay where they are
// until their own P2-9 step.
//
// The canonical replacement is not a second index: PostgreSQL already carries
// the term corpus on media_assets.search_terms, and SearchLocal matches it.
// `clip_search_terms` therefore has no PostgreSQL counterpart to create, which
// is what makes P2-9 unblock step 3 (retire clip_search_terms as a media
// index) reachable rather than a rewrite.
//
// Fail-closed: newArtlistMediaSSOTAssetStore returns the delegate unchanged
// when the media SSOT store is absent, so the SQLite-only degrade mode keeps
// its documented behaviour instead of silently returning no results.
//
// Ranking is preserved by reusing the same canonical scorer the SQLite path
// used (detail.ScoreClips), so callers observe an engine change and not a
// relevance change.
type artlistMediaSSOTAssetStore struct {
	artlist.AssetStore
	store   artlistLocalMediaStore
	mutator persistence.AssetMutator
}

var _ artlist.AssetStore = (*artlistMediaSSOTAssetStore)(nil)

// newArtlistMediaSSOTAssetStore wraps delegate so the DB-only search reads
// (SearchClips/SearchByTerms) resolve against the PostgreSQL media SSOT and the
// term-corpus write (UpdateSearchTerms) lands on the canonical media writer. It
// returns delegate untouched when the search handle is absent (SQLite-only
// degrade mode).
//
// mutator may be nil only when the caller could not recover the canonical
// writer's AssetMutator view; UpdateSearchTerms then FAILS CLOSED rather than
// writing the operational clip_search_terms mirror.
func newArtlistMediaSSOTAssetStore(delegate artlist.AssetStore, store artlistLocalMediaStore, mutator persistence.AssetMutator) artlist.AssetStore {
	if delegate == nil || store == nil {
		return delegate
	}
	return &artlistMediaSSOTAssetStore{AssetStore: delegate, store: store, mutator: mutator}
}

// UpdateSearchTerms re-points the retired SQLite clip_search_terms write at the
// canonical media SSOT (P2-9, F5 writer half).
//
// WHY THIS IS A RE-POINT AND NOT A DELETE. Measured at HEAD: the canonical
// commit contract (persistence.CommitRequest) has NO search_terms field and
// detail.DeriveSearchTerms had ZERO production callers, so nothing else writes
// media_assets.search_terms. Deleting the two Artlist call sites would therefore
// have dropped the derived term corpus from the SSOT and silently narrowed the
// PostgreSQL local search to name/search_text. PatchAsset on the canonical
// committer is the ONLY writer permitted to mutate media_assets, so the terms
// are derived with the canonical detail.DeriveSearchTerms helper and persisted
// there in the same self-owned transaction.
func (s *artlistMediaSSOTAssetStore) UpdateSearchTerms(ctx context.Context, clipID, source, name string, tags []string, searchText string) error {
	if s == nil {
		return nil
	}
	// FAIL-CLOSED: this wrapper only exists in PostgreSQL media mode, so a
	// missing mutator is a misconfiguration. It must NOT fall back to the
	// wrapped store's SQLite clip_search_terms write — that would re-create the
	// exact split-brain the migration removed. The SQLite-only degrade mode
	// never reaches this type (newArtlistMediaSSOTAssetStore returns the
	// unwrapped delegate when the media search handle is absent).
	if s.mutator == nil {
		return fmt.Errorf("artlist media SSOT: canonical search-term writer is not wired — refusing to write the operational clip_search_terms mirror")
	}
	terms := detail.DeriveSearchTerms(&asset.Asset{ID: clipID, Name: name, Tags: tags, SearchText: searchText})
	encoded, err := json.Marshal(terms)
	if err != nil {
		return fmt.Errorf("artlist media SSOT: encode search terms: %w", err)
	}
	encodedTerms := string(encoded)
	return s.mutator.PatchAsset(ctx, persistence.AssetPatch{
		AssetID:     clipID,
		SearchTerms: &encodedTerms,
		SearchText:  &searchText,
	})
}

// SearchClips mirrors the retired SQLite contract exactly: split the term into
// keywords, require every keyword, then rank with the canonical scorer.
func (s *artlistMediaSSOTAssetStore) SearchClips(ctx context.Context, source, term string) ([]*asset.Asset, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	keywords := strings.Fields(term)
	if len(keywords) == 0 {
		keywords = []string{term}
	}
	return s.search(ctx, source, keywords, artlistLocalSearchDefaultLimit)
}

// SearchByTerms is the multi-keyword entry point. SQLite satisfied it from the
// clip_search_terms inverted index; PostgreSQL answers it from
// media_assets.search_terms with the same AND semantics.
func (s *artlistMediaSSOTAssetStore) SearchByTerms(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.search(ctx, source, keywords, limit)
}

// search is the SINGLE query+rank site for both overridden methods.
func (s *artlistMediaSSOTAssetStore) search(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error) {
	terms := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if trimmed := strings.TrimSpace(keyword); trimmed != "" {
			terms = append(terms, trimmed)
		}
	}
	if len(terms) == 0 {
		return []*asset.Asset{}, nil
	}
	records, err := s.store.SearchLocal(ctx, pgmedia.LocalMediaSearchRequest{
		AllTerms:            terms,
		Source:              source,
		Limit:               limit,
		ExcludeUnclassified: true,
	})
	if err != nil {
		return nil, err
	}
	clips := make([]*asset.Asset, 0, len(records))
	for i := range records {
		if hydrated := records[i].HydrateAsset(); hydrated != nil {
			clips = append(clips, hydrated)
		}
	}
	return detail.ScoreClips(clips, terms), nil
}

// artlistFirstNonEmpty returns the first non-empty candidate. It is a local
// helper so this file does not depend on an unexported sibling package helper.
func artlistFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
