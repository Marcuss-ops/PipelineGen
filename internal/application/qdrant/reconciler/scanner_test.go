package reconciler

import (
	"reflect"
	"sort"
	"testing"
)

// classifyTableTest is one row in the table-driven classify test.
type classifyTableTest struct {
	name     string
	schema   SchemaVersions
	sqlite   map[string]AssetSnapshot
	qdrant   map[string]pointWithID
	expected []Classification // set equality, kind + asset_id order-insensitive
}

func defaultSchema() SchemaVersions {
	return SchemaVersions{
		Version:      "v3",
		PhysicalName: "media_assets_v3_nomic_768_siglip_768",
		RuntimeAlias: "media_assets_current",
		// PerChannelVersion is intentionally empty for the default
		// test schema: tests that don't focus on the version-check
		// path should not have to populate embedding_version_<ch>
		// for every channel. Tests that exercise version checks use
		// versionCheckSchema() below.
		PerChannelVersion: nil,
		RequiredKeys:      []string{"asset_id", "name", "source", "lifecycle_state"},
	}
}

// versionCheckSchema returns a SchemaVersions that enables the
// per-channel version-stale check for the text channel only. Tests
// that exercise the version-stale classification path use this
// helper instead of defaultSchema() so fixtures can focus on a
// single channel's behaviour.
func versionCheckSchema() SchemaVersions {
	return SchemaVersions{
		Version:      "v3",
		PhysicalName: "media_assets_v3_nomic_768_siglip_768",
		RuntimeAlias: "media_assets_current",
		PerChannelVersion: map[string]string{
			"text": "2026-06-16-v1",
		},
		RequiredKeys: []string{"asset_id", "name", "source", "lifecycle_state"},
	}
}

// multiChannelSchema returns a SchemaVersions that enables the
// per-channel version-stale check for BOTH text AND transcript
// channels. Used by TestReconcile_VersionMismatchPerChannel_Emitted
// to verify per-channel tracking of stale mismatches across more
// than one embedding channel.
func multiChannelSchema() SchemaVersions {
	return SchemaVersions{
		Version:      "v3",
		PhysicalName: "media_assets_v3_nomic_768_siglip_768",
		RuntimeAlias: "media_assets_current",
		PerChannelVersion: map[string]string{
			"text":       "2026-06-16-v1",
			"transcript": "2026-06-16-v1",
		},
		RequiredKeys: []string{"asset_id", "name", "source", "lifecycle_state"},
	}
}

// equalClassifications compares two slices ignoring order.
func equalClassifications(a, b []Classification) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]Classification{}, a...)
	bc := append([]Classification{}, b...)
	sort.Slice(ac, func(i, j int) bool { return ac[i].AssetID+string(ac[i].Kind) < ac[j].AssetID+string(ac[j].Kind) })
	sort.Slice(bc, func(i, j int) bool { return bc[i].AssetID+string(bc[i].Kind) < bc[j].AssetID+string(bc[j].Kind) })
	return reflect.DeepEqual(ac, bc)
}

func TestClassify_NoDrift(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{
		"a1": {ID: "a1", WorkspaceID: "ws1", LifecycleState: "ACTIVE"},
	}
	qdrant := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":               "a1",
			"name":                   "x",
			"source":                 "youtube",
			"lifecycle_state":        "ACTIVE",
			"workspace_id":           "ws1",
			"embedding_version_text": "2026-06-16-v1",
		}},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 0 {
		t.Fatalf("expected zero classifications, got %#v", got)
	}
}

func TestClassify_Missing(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{
		"a1": {ID: "a1", WorkspaceID: "ws1", LifecycleState: "ACTIVE"},
	}
	qdrant := map[string]pointWithID{}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindMissing || got[0].AssetID != "a1" {
		t.Fatalf("expected one Missing(a1), got %#v", got)
	}
}

func TestClassify_Orphan(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{}
	qdrant := map[string]pointWithID{
		"orphan1": {ID: canonicalPointID("orphan1"), Payload: map[string]interface{}{
			"asset_id": "orphan1",
		}},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindOrphan || got[0].AssetID != "orphan1" {
		t.Fatalf("expected one Orphan(orphan1), got %#v", got)
	}
	if got[0].QdrantPointID != canonicalPointID("orphan1") {
		t.Fatalf("expected QdrantPointID %q, got %q", canonicalPointID("orphan1"), got[0].QdrantPointID)
	}
}

func TestClassify_NonCanonicalPointID_PriorityWins(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{
		"a1": {ID: "a1", WorkspaceID: "ws1", LifecycleState: "ACTIVE"},
	}
	// Point UUID mismatches the canonical AssetIDToQdrantPointID(a1).
	qdrant := map[string]pointWithID{
		"a1": {ID: "wrong-uuid", Payload: map[string]interface{}{
			"asset_id":               "a1",
			"name":                   "x",
			"source":                 "youtube",
			"lifecycle_state":        "ACTIVE",
			"workspace_id":           "ws1",
			"embedding_version_text": "2026-06-16-v1",
		}},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d (%#v)", len(got), got)
	}
	if got[0].Kind != KindNonCanonicalPointID {
		t.Fatalf("priority: expected NonCanonicalPointID, got %s", got[0].Kind)
	}
}

func TestClassify_PayloadIncomplete_PriorityOverVersionStale(t *testing.T) {
	schema := versionCheckSchema()
	sqlite := map[string]AssetSnapshot{"a1": {ID: "a1", LifecycleState: "ACTIVE"}}
	// Missing "name" required → PayloadIncomplete should be first.
	qdrant := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":               "a1",
			"source":                 "youtube",
			"lifecycle_state":        "ACTIVE",
			"embedding_version_text": "v0", // would also fire VersionStale
		}},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindPayloadIncomplete {
		t.Fatalf("expected PayloadIncomplete wins over VersionStale, got %s", got[0].Kind)
	}
}

func TestClassify_VersionStale_LegacyGlobalFallback(t *testing.T) {
	schema := versionCheckSchema()
	sqlite := map[string]AssetSnapshot{"a1": {ID: "a1", LifecycleState: "ACTIVE"}}

	// Schema says text="2026-06-16-v1"; payload has legacy
	// "embedding_version"="2026-06-16-v1" but no per-channel key.
	// Per the verifier fallback rule, missing per-channel key with
	// matching legacy global → NO mismatch.
	qdrantMatchLegacy := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":          "a1",
			"name":              "x",
			"source":            "youtube",
			"lifecycle_state":   "ACTIVE",
			"embedding_version": "2026-06-16-v1",
		}},
	}
	got := classify(sqlite, qdrantMatchLegacy, schema, canonicalPointID, nil)
	if len(got) != 0 {
		t.Fatalf("legacy global fallback should NOT produce VersionStale, got %#v", got)
	}

	// Schema says text="2026-06-16-v1"; payload has legacy global
	// "embedding_version"="v0" → VersionStale on text channel.
	qdrantMismatchLegacy := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":          "a1",
			"name":              "x",
			"source":            "youtube",
			"lifecycle_state":   "ACTIVE",
			"embedding_version": "v0",
		}},
	}
	got = classify(sqlite, qdrantMismatchLegacy, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindVersionStale {
		t.Fatalf("legacy global mismatch should produce VersionStale, got %#v", got)
	}
	if got[0].Channel != "text" {
		t.Fatalf("expected Channel=text, got %q", got[0].Channel)
	}
}

func TestClassify_LifecycleMismatch_CaseInsensitive(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{"a1": {ID: "a1", LifecycleState: "ACTIVE"}}
	qdrant := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":               "a1",
			"name":                   "x",
			"source":                 "youtube",
			"lifecycle_state":        "active", // lowercase; should still match
			"embedding_version_text": "2026-06-16-v1",
		}},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 0 {
		t.Fatalf("case-insensitive lifecycle match should not classify, got %#v", got)
	}

	// Now mismatch (deleted).
	qdrant["a1"].Payload["lifecycle_state"] = "DELETED"
	got = classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindLifecycleMismatch {
		t.Fatalf("expected LifecycleMismatch(ACTIVE vs DELETED), got %#v", got)
	}
}

func TestClassify_WorkspaceMismatch(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{"a1": {ID: "a1", WorkspaceID: "ws1", LifecycleState: "ACTIVE"}}
	qdrant := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":               "a1",
			"name":                   "x",
			"source":                 "youtube",
			"lifecycle_state":        "ACTIVE",
			"workspace_id":           "ws2",
			"embedding_version_text": "2026-06-16-v1",
		}},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindWorkspaceMismatch {
		t.Fatalf("expected WorkspaceMismatch, got %#v", got)
	}
}

func TestClassify_LegacyKeys(t *testing.T) {
	schema := defaultSchema()
	sqlite := map[string]AssetSnapshot{"a1": {ID: "a1", LifecycleState: "ACTIVE"}}

	// legacy "status" present
	qdrantStatus := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":               "a1",
			"name":                   "x",
			"source":                 "youtube",
			"lifecycle_state":        "ACTIVE",
			"status":                 "ACTIVE",
			"embedding_version_text": "2026-06-16-v1",
		}},
	}
	got := classify(sqlite, qdrantStatus, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindLifecycleKeyLegacy {
		t.Fatalf("expected LifecycleKeyLegacy, got %#v", got)
	}

	// legacy "drive_link" present
	qdrantDriveLink := map[string]pointWithID{
		"a1": {ID: canonicalPointID("a1"), Payload: map[string]interface{}{
			"asset_id":               "a1",
			"name":                   "x",
			"source":                 "youtube",
			"lifecycle_state":        "ACTIVE",
			"drive_link":             "https://drive.example/x",
			"embedding_version_text": "2026-06-16-v1",
		}},
	}
	got = classify(sqlite, qdrantDriveLink, schema, canonicalPointID, nil)
	if len(got) != 1 || got[0].Kind != KindLocatorLegacy {
		t.Fatalf("expected LocatorLegacy(drive_link), got %#v", got)
	}
}

func TestClassify_Mixed_DeterministicOrdering(t *testing.T) {
	schema := versionCheckSchema()
	sqlite := map[string]AssetSnapshot{
		"missing": {ID: "missing", LifecycleState: "ACTIVE"},
		"clean":   {ID: "clean", LifecycleState: "ACTIVE"},
		"stale":   {ID: "stale", LifecycleState: "ACTIVE"},
	}
	qdrant := map[string]pointWithID{
		"orphan": {ID: canonicalPointID("orphan"), Payload: map[string]interface{}{"asset_id": "orphan"}},
		"clean": {
			ID: canonicalPointID("clean"), Payload: map[string]interface{}{
				"asset_id": "clean", "name": "x", "source": "youtube",
				"lifecycle_state": "ACTIVE", "embedding_version_text": "2026-06-16-v1",
			},
		},
		"stale": {
			ID: canonicalPointID("stale"), Payload: map[string]interface{}{
				"asset_id": "stale", "name": "x", "source": "youtube",
				"lifecycle_state":        "ACTIVE",
				"embedding_version_text": "v0",
			},
		},
	}
	got := classify(sqlite, qdrant, schema, canonicalPointID, nil)
	if len(got) != 3 {
		t.Fatalf("expected 3 classifications, got %d (%#v)", len(got), got)
	}
	if got[0].Kind != KindMissing || got[0].AssetID != "missing" {
		t.Fatalf("expected Missing(missing) first, got %s(%s)", got[0].Kind, got[0].AssetID)
	}
	if got[1].Kind != KindOrphan || got[1].AssetID != "orphan" {
		t.Fatalf("expected Orphan(orphan) second, got %s(%s)", got[1].Kind, got[1].AssetID)
	}
	if got[2].Kind != KindVersionStale || got[2].AssetID != "stale" {
		t.Fatalf("expected VersionStale(stale) third, got %s(%s)", got[2].Kind, got[2].AssetID)
	}
}

func TestEqualClassifications_IgnoresOrder(t *testing.T) {
	a := []Classification{
		{Kind: KindMissing, AssetID: "a"},
		{Kind: KindOrphan, AssetID: "b"},
	}
	b := []Classification{
		{Kind: KindOrphan, AssetID: "b"},
		{Kind: KindMissing, AssetID: "a"},
	}
	if !equalClassifications(a, b) {
		t.Fatalf("equalClassifications should be order-insensitive")
	}
}
