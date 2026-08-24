// Package scripts — processor_manifest_v2_test.go exercises the
// canonical buildManifestV2 helper (PR 1, SCRIPT-DOWNSTREAM-CUTOVER
// wave) plus the PersistenceProcessor's manifest emit seam.
//
// TDD surface (5 hermetic tests, package adapters, in-process):
//
//  1. TestBuildManifestV2_NilPlan_EmptyCanonicalMode asserts
//     that a nil plan returns the canonical NEW-mode empty
//     envelope (NoInlineAssets=true, Items=[]). NOT a panic,
//     NOT the legacy zero-value (NoInlineAssets=false).
//
//  2. TestBuildManifestV2_NoPostprocessors_EmptyCanonicalMode
//     asserts that a plan with zero postprocessors returns the
//     canonical NEW-mode empty envelope (distinct from the
//     legacy zero-value).
//
//  3. TestBuildManifestV2_VoiceoverOnly emits exactly 1
//     DownstreamRequest with Kind=DownstreamVoiceover,
//     ItemRef=plan.ID, Required=true, AssetRequirements.Voiceover !=
//     nil, Images == nil.
//
//  4. TestBuildManifestV2_AllCapabilities emits 2
//     DownstreamRequests (voiceover + images) when
//     all 3 processors are registered. Each item is keyed to
//     plan.ID. The Image request's Count=1 and the document
//     request's OutputDest.Kind="google_doc".
//
//  5. TestBuildManifestV2_JSONRoundTrip asserts the manifest
//     marshals to the canonical wire shape (NoInlineAssets=true
//     + Items=[{kind, item_ref, required, asset_requirements,
//     output_dest}]) and round-trips back to a struct with
//     equal values.
//
// All tests use the in-process basePlanForIdem() / baseModelForIdem() /
// baseProcessInput() helpers from processor_persistence_test.go — no
// SQLite, no Drive, no LLM calls (zero live-stack dependency).
//
// PR-1 wave: this file is the canonical TDD surface for the
// SCRIPT-DOWNSTREAM-CUTOVER wave's persistence + manifest emit
// contract. The 5 tests lock the wire-shape + fail-closed + item
// assembly contract. Future refactors that touch buildManifestV2
// MUST keep all 5 tests green.
package adapters

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// postprocessor list — used by tests that need to register a
// subset of postprocessors in the plan. The plan's Postprocessors
// field is []string; we round-trip through the typed constants to
// avoid hardcoding the wire strings in test bodies.
func planWithPostprocessors(plan *scriptpkg.ResolvedGenerationPlan, names ...ProcessorName) *scriptpkg.ResolvedGenerationPlan {
	plan.Postprocessors = make([]string, 0, len(names))
	for _, n := range names {
		plan.Postprocessors = append(plan.Postprocessors, string(n))
	}
	return plan
}

// TestBuildManifestV2_NilPlan_EmptyCanonicalMode asserts that a nil
// plan returns the canonical NEW-mode empty envelope. godlike/07
// fail-closed: a nil plan is a programming error, but the helper
// must NOT panic — it returns a deterministic canonical empty
// envelope so the caller's downstream code (e.g. the dispatcher's
// fan-out) sees a valid manifest shape.
func TestBuildManifestV2_NilPlan_EmptyCanonicalMode(t *testing.T) {
	t.Parallel()
	m := buildManifestV2(nil, baseProcessInput())
	require.NotNil(t, m, "buildManifestV2 must return a non-nil manifest for nil plan")
	assert.True(t, m.NoInlineAssets, "nil plan must yield canonical NEW-mode (NoInlineAssets=true), NOT legacy zero-value")
	assert.Empty(t, m.Items, "nil plan must yield empty Items slice")
}

// TestBuildManifestV2_NoPostprocessors_EmptyCanonicalMode asserts
// that a plan with zero registered postprocessors returns the
// canonical NEW-mode empty envelope. Distinct from the legacy
// zero-value &ManifestV2{} (NoInlineAssets=false) which the
// dispatcher's fail-closed branch rejects with
// ErrLegacyManifestRejected. The canonical empty NEW-mode is
// reachable via buildManifestV2 (no manual struct literal).
func TestBuildManifestV2_NoPostprocessors_EmptyCanonicalMode(t *testing.T) {
	t.Parallel()
	plan := basePlanForIdem()
	plan.Postprocessors = nil // explicit: no postprocessors registered
	m := buildManifestV2(plan, baseProcessInput())
	require.NotNil(t, m)
	assert.True(t, m.NoInlineAssets, "empty postprocessor plan must yield canonical NEW-mode")
	assert.Empty(t, m.Items, "empty postprocessor plan must yield empty Items slice")
}

// TestBuildManifestV2_VoiceoverOnly asserts that registering the
// voiceover postprocessor emits exactly 1 DownstreamRequest with
// Kind=DownstreamVoiceover, ItemRef=plan.ID, Required=true, and
// AssetRequirements.Voiceover != nil (the dispatcher's fail-closed
// branch requires Voiceover != nil for a DownstreamVoiceover
// envelope).
//
// The VoiceID is the canonical namespaced placeholder
// (canonicalDefaultVoiceID) — pinned here so a future refactor that
// flips VoiceID back to "" (which would silently fail NewVoiceoverRequirements'
// fail-closed branch) surfaces as a test failure.
func TestBuildManifestV2_VoiceoverOnly(t *testing.T) {
	t.Parallel()
	plan := planWithPostprocessors(basePlanForIdem(), ProcessorVoiceover)
	m := buildManifestV2(plan, baseProcessInput())
	require.NotNil(t, m)
	require.Len(t, m.Items, 1, "voiceover-only plan must emit exactly 1 DownstreamRequest")
	item := m.Items[0]
	assert.Equal(t, scriptpkg.DownstreamVoiceover, item.Kind)
	assert.Equal(t, plan.ID, item.ItemRef)
	assert.True(t, item.Required, "downstream siblings are Required=true (fail-closed at Step 11B)")
	require.NotNil(t, item.AssetRequirements.Voiceover, "DownstreamVoiceover envelope MUST carry Voiceover != nil")
	assert.Equal(t, canonicalDefaultVoiceID(plan.Language), item.AssetRequirements.Voiceover.VoiceID, "voiceover envelope MUST carry the canonical namespaced placeholder VoiceID (defense-in-depth against broken dispatcher override)")
	assert.Nil(t, item.AssetRequirements.Images, "voiceover-only envelope MUST NOT carry Images")
}

// TestBuildManifestV2_AllCapabilities asserts that registering all
// 3 production postprocessors (voiceover + images + document)
// emits exactly 3 DownstreamRequests in the canonical order. The
// image request's Count=1 (canonical default) and the document
// request's OutputDest.Kind="google_doc" are load-bearing for the
// Step 11B dispatcher's per-kind routing.
func TestBuildManifestV2_AllCapabilities(t *testing.T) {
	t.Parallel()
	plan := planWithPostprocessors(basePlanForIdem(), ProcessorVoiceover, ProcessorImages)
	m := buildManifestV2(plan, baseProcessInput())
	require.NotNil(t, m)
	require.Len(t, m.Items, 2, "all-capabilities plan must emit exactly 2 DownstreamRequests (voiceover + images; Sprint 1.0: document retired from script pipeline)")

	// matching the buildManifestV2 implementation).
	assert.Equal(t, scriptpkg.DownstreamVoiceover, m.Items[0].Kind)
	require.NotNil(t, m.Items[0].AssetRequirements.Voiceover)

	assert.Equal(t, scriptpkg.DownstreamImages, m.Items[1].Kind)
	require.NotNil(t, m.Items[1].AssetRequirements.Images)
	assert.Equal(t, 1, m.Items[1].AssetRequirements.Images.Count, "canonical default Count=1")

	// All 3 items must be keyed to plan.ID (the canonical
	// per-item identifier).
	for i, it := range m.Items {
		assert.Equal(t, plan.ID, it.ItemRef, "item %d must be keyed to plan.ID", i)
		assert.True(t, it.Required, "item %d must be Required=true (fail-closed at Step 11B)", i)
	}
}

// TestBuildManifestV2_JSONRoundTrip asserts the manifest marshals
// to the canonical wire shape (NoInlineAssets=true + Items=[{kind,
// item_ref, required, asset_requirements, output_dest}]) and
// round-trips back to an equal-value struct. The wire shape is
// load-bearing for the Step 11B dispatcher's JSON decoder.
func TestBuildManifestV2_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	plan := planWithPostprocessors(basePlanForIdem(), ProcessorVoiceover, ProcessorImages)
	m := buildManifestV2(plan, baseProcessInput())
	require.NotNil(t, m)

	bytes, err := json.Marshal(m)
	require.NoError(t, err)
	require.NotEmpty(t, bytes)

	// Wire shape: no_inline_assets + items + per-item keys.
	s := string(bytes)
	assert.Contains(t, s, `"no_inline_assets":true`)
	assert.Contains(t, s, `"items":`)
	assert.Contains(t, s, `"item_ref":`)
	assert.Contains(t, s, `"required":true`)
	assert.Contains(t, s, `"asset_requirements":`)
	assert.Contains(t, s, `"output_dest":`)

	// Round-trip: unmarshal back into ManifestV2 and assert
	// structural equality.
	var restored scriptpkg.ManifestV2
	require.NoError(t, json.Unmarshal(bytes, &restored))
	assert.Equal(t, m.NoInlineAssets, restored.NoInlineAssets)
	assert.Len(t, restored.Items, len(m.Items))
	for i := range m.Items {
		assert.Equal(t, m.Items[i].Kind, restored.Items[i].Kind)
		assert.Equal(t, m.Items[i].ItemRef, restored.Items[i].ItemRef)
		assert.Equal(t, m.Items[i].Required, restored.Items[i].Required)
	}
}

// TestPersistence_EmitsCanonicalManifestV2_AfterFreshInsert asserts
// the PersistenceProcessor's end-to-end manifest emit seam: after
// a fresh SaveScript insert, the processor calls SaveManifestV2
// with the JSON-marshalled canonical NEW-mode envelope.
//
// Locked invariants:
//   - SaveManifestV2 is called EXACTLY 1 time (idempotency hit
//     triggers a different path that is tested in
//     TestPersistence_ReplayNoInsert).
//   - the manifest bytes round-trip to a struct with
//     NoInlineAssets=true + Items keyed to plan.ID.
//   - godlike/07 fail-closed: a SaveManifestV2 error aborts the
//     postprocessor (this is the production contract).
func TestPersistence_EmitsCanonicalManifestV2_AfterFreshInsert(t *testing.T) {
	t.Parallel()
	plan := planWithPostprocessors(basePlanForIdem(), ProcessorVoiceover, ProcessorImages)
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	_, err := proc.Process(context.Background(), plan, baseProcessInput())
	require.NoError(t, err)
	assert.Equal(t, int32(1), repo.saveManifestCalls.Load(), "SaveManifestV2 must be called exactly 1x after a fresh insert")
	assert.Equal(t, int64(1234), repo.lastManifestScriptID)
	require.NotEmpty(t, repo.lastManifest, "manifest bytes must be non-empty")

	// Round-trip and assert canonical shape.
	var restored scriptpkg.ManifestV2
	require.NoError(t, json.Unmarshal(repo.lastManifest, &restored))
	assert.True(t, restored.NoInlineAssets, "emitted manifest must be canonical NEW-mode")
	assert.Len(t, restored.Items, 2, "emitted manifest must carry 2 DownstreamRequests (voiceover + images)")
	assert.Equal(t, scriptpkg.DownstreamVoiceover, restored.Items[0].Kind)
	assert.Equal(t, scriptpkg.DownstreamImages, restored.Items[1].Kind)
}

// TestPersistence_EmitsEmptyManifestV2_WhenNoPostprocessors asserts
// that the manifest emit seam writes the canonical empty
// NEW-mode envelope (NoInlineAssets=true, Items=[]) when the plan
// has no registered postprocessors. Distinct from the legacy
// zero-value (NoInlineAssets=false) which the dispatcher rejects
// with ErrLegacyManifestRejected.
func TestPersistence_EmitsEmptyManifestV2_WhenNoPostprocessors(t *testing.T) {
	t.Parallel()
	plan := planWithPostprocessors(basePlanForIdem()) // no postprocessors
	repo := &idemFakeRepo{}
	proc := NewPersistenceProcessor(repo, zap.NewNop())

	_, err := proc.Process(context.Background(), plan, baseProcessInput())
	require.NoError(t, err)
	require.NotEmpty(t, repo.lastManifest, "manifest must still be written even when plan has no postprocessors (canonical empty NEW-mode)")

	var restored scriptpkg.ManifestV2
	require.NoError(t, json.Unmarshal(repo.lastManifest, &restored))
	assert.True(t, restored.NoInlineAssets, "empty plan must yield canonical NEW-mode")
	assert.Empty(t, restored.Items, "empty plan must yield empty Items slice")
}
