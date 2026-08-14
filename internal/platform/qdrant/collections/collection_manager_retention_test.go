// PR 9 (June 2026, feat/qdrant-operational-readiness) regression test
// for the retention sort + loop fix in CleanupWithConfig.
//
// Pre-PR9 had TWO compounding bugs in the eligibility-sweep loop:
//  1. `sort.Strings(eligible)` sorts ASCENDING. The reindex produces
//     names with monotonically-increasing suffixes (e.g.
//     media_assets_v3__ts_20260101_xxx < media_assets_v3__ts_20260228_xxx),
//     so the ascending sort placed OLDEST at the head — keep_last_n
//     would keep the WRONG (oldest) collections, dropping the newest.
//  2. The loop terminated on the first `keepLeft <= 0` iteration
//     (via a `break` inside the if-else boundary), which meant at most
//     ONE collection was protected by the floor even when keepLastN > 2.
//
// This test stands up an in-memory fakeQdrantServer (via httptest) that
// records every DeleteCollection call and serves a fixed
// /collections list with a deterministic active-alias target. We then
// assert: the DESCENDING sort picks the RIGHT collections to keep and
// drops the CORRECT remainder — exactly matching the post-fix behaviour.
package collections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"

	"go.uber.org/zap"
)

// ── Fake Qdrant server ──────────────────────────────────────────────

// fakeQdrantServer is the httptest-driven Qdrant mock for retention
// tests. It serves the minimum surface CleanupWithConfig touches:
//   - GET /collections
//   - GET /collections/{alias}/aliases
//   - DELETE /collections/{name}
//
// Recorded: every Delete call (so the test can assert which collections
// were dropped) + every ActiveAlias query (so sanity checks can confirm
// the active target was NEVER deleted).
type fakeQdrantServer struct {
	ts           *httptest.Server
	colls        []string
	aliasTarget  string
	deletedColls []string
	aliasQueries []string
}

func newFakeQdrantServer(colls []string, aliasTarget string) *fakeQdrantServer {
	f := &fakeQdrantServer{
		colls:       colls,
		aliasTarget: aliasTarget,
	}
	mux := http.NewServeMux()

	// GET /collections → {"result": {"collections": [{"name": "<n>"}, ...]}}
	mux.HandleFunc("/collections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		out := struct {
			Result struct {
				Collections []struct {
					Name string `json:"name"`
				} `json:"collections"`
			} `json:"result"`
		}{}
		for _, c := range f.colls {
			out.Result.Collections = append(out.Result.Collections, struct {
				Name string `json:"name"`
			}{Name: c})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	// GET /aliases → canonical envelope (PR-ALIAS-RESOLVE-FIX 2026-07-04).
	// Production code in transport/client_aliases.go::GetAliasTarget now
	// calls the GLOBAL /aliases endpoint, not the per-collection
	// /collections/{alias}/aliases. Without this handler, the mock
	// returns 404 and the active collection leaks into the eligible
	// list, shifting the keep-2 floor by one (3 dropped instead of 2,
	// 2 dropped instead of 1).
	mux.HandleFunc("/aliases", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f.aliasQueries = append(f.aliasQueries, "media_assets_current")
		out := struct {
			Result struct {
				Aliases []struct {
					AliasName      string `json:"alias_name"`
					CollectionName string `json:"collection_name"`
				} `json:"aliases"`
			} `json:"result"`
		}{}
		out.Result.Aliases = append(out.Result.Aliases, struct {
			AliasName      string `json:"alias_name"`
			CollectionName string `json:"collection_name"`
		}{AliasName: "media_assets_current", CollectionName: f.aliasTarget})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	// GET /collections/{alias}/aliases OR DELETE /collections/{name}.
	//
	// PR 9 review fix (IMPORTANT): the previous handler dispatched on
	// path-suffix FIRST then method. Asking for "does the URL end in
	// /aliases?" before even looking at the HTTP method caused the
	// DELETE branch (where the URL is /collections/<name> with NO
	// /aliases suffix) to short-circuit to NotFound. Reordered to
	// method-first so the DELETE branch is reachable.
	mux.HandleFunc("/collections/", func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/collections/"
		const suffix = "/aliases"
		path := r.URL.Path
		if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
			http.NotFound(w, r)
			return
		}
		remainder := path[len(prefix):]

		switch r.Method {
		case http.MethodGet:
			if !strings.HasSuffix(remainder, suffix) {
				http.NotFound(w, r)
				return
			}
			alias := remainder[:len(remainder)-len(suffix)]
			if alias != "media_assets_current" {
				http.NotFound(w, r)
				return
			}
			f.aliasQueries = append(f.aliasQueries, alias)
			out := struct {
				Result struct {
					Aliases []struct {
						AliasName      string `json:"alias_name"`
						CollectionName string `json:"collection_name"`
					} `json:"aliases"`
				} `json:"result"`
			}{}
			out.Result.Aliases = append(out.Result.Aliases, struct {
				AliasName      string `json:"alias_name"`
				CollectionName string `json:"collection_name"`
			}{AliasName: alias, CollectionName: f.aliasTarget})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodDelete:
			if strings.HasSuffix(remainder, suffix) {
				http.NotFound(w, r)
				return
			}
			f.deletedColls = append(f.deletedColls, remainder)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"result":true,"status":"acknowledged"}`)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	f.ts = httptest.NewServer(mux)
	return f
}

func (f *fakeQdrantServer) Close() {
	if f.ts != nil {
		f.ts.Close()
	}
}

func (f *fakeQdrantServer) URL() string {
	if f.ts == nil {
		return ""
	}
	return f.ts.URL
}

// ── The actual test ─────────────────────────────────────────────────

// TestCleanupWithConfig_DescendingSort_LastNKeptIsCorrect exercises the
// sort + loop fix on a representative 5-collection fleet. The schema
// prefix "media_assets_v3" matches the fixed qdrantSchema.DefaultV3Schema() output.
// Expected post-fix state:
//   - Active alias target: media_assets_v3 (kept verbatim).
//   - Eligible list (other 4): sorted DESCENDING → [c4, c3, c2, c1].
//   - keepLastN=3 → keepLeft = 2 → protect c4 + c3.
//   - Drop sweep: c2 + c1 deleted.
//   - Active NEVER appears in deletedColls.
//   - Drop set == {c1, c2} exactly (order irrelevant; sorted compare).
func TestCleanupWithConfig_DescendingSort_LastNKeptIsCorrect(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	activeName := prefix + "__ts_20260601_active"
	colls := []string{
		activeName,                          // active alias target (has prefix)
		prefix + "__ts_20260101_120000_aaa", // oldest
		prefix + "__ts_20260301_120000_ccc",
		prefix + "__ts_20260201_120000_bbb",
		prefix + "__ts_20260401_120000_ddd", // newest
	}
	f := newFakeQdrantServer(colls, activeName)
	defer f.Close()

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: f.URL(), Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	res, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{
		RetentionDays: 1, // binary switch NOT used; sort + duration gate governs
		KeepLastN:     3,
	})
	if err != nil {
		t.Fatalf("CleanupWithConfig returned error: %v", err)
	}

	// Sort deleted for stable comparison.
	gotDropped := append([]string(nil), res.DroppedNames...)
	wantDropped := []string{
		prefix + "__ts_20260101_120000_aaa",
		prefix + "__ts_20260201_120000_bbb",
	}
	sort.Strings(gotDropped)
	sort.Strings(wantDropped)
	if !reflect.DeepEqual(gotDropped, wantDropped) {
		t.Fatalf("DroppedNames mismatch:\n got  %v\n want %v", gotDropped, wantDropped)
	}

	// The newest 2 (ddd + ccc) must be in the PROTECTED set.
	wantProtected := []string{
		prefix + "__ts_20260401_120000_ddd",
		prefix + "__ts_20260301_120000_ccc",
	}
	sort.Strings(res.ProtectedKept)
	sort.Strings(wantProtected)
	if !reflect.DeepEqual(res.ProtectedKept, wantProtected) {
		t.Fatalf("ProtectedKept mismatch:\n got  %v\n want %v (the keep_last_n tail MUST be the newest, not the oldest, post-fix)",
			res.ProtectedKept, wantProtected)
	}

	// The active alias target must NEVER appear in deletedColl.
	gotDeleted := append([]string(nil), f.deletedColls...)
	for _, d := range gotDeleted {
		if d == "media_assets_v3" {
			t.Fatalf("ACTIVE alias target %q must never be deleted; deletedColls=%v", d, gotDeleted)
		}
	}

	if res.CollectionsDropped != len(wantDropped) {
		t.Fatalf("CollectionsDropped = %d, want %d (res=%+v)", res.CollectionsDropped, len(wantDropped), res)
	}
	// Active + 2 protected = 3 kept.
	if res.CollectionsKept != 3 {
		t.Fatalf("CollectionsKept = %d, want 3 (active + 2 protected tail)", res.CollectionsKept)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("expected zero errors on a clean sweep, got %v", res.Errors)
	}
}

// TestCleanupWithConfig_KeepLastN2_KeepsOneNewestColl is the focused
// regression test for the second bug: the `break` inside the inner branch
// terminated the loop after the FIRST keep iteration, so even with
// keepLastN=5 the loop would only protect ONE additional collection (on
// top of the active one) — meaning at most TWO collections survived.
//
// Post-fix the loop instead walks the entire keepLeft tail. With
// keepLastN=2 and 3 eligible collections, the floor lifts the effective
// keep_last_n to 2 (active + 1 protected), so eligible tail length = 2
// (the oldest 1 dropped).
func TestCleanupWithConfig_KeepLastN2_KeepsOneNewestColl(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	activeName := prefix + "__ts_20260601_active"
	colls := []string{
		activeName,                   // active alias target (has prefix)
		prefix + "__ts_20260101_aaa", // oldest eligible
		prefix + "__ts_20260201_bbb", // middle
	}
	f := newFakeQdrantServer(colls, activeName)
	defer f.Close()

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: f.URL(), Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	res, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{
		RetentionDays: 1,
		KeepLastN:     2,
	})
	if err != nil {
		t.Fatalf("CleanupWithConfig: %v", err)
	}

	// Active + 1 protected newest = 2 kept; 1 dropped (oldest eligible).
	if res.CollectionsDropped != 1 {
		t.Fatalf("CollectionsDropped = %d, want 1 (oldest eligible); res=%+v", res.CollectionsDropped, res)
	}
	if res.CollectionsKept != 2 {
		t.Fatalf("CollectionsKept = %d, want 2 (active + 1 protected)", res.CollectionsKept)
	}
	// The dropped MUST be the oldest eligible (a__ts_20260101) — the
	// keep_last_n sits at the descending tail, _not_ the ascending head.
	wantDropped := prefix + "__ts_20260101_aaa"
	if len(res.DroppedNames) != 1 || res.DroppedNames[0] != wantDropped {
		t.Fatalf("DroppedNames = %v, want [%q] (post-fix: oldest eligible is dropped; pre-fix the newest would be dropped)", res.DroppedNames, wantDropped)
	}
}

// TestCleanupWithConfig_FailClosed_OnAliasResolutionError verifies the
// fail-closed contract: when /aliases returns a non-ErrCollectionNotFound
// error (e.g. 502 Bad Gateway), CleanupWithConfig returns the wrapped
// error and drops NOTHING. This protects the active production collection
// from being dropped when qdrant is transiently unavailable. Mirrors
// InspectRuntime's fail-closed pattern at collection_prepare.go:20-25.
func TestCleanupWithConfig_FailClosed_OnAliasResolutionError(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	activeName := prefix + "__ts_20260601_active"
	colls := []string{
		activeName,                   // active alias target
		prefix + "__ts_20260101_aaa", // eligible
		prefix + "__ts_20260201_bbb", // eligible
	}

	// Minimal mock: 200 on /collections, 502 on /aliases (transient),
	// 500 on /collections/{name} DELETE (so we can assert it was
	// never reached — the fail-closed path must abort before the loop).
	mux := http.NewServeMux()
	var deleteCalls []string
	mux.HandleFunc("/collections", func(w http.ResponseWriter, r *http.Request) {
		out := struct {
			Result struct {
				Collections []struct {
					Name string `json:"name"`
				} `json:"collections"`
			} `json:"result"`
		}{}
		for _, c := range colls {
			out.Result.Collections = append(out.Result.Collections, struct {
				Name string `json:"name"`
			}{Name: c})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/aliases", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"status":{"error":"bad gateway"}}`)
	})
	mux.HandleFunc("/collections/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalls = append(deleteCalls, strings.TrimPrefix(r.URL.Path, "/collections/"))
		}
		http.Error(w, "fail-closed path must abort before DELETE", http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cm := NewCollectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: ts.URL, Timeout: 5}, zap.NewNop()), schema, zap.NewNop())
	_, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{RetentionDays: 1, KeepLastN: 3})

	if err == nil {
		t.Fatalf("CleanupWithConfig MUST return error on transient alias-resolution failure (fail-closed)")
	}
	if !strings.Contains(err.Error(), "resolve active target") {
		t.Errorf("error wrapping must preserve the 'resolve active target' diagnostic prefix; got %q", err.Error())
	}
	// Code-reviewer D3: assert the error wraps a typed *transport.APIError
	// so a future regression that breaks the wrap chain (e.g. replaces
	// fmt.Errorf("...%w", err) with errors.New(err.Error())) is caught.
	var apiErr *transport.APIError
	if !errors.As(err, &apiErr) {
		t.Errorf("error must wrap *transport.APIError; got %T (%v)", err, err)
	}
	if len(deleteCalls) != 0 {
		t.Errorf("DELETE was called %d times on fail-closed path; want 0; deleteCalls=%v", len(deleteCalls), deleteCalls)
	}
}

// statusAwareRetentionLedger wraps projectionLedgerStub with a pre-built
// projection set so the retention tests can pin exact ACTIVE/RETIRED/FAILED
// lifecycle states without touching a real SQLite DB.
func statusAwareRetentionLedger(projections []capregistry.Projection) *projectionLedgerStub {
	return &projectionLedgerStub{sequence: 291, projections: projections}
}

func retentionProjection(id, collection, status string, seq int64) capregistry.Projection {
	return capregistry.Projection{
		ProjectionID:      id,
		ProjectionType:    "qdrant",
		CollectionName:    collection,
		AliasName:         "media_assets_current",
		Status:            status,
		SourceRegistrySeq: seq,
		CreatedAt:         "2026-08-14T00:00:00Z",
	}
}

// TestCleanupWithConfig_StatusAware_FailedPartialNeverKept is the regression
// test for the failed-partial-vs-rollback bug. A FAILED partial has a NEWER
// name timestamp than the previous known-good rollback, so the legacy
// newest-by-name tail would keep the failed partial and DROP the rollback.
// The status-aware sweep must do the opposite: never protect a FAILED build,
// and keep the most recent known-good (RETIRED) rollback instead.
func TestCleanupWithConfig_StatusAware_FailedPartialNeverKept(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	active := prefix + "_20260814_071358_545816432"
	failedPartial := prefix + "_20260814_070758_298500178" // newer than rollback
	rollback := prefix + "_20260813_184719_687643250"      // previous known-good
	olderRetired := prefix + "_20260813_182650_373125513"  // older retired

	colls := []string{active, failedPartial, rollback, olderRetired}
	f := newFakeQdrantServer(colls, active)
	defer f.Close()

	ledger := statusAwareRetentionLedger([]capregistry.Projection{
		retentionProjection("active", active, string(capregistry.ProjectionActive), 291),
		retentionProjection("failed", failedPartial, string(capregistry.ProjectionFailed), 291),
		retentionProjection("rollback", rollback, string(capregistry.ProjectionRetired), 283),
		retentionProjection("older", olderRetired, string(capregistry.ProjectionRetired), 267),
	})

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: f.URL(), Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())
	if err := cm.SetRegistryLedger(context.Background(), ledger); err != nil {
		t.Fatalf("SetRegistryLedger: %v", err)
	}

	res, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{
		RetentionDays: 1,
		KeepLastN:     2,
	})
	if err != nil {
		t.Fatalf("CleanupWithConfig: %v", err)
	}

	gotDropped := append([]string(nil), res.DroppedNames...)
	wantDropped := []string{failedPartial, olderRetired}
	sort.Strings(gotDropped)
	sort.Strings(wantDropped)
	if !reflect.DeepEqual(gotDropped, wantDropped) {
		t.Fatalf("DroppedNames mismatch:\n got  %v\n want %v (FAILED partial must be dropped and rollback kept)", gotDropped, wantDropped)
	}

	if len(res.ProtectedKept) != 1 || res.ProtectedKept[0] != rollback {
		t.Fatalf("ProtectedKept = %v, want [%s] (the known-good rollback must be the tail-kept target)", res.ProtectedKept, rollback)
	}
	if res.CollectionsKept != 2 {
		t.Fatalf("CollectionsKept = %d, want 2 (active + rollback)", res.CollectionsKept)
	}
	if res.CollectionsDropped != 2 {
		t.Fatalf("CollectionsDropped = %d, want 2 (failed partial + older retired)", res.CollectionsDropped)
	}
}

// TestCleanupWithConfig_RetiredPrefixMatched verifies the retired-schema
// prefix gate: collections from a superseded schema generation (e5) are
// matched and dropped, while unknown collections that share NO registered
// prefix are left untouched.
func TestCleanupWithConfig_RetiredPrefixMatched(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	active := prefix + "_20260814_071358_545816432"
	nomicRollback := prefix + "_20260813_184719_687643250"
	e5 := "media_assets_v3_e5_768_siglip_768_20260813_181431_205856695"
	bareLegacy := "media_assets"
	synthetic := "synthetic_assets_test_v3"

	colls := []string{active, nomicRollback, e5, bareLegacy, synthetic}
	f := newFakeQdrantServer(colls, active)
	defer f.Close()

	ledger := statusAwareRetentionLedger([]capregistry.Projection{
		retentionProjection("active", active, string(capregistry.ProjectionActive), 291),
		retentionProjection("rollback", nomicRollback, string(capregistry.ProjectionRetired), 283),
		retentionProjection("e5", e5, string(capregistry.ProjectionRetired), 267),
	})

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: f.URL(), Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())
	if err := cm.SetRegistryLedger(context.Background(), ledger); err != nil {
		t.Fatalf("SetRegistryLedger: %v", err)
	}

	res, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{
		RetentionDays:   1,
		KeepLastN:       2,
		RetiredPrefixes: []string{"media_assets_v3_e5_768_siglip_768"},
	})
	if err != nil {
		t.Fatalf("CleanupWithConfig: %v", err)
	}

	if len(res.DroppedNames) != 1 || res.DroppedNames[0] != e5 {
		t.Fatalf("DroppedNames = %v, want [%s] (only the retired e5 collection should be dropped)", res.DroppedNames, e5)
	}
	for _, untouched := range []string{active, nomicRollback, bareLegacy, synthetic} {
		for _, dropped := range res.DroppedNames {
			if dropped == untouched {
				t.Fatalf("collection %q must never be dropped; DroppedNames=%v", untouched, res.DroppedNames)
			}
		}
	}
}

// TestCleanupWithConfig_DryRun_NoDeletion verifies the dry-run contract:
// the result reports the WOULD-BE dropped set but issues zero Qdrant
// DELETE calls.
func TestCleanupWithConfig_DryRun_NoDeletion(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	active := prefix + "_active"
	colls := []string{
		active,
		prefix + "_20260101_aaa",
		prefix + "_20260201_bbb",
	}
	f := newFakeQdrantServer(colls, active)
	defer f.Close()

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: f.URL(), Timeout: 5}, zap.NewNop())
	cm := NewCollectionManager(client, schema, zap.NewNop())

	res, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{
		RetentionDays: 1,
		KeepLastN:     2,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("CleanupWithConfig: %v", err)
	}

	if !res.DryRun {
		t.Fatalf("DryRun result flag must be true; res=%+v", res)
	}
	if len(f.deletedColls) != 0 {
		t.Fatalf("dry-run must NOT issue DELETE calls; deletedColls=%v", f.deletedColls)
	}
	wantDropped := prefix + "_20260101_aaa"
	if len(res.DroppedNames) != 1 || res.DroppedNames[0] != wantDropped {
		t.Fatalf("DroppedNames = %v, want [%s] (dry-run preview of the drop set)", res.DroppedNames, wantDropped)
	}
}

// TestRetentionPrefixes_RejectsOverlappingPrefix pins the fail-closed guard:
// a retired prefix that is a prefix of the current canonical name (e.g. a
// bare "media_assets") would match the live fleet and must be rejected.
func TestRetentionPrefixes_RejectsOverlappingPrefix(t *testing.T) {
	current := qdrantSchema.DefaultV3Schema().CanonicalName()

	if _, err := retentionPrefixes(current, []string{"media_assets"}); err == nil {
		t.Fatalf("expected error for overlapping prefix %q", "media_assets")
	}
	if _, err := retentionPrefixes(current, []string{"media_assets_v3"}); err == nil {
		t.Fatalf("expected error for overlapping prefix %q", "media_assets_v3")
	}
	if _, err := retentionPrefixes(current, []string{current}); err == nil {
		t.Fatalf("expected error for prefix equal to the current canonical name")
	}

	got, err := retentionPrefixes(current, []string{"media_assets_v3_e5_768_siglip_768", "", "media_assets_v3_e5_768_siglip_768"})
	if err != nil {
		t.Fatalf("unexpected error for valid retired prefix: %v", err)
	}
	want := []string{current, "media_assets_v3_e5_768_siglip_768"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retentionPrefixes = %v, want %v (dedup + skip empty)", got, want)
	}
}
