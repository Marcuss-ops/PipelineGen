// Package adapters_test — semantic_asset_search_test.go exercises the
// canonical typed adapter SemanticAssetSearch (PR-TRANSLATE-SCRIPT-SPEC
// §7, 2026-08-08). 8 hermetic TDD tests pin the 8 contract endpoints
// documented in the production file's goddoc:
//
//  1. EmptyQueryReturnsEmptyWithoutEmbed
//  2. NilSearcherFails
//  3. NilEmbedderFails
//  4. DefaultsLimitAndMinScore
//  5. SourceStockBuildsStockFilter
//  6. WorkspaceRequiredForUserTraffic
//  7. IsSystemAllowsEmptyWorkspace
//  8. ConvertsDriveURLFallback
//
// godlike/06 SSOT (one canonical owner per fact): every search.Query
// shape (Text / Limit / MinScore / Sources / Filters.Source / Actor) and
// every search.Candidate shape is the canonical property of the search
// package; this test asserts the adapter propagates these properties
// correctly without redefining the types.
package adapters_test

import (
	"context"
	"errors"
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// ── Fakes (canonical hermetic stubs) ──────────────────────────────────

// fakeSearcher is a hermetic stub implementing search.SearchBackend.
// Records the last Query received + canned Candidates to return.
type fakeSearcher struct {
	name        string
	caps        []search.Capability
	lastQuery   search.Query
	candidates  []search.Candidate
	err         error
	searchCalls int
}

func (f *fakeSearcher) Name() string                      { return f.name }
func (f *fakeSearcher) Capabilities() []search.Capability { return f.caps }
func (f *fakeSearcher) Universe() search.SearchUniverse   { return search.SearchCatalog }
func (f *fakeSearcher) Search(_ context.Context, q search.Query) ([]search.Candidate, error) {
	f.lastQuery = q
	f.searchCalls++
	return f.candidates, f.err
}

// Compile-time pin: fakeSearcher satisfies the canonical port.
var _ search.SearchBackend = (*fakeSearcher)(nil)

// fakeEmbedder is a hermetic stub implementing search.QueryEmbedder.
// Records the last text embedded + canned err to return.
type fakeEmbedder struct {
	lastText  string
	calls     int
	returnErr error
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	f.lastText = text
	f.calls++
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

// Compile-time pin: fakeEmbedder satisfies the canonical port.
var _ search.QueryEmbedder = (*fakeEmbedder)(nil)

// newTestAdapter wires the fakes into a fresh SemanticAssetSearch
// (with composition-time defaults from the production file).
func newTestAdapter() (*adapterspkg.SemanticAssetSearch, *fakeSearcher, *fakeEmbedder) {
	fs := &fakeSearcher{name: "test-backend", caps: []search.Capability{search.CapVideo}}
	fe := &fakeEmbedder{}
	a := adapterspkg.NewSemanticAssetSearch(fs, fe, nil)
	return a, fs, fe
}

// ── Contract 1: EmptyQueryReturnsEmptyWithoutEmbed ──────────────────

// TestSemanticAssetSearch_EmptyQueryReturnsEmptyWithoutEmbed locks
// contract 1: when the request's Query text is empty (or whitespace
// only), the adapter returns nil + no error AND never invokes the
// embedder. godlike/07 minimum-blast-radius: avoid wasted embedder
// calls for empty input.
func TestSemanticAssetSearch_EmptyQueryReturnsEmptyWithoutEmbed(t *testing.T) {
	a, _, fe := newTestAdapter()

	cases := []struct {
		name  string
		query string
	}{
		{"literal_empty", ""},
		{"whitespace_only", "   \t\n  "},
		{"trimmed_to_empty", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
				Query:    tc.query,
				Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true}, // bypass workspace gate for this test
				Limit:    10,
				MinScore: 0.5,
			})
			if err != nil {
				t.Fatalf("SearchAssets(%q) returned err = %v, want nil", tc.query, err)
			}
			if hits != nil {
				t.Errorf("SearchAssets(%q) returned %d hits, want nil", tc.query, len(hits))
			}
			if fe.calls != 0 {
				t.Errorf("Embed called %d times for empty query, want 0 (contract 1: no embed for empty input)", fe.calls)
			}
		})
	}
}

// ── Contract 2: NilSearcherFails ────────────────────────────────────

// TestSemanticAssetSearch_NilSearcherFails locks contract 2: a nil
// Searcher field returns the typed sentinel ErrSemanticSearchNilSearcher
// (godlike/07 fail-closed at the seam, NOT a panic, NOT a silent success).
func TestSemanticAssetSearch_NilSearcherFails(t *testing.T) {
	fe := &fakeEmbedder{}
	a := adapterspkg.NewSemanticAssetSearch(nil, fe, nil) // nil Searcher

	hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
		Query: "hello",
		Actor: search.Actor{WorkspaceID: "ws-1", IsSystem: true},
	})
	if !errors.Is(err, adapterspkg.ErrSemanticSearchNilSearcher) {
		t.Errorf("err = %v, want errors.Is(ErrSemanticSearchNilSearcher)", err)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil on nil-port fail-closed", hits)
	}
	if fe.calls != 0 {
		t.Errorf("Embed called %d times on nil-port fail-closed, want 0", fe.calls)
	}
}

// ── Contract 3: NilEmbedderFails ─────────────────────────────────────

// TestSemanticAssetSearch_NilEmbedderFails locks contract 3: a nil
// Embedder field returns the typed sentinel ErrSemanticSearchNilEmbedder.
func TestSemanticAssetSearch_NilEmbedderFails(t *testing.T) {
	fs := &fakeSearcher{name: "test-backend"}
	a := adapterspkg.NewSemanticAssetSearch(fs, nil, nil) // nil Embedder

	hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
		Query: "hello",
		Actor: search.Actor{WorkspaceID: "ws-1", IsSystem: true},
	})
	if !errors.Is(err, adapterspkg.ErrSemanticSearchNilEmbedder) {
		t.Errorf("err = %v, want errors.Is(ErrSemanticSearchNilEmbedder)", err)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil on nil-port fail-closed", hits)
	}
	if fs.searchCalls != 0 {
		t.Errorf("Search called %d times on nil-Embedder fail-closed, want 0", fs.searchCalls)
	}
}

// ── Contract 4: DefaultsLimitAndMinScore ─────────────────────────────

// TestSemanticAssetSearch_DefaultsLimitAndMinScore locks contract 4:
// when the request omits Limit (==0) or MinScore (<=0), the adapter
// substitutes its composition-time DefaultLimit + DefaultMinScore
// (20 + 0.50) before fanning out to the canonical search.Query.
func TestSemanticAssetSearch_DefaultsLimitAndMinScore(t *testing.T) {
	t.Run("zero_limit_uses_default", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query: "hello",
			Actor: search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			Limit: 0, // explicitly unset
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if fs.lastQuery.Limit != 20 {
			t.Errorf("fs.lastQuery.Limit = %d, want 20 (default)", fs.lastQuery.Limit)
		}
	})

	t.Run("negative_limit_uses_default", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query: "hello",
			Actor: search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			Limit: -5, // negative is treated as unset
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if fs.lastQuery.Limit != 20 {
			t.Errorf("fs.lastQuery.Limit = %d, want 20 (default for negative)", fs.lastQuery.Limit)
		}
	})

	t.Run("explicit_limit_is_respected", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query: "hello",
			Actor: search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			Limit: 7, // explicit override
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if fs.lastQuery.Limit != 7 {
			t.Errorf("fs.lastQuery.Limit = %d, want 7 (explicit override)", fs.lastQuery.Limit)
		}
	})

	t.Run("zero_minscore_uses_default", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "hello",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			MinScore: 0, // explicitly unset
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if fs.lastQuery.MinScore != 0.50 {
			t.Errorf("fs.lastQuery.MinScore = %v, want 0.50 (default)", fs.lastQuery.MinScore)
		}
	})

	t.Run("negative_minscore_uses_default", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "hello",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			MinScore: -0.1,
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if fs.lastQuery.MinScore != 0.50 {
			t.Errorf("fs.lastQuery.MinScore = %v, want 0.50 (default for negative)", fs.lastQuery.MinScore)
		}
	})

	t.Run("explicit_minscore_is_respected", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "hello",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			MinScore: 0.85,
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if fs.lastQuery.MinScore != 0.85 {
			t.Errorf("fs.lastQuery.MinScore = %v, want 0.85 (explicit override)", fs.lastQuery.MinScore)
		}
	})
}

// ── Contract 5: SourceStockBuildsStockFilter ─────────────────────────

// TestSemanticAssetSearch_SourceStockBuildsStockFilter locks contract 5:
// when Source="stock" (the user-spec-named canonical case), the adapter
// populates both q.Sources and q.Filters.Source on the canonical
// search.Query so the stock backend receives the correct filter
// (godlike/06 SSOT: stock is a first-class filter on the canonical
// orchestrator input, NOT a special case inside the backend).
//
// 5 sub-cases document the broader contract: the production code's
// `if req.Source != ""` guard is non-source-specific — ANY non-empty
// Source propagates identically. The "stock" sub-case is the
// user-spec-named primary assertion; the other 4 sub-cases pin the
// broader contract so a future regression that special-cases
// (e.g. `if Source == "stock"`) surfaces as a test failure.
func TestSemanticAssetSearch_SourceStockBuildsStockFilter(t *testing.T) {
	cases := []struct {
		name             string
		source           string
		wantSources      []string
		wantFilterSource string
	}{
		{"empty_source_no_propagation", "", nil, ""},
		{"stock_source_propagates", "stock", []string{"stock"}, "stock"},
		{"youtube_source_propagates", "youtube", []string{"youtube"}, "youtube"},
		{"voiceover_source_propagates", "voiceover", []string{"voiceover"}, "voiceover"},
		{"custom_source_propagates", "my-custom-source", []string{"my-custom-source"}, "my-custom-source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, fs, _ := newTestAdapter()

			_, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
				Query:  "search term",
				Source: tc.source,
				Actor:  search.Actor{WorkspaceID: "ws-1", IsSystem: true},
				Limit:  10,
			})
			if err != nil {
				t.Fatalf("SearchAssets returned err = %v, want nil", err)
			}

			// q.Sources MUST equal the expected slice (or nil for empty).
			if len(tc.wantSources) == 0 {
				if len(fs.lastQuery.Sources) != 0 {
					t.Errorf("fs.lastQuery.Sources = %v, want empty (empty Source must NOT propagate)", fs.lastQuery.Sources)
				}
			} else {
				if got := fs.lastQuery.Sources; len(got) != len(tc.wantSources) || got[0] != tc.wantSources[0] {
					t.Errorf("fs.lastQuery.Sources = %v, want %v", got, tc.wantSources)
				}
			}
			// q.Filters.Source MUST equal the expected value (or "" for empty).
			if got := fs.lastQuery.Filters.Source; got != tc.wantFilterSource {
				t.Errorf("fs.lastQuery.Filters.Source = %q, want %q", got, tc.wantFilterSource)
			}
		})
	}
}

// ── Contract 6: WorkspaceRequiredForUserTraffic ──────────────────────

// TestSemanticAssetSearch_WorkspaceRequiredForUserTraffic locks
// contract 6: when the request is user traffic (Actor.IsSystem=false
// AND Actor.WorkspaceID==""), the adapter returns the typed sentinel
// ErrSemanticSearchWorkspaceRequired. godlike/07 fail-closed at the
// user-traffic boundary — empty WorkspaceID silently degraded to admin
// would violate the canonical tenant-isolation invariant.
func TestSemanticAssetSearch_WorkspaceRequiredForUserTraffic(t *testing.T) {
	a, fs, fe := newTestAdapter()

	hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
		Query: "hello",
		Actor: search.Actor{
			WorkspaceID: "",    // empty workspace
			IsSystem:    false, // user traffic (not IsSystem)
		},
	})
	if !errors.Is(err, adapterspkg.ErrSemanticSearchWorkspaceRequired) {
		t.Errorf("err = %v, want errors.Is(ErrSemanticSearchWorkspaceRequired)", err)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil on workspace-required fail-closed", hits)
	}
	// Neither embedder nor searcher should be invoked.
	if fe.calls != 0 {
		t.Errorf("Embed called %d times on workspace-required fail-closed, want 0", fe.calls)
	}
	if fs.searchCalls != 0 {
		t.Errorf("Search called %d times on workspace-required fail-closed, want 0", fs.searchCalls)
	}
}

// ── Contract 7: IsSystemAllowsEmptyWorkspace ─────────────────────────

// TestSemanticAssetSearch_IsSystemAllowsEmptyWorkspace locks contract 7:
// when Actor.IsSystem=true, the workspace gate is BYPASSED. System
// traffic (reconcile / admin paths) may carry an empty WorkspaceID
// and the search proceeds normally.
func TestSemanticAssetSearch_IsSystemAllowsEmptyWorkspace(t *testing.T) {
	a, fs, fe := newTestAdapter()
	fs.candidates = []search.Candidate{
		{AssetID: "sys-asset-1", Score: 0.95, DriveLink: "https://drive.google.com/file/d/sys-asset-1/view", Source: "youtube", Title: "System Asset"},
	}

	hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
		Query: "reconcile scan",
		Actor: search.Actor{
			WorkspaceID: "", // empty
			IsSystem:    true,
		},
	})
	if err != nil {
		t.Fatalf("SearchAssets returned err = %v, want nil (IsSystem bypasses workspace gate)", err)
	}
	if len(hits) != 1 {
		t.Errorf("len(hits) = %d, want 1", len(hits))
	}
	if fe.calls != 1 {
		t.Errorf("Embed called %d times, want 1", fe.calls)
	}
	if fs.searchCalls != 1 {
		t.Errorf("Search called %d times, want 1", fs.searchCalls)
	}
}

// ── Contract 8: ConvertsDriveURLFallback ────────────────────────────

// TestSemanticAssetSearch_ConvertsDriveURLFallback locks contract 8:
// when the backend returns 0 hits AND the request carries a non-empty
// DriveURL AND urlutil.FileIDFromDriveLink extracts a file ID, the
// adapter appends a synthetic hit with AssetID=fileID and
// DriveLink=DriveURL. This is the canonical "fallback" semantics: the
// caller never sees a zero-result when they have supplied their own
// Drive URL anchor.
//
// sub-cases:
//   - HappyPath: backend returns 0 hits + valid Drive URL → synthetic
//     hit with AssetID extracted from URL
//   - NoFallbackWhenBackendHasHits: backend returns 1+ hits → no
//     synthetic hit appended (the canonical hits take precedence)
//   - NoFallbackOnEmptyDriveURL: backend returns 0 hits + empty
//     DriveURL → no synthetic hit (cannot fabricate from nothing)
//   - NoFallbackOnUnparseableDriveURL: backend returns 0 hits + URL
//     that does not match the DriveURL pattern → no synthetic hit
//     (cannot extract file ID; do not fabricate)
func TestSemanticAssetSearch_ConvertsDriveURLFallback(t *testing.T) {
	t.Run("happy_path_appends_synthetic_hit", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		// Backend returns 0 hits.
		fs.candidates = nil

		// A valid Drive file URL — the file ID is `abc123def456`.
		driveURL := "https://drive.google.com/file/d/abc123def456/view"

		hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "search term",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			DriveURL: driveURL,
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if len(hits) != 1 {
			t.Fatalf("len(hits) = %d, want 1 (synthetic fallback appended)", len(hits))
		}
		if hits[0].AssetID != "abc123def456" {
			t.Errorf("hits[0].AssetID = %q, want %q (extracted from DriveURL)", hits[0].AssetID, "abc123def456")
		}
		if hits[0].DriveLink != driveURL {
			t.Errorf("hits[0].DriveLink = %q, want %q (echoed DriveURL)", hits[0].DriveLink, driveURL)
		}
	})

	t.Run("no_fallback_when_backend_has_hits", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		// Backend returns 1 hit — fallback MUST NOT append a synthetic.
		fs.candidates = []search.Candidate{
			{AssetID: "real-asset", Score: 0.99, DriveLink: "https://drive.google.com/file/d/real-asset/view", Source: "youtube", Title: "Real"},
		}
		driveURL := "https://drive.google.com/file/d/abc123def456/view"

		hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "search term",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			DriveURL: driveURL,
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if len(hits) != 1 {
			t.Errorf("len(hits) = %d, want 1 (no synthetic when backend has hits)", len(hits))
		}
		if hits[0].AssetID != "real-asset" {
			t.Errorf("hits[0].AssetID = %q, want %q (canonical hit preserved)", hits[0].AssetID, "real-asset")
		}
	})

	t.Run("no_fallback_on_empty_drive_url", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		fs.candidates = nil

		hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "search term",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			DriveURL: "", // empty
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if len(hits) != 0 {
			t.Errorf("len(hits) = %d, want 0 (no synthetic on empty DriveURL — cannot fabricate from nothing)", len(hits))
		}
	})

	t.Run("no_fallback_on_unparseable_drive_url", func(t *testing.T) {
		a, fs, _ := newTestAdapter()
		fs.candidates = nil

		// Not a Drive URL at all — FileIDFromDriveLink returns an error.
		driveURL := "https://example.com/not-a-drive-url"

		hits, err := a.SearchAssets(context.Background(), adapterspkg.SemanticAssetSearchRequest{
			Query:    "search term",
			Actor:    search.Actor{WorkspaceID: "ws-1", IsSystem: true},
			DriveURL: driveURL,
		})
		if err != nil {
			t.Fatalf("SearchAssets returned err = %v, want nil", err)
		}
		if len(hits) != 0 {
			t.Errorf("len(hits) = %d, want 0 (no synthetic on unparseable DriveURL — cannot extract file ID)", len(hits))
		}
	})
}
