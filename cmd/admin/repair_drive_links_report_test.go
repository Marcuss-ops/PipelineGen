package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepairAuditReport_JSONRoundTrip(t *testing.T) {
	original := repairAuditReport{
		JobID:              "job-json-audit",
		ExecutedAt:         "2026-07-31T12:00:00Z",
		RemoveInvalid:      true,
		RefreshDocs:        true,
		AssetsReferenced:   3,
		Verified:           1,
		Updated:            1,
		BrokenLocations:    1,
		QdrantMismatches:   1,
		QdrantSynced:       1,
		SpecSceneRepaired:  true,
		SQLiteUpdated:      true,
		DocumentsRefreshed: 1,
		Warnings:           []string{"one warning"},
		Details: []repairAssetDetail{{
			ItemIdx: 0, SceneID: "scene-0", Label: "clip", AssetID: "asset-1",
			FileID: "file-1", Link: "https://drive.example/file-1", State: "UPDATED",
			ErrorCode: "STALE_LINK", Action: "updated",
		}},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal repair report: %v", err)
	}
	wire := string(raw)
	for _, key := range []string{`"job_id"`, `"executed_at"`, `"remove_invalid"`, `"refresh_docs"`, `"assets_referenced"`, `"qdrant_synced"`, `"warnings"`, `"details"`} {
		if !strings.Contains(wire, key) {
			t.Fatalf("expected repair report JSON key %s: %s", key, wire)
		}
	}
	var decoded repairAuditReport
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal repair report: %v", err)
	}
	if decoded.JobID != original.JobID || decoded.AssetsReferenced != 3 || decoded.QdrantSynced != 1 ||
		len(decoded.Warnings) != 1 || len(decoded.Details) != 1 || decoded.Details[0].ErrorCode != "STALE_LINK" {
		t.Fatalf("round-trip mismatch: original=%#v decoded=%#v json=%s", original, decoded, raw)
	}
}

func TestRepairAuditReport_EmptyOptionalFieldsOmitted(t *testing.T) {
	raw, err := json.Marshal(repairAuditReport{JobID: "job-empty"})
	if err != nil {
		t.Fatalf("marshal empty repair report: %v", err)
	}
	wire := string(raw)
	if strings.Contains(wire, `"warnings"`) || strings.Contains(wire, `"details"`) {
		t.Fatalf("empty optional report fields must be omitted: %s", wire)
	}
}
