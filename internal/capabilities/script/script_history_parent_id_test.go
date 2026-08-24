// Package script (api) — script_history_parent_id_test.go regression
// guard for DRIFT-23-4 (HC-7, June 2026): the pre-fix bug at
// internal/api/script/helpers.go::ListScripts emitted literal
// gin.H{"parent_id": ""} for every row of /scripts, regardless of
// whether the script had a parent. The historical fix is in place
// (line 128 now emits s.ParentScriptID, an int64 marshaled as a JSON
// number; the gin.H literal is gone).
//
// This test pins the ACTUAL HTTP/JSON boundary — the only layer where
// the historical bug surfaced. A SQLite-layer test would not catch a
// regression here, because the SQLite repository would still hand
// back a record with a correctly-populated `*int64 ParentScriptID`;
// the bug was downstream at the JSON emit.
//
// The test asserts:
//  1. No row in /scripts contains the historical literal `""` as
//     parent_id (anti-pattern detector).
//  2. A child script's parent_id equals the parent's id as a JSON
//     number (positive case).
//  3. ListScripts and GetScriptByID return the SAME parent_id for
//     the same id (round-trip invariant — guards divergence between
//     the two emits).
//  4. A root script (no parent) emits `parent_id: 0` as a JSON
//     number, not `""` — so downstream callers can distinguish
//     root from child.
//
// Test refs:
//   - architecture/current.yaml::DRIFT-23-4 (HC-7)
//   - scripts/ci-architectural-checks.sh (empty-string literal gate)
//   - internal/api/script/helpers.go::ListScripts lines 78-130
//
// Convention note: the fakeScriptHistoryRepo below uses the
// per-method-on-fake pattern (each method declared directly on the
// struct) to match fakeJobsService in handler_test.go. Ten stub
// methods return errors.New("not implemented") so any future handler
// expansion that begins exercising a new method surfaces at this
// test instead of silently returning zero values.
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"


	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
)

// fakeScriptHistoryRepo satisfies usecase.ScriptRepository with two
// functional read paths (ListScripts, GetScriptByID) used by
// ScriptHistoryHandler and ten stub methods (errors.New on call) for
// every other interface method. Pattern matches fakeJobsService in
// handler_test.go for convention consistency.
type fakeScriptHistoryRepo struct {
	scripts []*adapters.ScriptRecord
}

// ── Functional methods (used by ScriptHistoryHandler) ────────────

// ListScripts returns the in-memory fake state regardless of filter.
func (f *fakeScriptHistoryRepo) ListScripts(_ context.Context, _ adapters.ScriptListFilter) ([]*adapters.ScriptRecord, error) {
	return f.scripts, nil
}

// GetScriptByID returns (record, nil, nil, nil) for known ids, or
// "script not found" for unknown ids.
func (f *fakeScriptHistoryRepo) GetScriptByID(id int64) (*adapters.ScriptRecord, []adapters.ScriptSectionRecord, []adapters.ScriptStockMatchRecord, error) {
	for _, s := range f.scripts {
		if s.ID == id {
			return s, nil, nil, nil
		}
	}
	return nil, nil, nil, fmt.Errorf("script not found: %d", id)
}

// ── Stub methods (not exercised by these tests) ─────────────────
//
// Return errors.New("...not implemented") for every unimplemented
// method so handler drift surfaces here at test time rather than
// silently emitting zero values into the JSON response. Pattern
// matches fakeJobsService which uses errors.New for the same purpose.

// SaveScript is unimplemented.
func (f *fakeScriptHistoryRepo) SaveScript(_ context.Context, _ *adapters.ScriptRecord, _ []adapters.ScriptSectionRecord, _ []adapters.ScriptStockMatchRecord) (int64, error) {
	return 0, errors.New("fakeScriptHistoryRepo.SaveScript: not implemented (parent-id regression test only exercises ListScripts + GetScriptByID)")
}

// UpdateScriptFinalContent is unimplemented. The 9-param shape
// (ctx, scriptID int64, outputText string, wordCount int, status string,
// metadata string, model string, ollamaBaseURL string, version int)
// satisfies the interface contract; rename helpers when adding
// meaningful test coverage for this method.
func (f *fakeScriptHistoryRepo) UpdateScriptFinalContent(_ context.Context, _ int64, _ string, _ int, _, _, _, _ string, _ int) error {
	return errors.New("fakeScriptHistoryRepo.UpdateScriptFinalContent: not implemented")
}

func (f *fakeScriptHistoryRepo) SaveGenerationLog(_ context.Context, _ adapters.ScriptGenerationLog) error {
	return errors.New("fakeScriptHistoryRepo.SaveGenerationLog: not implemented")
}

func (f *fakeScriptHistoryRepo) SaveOutlineSections(_ context.Context, _ int64, _ []adapters.ScriptOutlineSectionRecord) error {
	return errors.New("fakeScriptHistoryRepo.SaveOutlineSections: not implemented")
}

func (f *fakeScriptHistoryRepo) SaveResearchSources(_ context.Context, _ int64, _ []adapters.ScriptResearchSource) error {
	return errors.New("fakeScriptHistoryRepo.SaveResearchSources: not implemented")
}

func (f *fakeScriptHistoryRepo) SaveManifestV2(_ context.Context, _ int64, _ ports.ScriptManifestJSON) error {
	return errors.New("fakeScriptHistoryRepo.SaveManifestV2: not implemented")
}

func (f *fakeScriptHistoryRepo) NextVersionForTopic(_ context.Context, _, _, _ string) (int, error) {
	return 0, errors.New("fakeScriptHistoryRepo.NextVersionForTopic: not implemented")
}

func (f *fakeScriptHistoryRepo) GetSectionByID(_ context.Context, _ int64) (*adapters.ScriptSectionRecord, error) {
	return nil, errors.New("fakeScriptHistoryRepo.GetSectionByID: not implemented")
}

func (f *fakeScriptHistoryRepo) GetAdjacentSections(_ context.Context, _ int64, _ int) (*adapters.ScriptSectionRecord, *adapters.ScriptSectionRecord, error) {
	return nil, nil, errors.New("fakeScriptHistoryRepo.GetAdjacentSections: not implemented")
}

func (f *fakeScriptHistoryRepo) UpdateSectionContent(_ context.Context, _ int64, _ string) error {
	return errors.New("fakeScriptHistoryRepo.UpdateSectionContent: not implemented")
}

func (f *fakeScriptHistoryRepo) FindScriptByIdempotencyKey(_ context.Context, _, _, _ string, _ int, _ string) (*adapters.ScriptRecord, bool, error) {
	return nil, false, errors.New("fakeScriptHistoryRepo.FindScriptByIdempotencyKey: not implemented")
}

// Compile-time assertion: fakeScriptHistoryRepo satisfies the
// canonical script-repository contract consumed by ScriptHistoryHandler
// (Contract: internal/application/scripts/adapters.ScriptRepository).
var _ adapters.ScriptRepository = (*fakeScriptHistoryRepo)(nil)

// newParentChildScriptHistoryRouter wires a /scripts router with two
// scripts in the fake repo: a root (id=1, no parent of its own) and
// a child (id=2, ParentScriptID=1). Returns the gin engine and the
// underlying fake for inspection by individual tests.
func newParentChildScriptHistoryRouter(t *testing.T) (*gin.Engine, *fakeScriptHistoryRepo) {
	t.Helper()
	parent := &adapters.ScriptRecord{
		ID:    1,
		Title: "Root script",
		Topic: "history",
		// ParentScriptID intentionally 0 — this is a root script.
	}
	child := &adapters.ScriptRecord{
		ID:             2,
		Title:          "Child script",
		Topic:          "history-v2",
		ParentScriptID: 1, // points at parent.ID
	}
	repo := &fakeScriptHistoryRepo{scripts: []*adapters.ScriptRecord{parent, child}}
	handler := NewScriptHistoryHandler(repo, zap.NewNop())
	router := gin.New()
	grp := router.Group("/scripts")
	handler.RegisterRoutes(grp)
	return router, repo
}

// TestScriptHistory_ListScripts_DoesNotEmitEmptyParentID is the core
// DRIFT-23-4 anti-pattern detector. It scans every row of /scripts for
// the historical literal `"parent_id": ""` and fails the test if any
// row re-introduces it. Also asserts:
//   - The child row's parent_id equals the parent script's id as a
//     JSON number (positive case).
//   - The root row's parent_id is 0 (number) — distinct from literal
//     "" so callers can tell root from child.
func TestScriptHistory_ListScripts_DoesNotEmitEmptyParentID(t *testing.T) {
	router, _ := newParentChildScriptHistoryRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/scripts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Scripts []map[string]json.RawMessage `json:"scripts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Scripts, 2)

	var parentRow, childRow map[string]json.RawMessage
	for _, sc := range resp.Scripts {
		var id int
		require.NoError(t, json.Unmarshal(sc["id"], &id))
		switch id {
		case 1:
			parentRow = sc
		case 2:
			childRow = sc
		}
	}
	require.NotNil(t, parentRow, "root script (id=1) missing from /scripts")
	require.NotNil(t, childRow, "child script (id=2) missing from /scripts")

	// 1. Defensive scan: no row may emit the historical literal `""`.
	for i, sc := range resp.Scripts {
		p, ok := sc["parent_id"]
		require.True(t, ok, "row[%d] missing parent_id field", i)
		require.NotEqual(t, `""`, string(p),
			"row[%d] emitted the historical literal \"parent_id\": \"\" (DRIFT-23-4 regression)", i)
	}

	// 2. Child row's parent_id must equal the parent script's id (1) as JSON number.
	assert.JSONEq(t, "1", string(childRow["parent_id"]),
		"child row's parent_id must equal the parent script's id (1) as a JSON number")

	// 3. Root row must surface 0 (number), distinct from literal "".
	assert.JSONEq(t, "0", string(parentRow["parent_id"]),
		"root script's parent_id must be 0 (JSON number zero), not \"\"")
}

// TestScriptHistory_GetScriptByID_RoundTripsParentID asserts the
// round-trip invariant: ListScripts(childID).parent_id must equal
// GetScriptByID(childID).parent_id, AND neither path may emit the
// literal `"parent_id": ""`. Guards divergence between the two
// emit sites (so a future fix to one path doesn't drift the other).
func TestScriptHistory_GetScriptByID_RoundTripsParentID(t *testing.T) {
	router, _ := newParentChildScriptHistoryRouter(t)

	// Read the child via the canonical GetScriptByID path.
	req := httptest.NewRequest(http.MethodGet, "/scripts/2", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var single map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &single))
	p, ok := single["parent_id"]
	require.True(t, ok, "GetScriptByID response missing parent_id")
	require.NotEqual(t, `""`, string(p),
		"GetScriptByID emitted the historical literal \"parent_id\": \"\" (DRIFT-23-4 regression)")
	require.JSONEq(t, "1", string(p),
		"GetScriptByID on child must emit parent_id = 1 (parent script's id) as JSON number")

	// Read the child via the ListScripts path.
	listReq := httptest.NewRequest(http.MethodGet, "/scripts", nil)
	listW := httptest.NewRecorder()
	router.ServeHTTP(listW, listReq)
	require.Equal(t, http.StatusOK, listW.Code)
	var listResp struct {
		Scripts []map[string]json.RawMessage `json:"scripts"`
	}
	require.NoError(t, json.Unmarshal(listW.Body.Bytes(), &listResp))

	var childListParent json.RawMessage
	for _, sc := range listResp.Scripts {
		var id int
		require.NoError(t, json.Unmarshal(sc["id"], &id))
		if id == 2 {
			childListParent = sc["parent_id"]
			break
		}
	}
	require.NotEmpty(t, childListParent, "child row missing from ListScripts")

	// 4. Round-trip invariant: ListScripts(2).parent_id ==
	//    GetScriptByID(2).parent_id (raw JSON value equality).
	assert.JSONEq(t, string(single["parent_id"]), string(childListParent),
		"ListScripts(2).parent_id must equal GetScriptByID(2).parent_id (round-trip invariant)")
}

// TestScriptHistory_GetScriptByID_RootEmitsZeroParentID asserts that
// the canonical GetScriptByID read path distinguishes root scripts
// (no parent) from children: the JSON value must be the number 0,
// NOT the empty string. Regression here means downstream callers
// can no longer tell whether a row is a root or a missing-data
// sentinel.
func TestScriptHistory_GetScriptByID_RootEmitsZeroParentID(t *testing.T) {
	router, _ := newParentChildScriptHistoryRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/scripts/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var single map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &single))
	p, ok := single["parent_id"]
	require.True(t, ok, "GetScriptByID response missing parent_id")

	require.NotEqual(t, `""`, string(p),
		"GetScriptByID on root emitted the historical literal \"parent_id\": \"\"")
	assert.JSONEq(t, "0", string(p),
		"root script must surface parent_id: 0 (JSON number), not \"\"")
}
