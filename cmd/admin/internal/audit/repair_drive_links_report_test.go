package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepairAuditReport_JSONRoundTrip(t *testing.T) {
	original := repairAuditReport{
		JobID:               "job-json-audit",
		ExecutedAt:          "2026-07-31T12:00:00Z",
		RemoveInvalid:       true,
		RefreshDocs:         true,
		AssetsReferenced:    3,
		Verified:            1,
		Updated:             1,
		BrokenLocations:     1,
		QdrantMismatches:    1,
		QdrantEventsEmitted: 1,
		SpecSceneRepaired:   true,
		SQLiteUpdated:       true,
		DocumentsRefreshed:  1,
		Warnings:            []string{"one warning"},
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
	for _, key := range []string{`"job_id"`, `"executed_at"`, `"remove_invalid"`, `"refresh_docs"`, `"assets_referenced"`, `"qdrant_events_emitted"`, `"warnings"`, `"details"`} {
		if !strings.Contains(wire, key) {
			t.Fatalf("expected repair report JSON key %s: %s", key, wire)
		}
	}
	var decoded repairAuditReport
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal repair report: %v", err)
	}
	if decoded.JobID != original.JobID || decoded.AssetsReferenced != 3 || decoded.QdrantEventsEmitted != 1 ||
		len(decoded.Warnings) != 1 || len(decoded.Details) != 1 || decoded.Details[0].ErrorCode != "STALE_LINK" {
		t.Fatalf("round-trip mismatch: original=%#v decoded=%#v json=%s", original, decoded, raw)
	}
}

func TestRepairReportIsNoOp_RequiresZeroSideEffects(t *testing.T) {
	if !repairReportIsNoOp(repairAuditReport{}) {
		t.Fatal("zero-valued repair report must be classified as a no-op")
	}
	readOnly := repairAuditReport{
		AssetsReferenced: 1,
		Verified:         1,
		Details:          []repairAssetDetail{{State: "VERIFIED", Action: "preserved"}},
	}
	if !repairReportIsNoOp(readOnly) {
		t.Fatalf("read-only observations must remain a no-op: %#v", readOnly)
	}

	cases := []struct {
		name   string
		mutate func(*repairAuditReport)
	}{
		{"sqlite", func(r *repairAuditReport) { r.SQLiteUpdated = true }},
		{"qdrant", func(r *repairAuditReport) { r.QdrantEventsEmitted = 1 }},
		{"qdrant mismatch", func(r *repairAuditReport) { r.QdrantMismatches = 1 }},
		{"document", func(r *repairAuditReport) { r.DocumentsRefreshed = 1 }},
		{"specscene", func(r *repairAuditReport) { r.SpecSceneRepaired = true }},
		{"upload warning", func(r *repairAuditReport) { r.Warnings = []string{"upload"} }},
		{"requested mutation", func(r *repairAuditReport) {
			r.RemoveInvalid = true
			r.Missing = 1
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := repairAuditReport{}
			tc.mutate(&report)
			if repairReportIsNoOp(report) {
				t.Fatalf("report with %s side effect was classified as no-op: %#v", tc.name, report)
			}
		})
	}
}

func TestRepairAuditReport_NoOpIsSerialized(t *testing.T) {
	raw, err := json.Marshal(repairAuditReport{JobID: "job-replay", NoOp: true})
	if err != nil {
		t.Fatalf("marshal no-op report: %v", err)
	}
	var decoded repairAuditReport
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal no-op report: %v", err)
	}
	if !decoded.NoOp || !repairReportIsNoOp(decoded) {
		t.Fatalf("no-op contract was not preserved: raw=%s decoded=%#v", raw, decoded)
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
