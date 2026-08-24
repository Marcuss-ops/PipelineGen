// envelope_test.go — PR 11 (June 2026) idempotency contract tests.
//
// event_key shape: "reconcile:reindex:<assetID>:<target_schema_version>:<full_content_hash>"
//
// These tests pin the idemptency invariants:
//   - identical inputs over two calls → same event_key
//   - hash change → different event_key
//   - schema change → different event_key
//   - assetID change → different event_key
//   - any required field empty → error (fail-closed)
//   - payload.source_version mirrors the event_key's hash component
//     so the worker downstream sees a coherent pair

package outboxevents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestBuildReindexEnvelopeV1_Deterministic — Identical inputs over
// two calls produce the same event_key. ON CONFLICT DO NOTHING
// collapses the second commit; this test pins that the key is stable,
// not random.
func TestBuildReindexEnvelopeV1_Deterministic(t *testing.T) {
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	k1, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 1: unexpected error: %v", err)
	}
	k2, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 2: unexpected error: %v", err)
	}
	if k1 != k2 {
		t.Fatalf("event_key not deterministic: k1=%q k2=%q", k1, k2)
	}
	wantPrefix := "reconcile:reindex:asset-1:media_assets_v3:hash-aaaa"
	if k1 != wantPrefix {
		t.Fatalf("event_key shape drifted: got %q want %q", k1, wantPrefix)
	}
}

// TestBuildReindexEnvelopeV1_HashChange — A source_version change
// produces a different event_key so the worker can re-evaluate on
// the new fingerprint (a fresh ingest detected new content).
func TestBuildReindexEnvelopeV1_HashChange(t *testing.T) {
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	k1, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	k2, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-bbbb", t0)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if k1 == k2 {
		t.Fatalf("event_key should differ on hash change: both=%q", k1)
	}
}

// TestBuildReindexEnvelopeV1_SchemaChange — A targetSchemaVersion
// change (collection / schema upgrade between reconcile runs)
// produces a different event_key. Schema bump means a different
// physical target the worker should re-evaluate against.
func TestBuildReindexEnvelopeV1_SchemaChange(t *testing.T) {
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	k1, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	k2, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v4", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if k1 == k2 {
		t.Fatalf("event_key should differ on schema change: both=%q", k1)
	}
}

// TestBuildReindexEnvelopeV1_AssetChange — Distinct assets produce
// distinct event_keys.
func TestBuildReindexEnvelopeV1_AssetChange(t *testing.T) {
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)
	k1, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	k2, _, err := BuildReindexEnvelopeV1("asset-2", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if k1 == k2 {
		t.Fatalf("event_key should differ across assets: both=%q", k1)
	}
}

// TestBuildReindexEnvelopeV1_RejectsEmptyAssetID — fail-closed on
// missing required input.
func TestBuildReindexEnvelopeV1_RejectsEmptyAssetID(t *testing.T) {
	_, _, err := BuildReindexEnvelopeV1("", "media_assets_v3", "hash-aaaa", time.Now())
	if err == nil {
		t.Fatalf("expected error for empty assetID")
	}
	if !strings.Contains(err.Error(), "assetID") {
		t.Fatalf("error should mention assetID, got: %v", err)
	}
}

// TestBuildReindexEnvelopeV1_RejectsEmptySchema — fail-closed on
// missing schema version (cannot derive a deterministic key).
func TestBuildReindexEnvelopeV1_RejectsEmptySchema(t *testing.T) {
	_, _, err := BuildReindexEnvelopeV1("asset-1", "", "hash-aaaa", time.Now())
	if err == nil {
		t.Fatalf("expected error for empty schema")
	}
	if !strings.Contains(err.Error(), "targetSchemaVersion") && !strings.Contains(err.Error(), "schema") {
		t.Fatalf("error should mention schema, got: %v", err)
	}
}

// TestBuildReindexEnvelopeV1_RejectsEmptySourceVersion — fail-closed
// on missing fingerprint (PR 11 hardening: empty sourceVersion would
// silently collapse every reconcile run into one row even when the
// underlying content changed).
func TestBuildReindexEnvelopeV1_RejectsEmptySourceVersion(t *testing.T) {
	_, _, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "", time.Now())
	if err == nil {
		t.Fatalf("expected error for empty sourceVersion")
	}
	if !strings.Contains(err.Error(), "sourceVersion") && !strings.Contains(err.Error(), "content_hash") {
		t.Fatalf("error should mention sourceVersion / content_hash, got: %v", err)
	}
}

// TestBuildReindexEnvelopeV1_PayloadMirrorsKey — The payload's
// source_version field carries the same hash the event_key is built
// from. Worker downstream reads payload.source_version for the
// supersede gate; a divergence between event_key and payload would
// split the dedup vector across two columns and silently re-emit.
func TestBuildReindexEnvelopeV1_PayloadMirrorsKey(t *testing.T) {
	k, pJSON, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", time.Now())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.HasSuffix(k, ":hash-aaaa") {
		t.Fatalf("event_key missing hash suffix: %q", k)
	}
	var payload map[string]any
	if jerr := json.Unmarshal([]byte(pJSON), &payload); jerr != nil {
		t.Fatalf("payload unmarshal: %v", jerr)
	}
	if payload["source_version"] != "hash-aaaa" {
		t.Fatalf("payload.source_version=%v want hash-aaaa", payload["source_version"])
	}
	if payload["idempotency_key"] != k {
		t.Fatalf("payload.idempotency_key=%v should equal event_key=%q", payload["idempotency_key"], k)
	}
	if payload["schema_version"] != ReindexEnvelopeV1Schema {
		t.Fatalf("payload.schema_version=%v want %q", payload["schema_version"], ReindexEnvelopeV1Schema)
	}
	if payload["asset_id"] != "asset-1" {
		t.Fatalf("payload.asset_id=%v want asset-1", payload["asset_id"])
	}
	if payload["target_index_version"] != "media_assets_v3" {
		t.Fatalf("payload.target_index_version=%v want media_assets_v3", payload["target_index_version"])
	}
	if evID, _ := payload["event_id"].(string); evID == "" {
		t.Fatalf("payload.event_id is empty (audit token must be set so logs distinguish two re-emitted events)")
	}
}

// TestBuildReindexEnvelopeV1_EventIDUniqueAcrossCalls — The audit
// event_id is a per-call UUID; the dedup event_key is NOT. Split
// keeps log-searchability without sacrificing dedup.
func TestBuildReindexEnvelopeV1_EventIDUniqueAcrossCalls(t *testing.T) {
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)
	_, p1, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	_, p2, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	var id1, id2 map[string]any
	_ = json.Unmarshal([]byte(p1), &id1)
	_ = json.Unmarshal([]byte(p2), &id2)
	if id1["event_id"] == id2["event_id"] {
		t.Fatalf("event_id must be unique per call; both=%v", id1["event_id"])
	}
	if id1["idempotency_key"] != id2["idempotency_key"] {
		t.Fatalf("idempotency_key (event_key) must be stable across calls; got %v vs %v",
			id1["idempotency_key"], id2["idempotency_key"])
	}
}
