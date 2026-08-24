// Package stockpipeline — orchestrator_run_test.go (PR-007, July 2026).
//
// TDD coverage for the LLM enrichment plumbing-on-nil contract.
// The 6 fields (Category / Event / Round / Scene / Subject /
// Entities) on StockRunMetadata are wired into the wire shape
// NOW so future PRs can populate them after the LLM enrichment
// pass lands. For now, the fields stay at zero-value; omitempty
// drops them from the JSON payload.
//
// godlike/07 NO-FAKE-AVAILABILITY: the LLM enrichment pass is
// the SOLE authority on when to populate these fields. A
// placeholder string (e.g. Category:"unknown" or Event:"n/a")
// would be a fake-availability anti-pattern — it would emit
// meaningless data into the Qdrant BM25 channel and pollute
// downstream search results.
//
// Test scope: the test name references "RunOrchestratorResilient"
// because that is the orchestrator entry point that ultimately
// produces the metadata.json payload (via its publish/finalize
// steps which call writeAndHashMetadata → buildStockRunMetadata).
// The test directly exercises buildStockRunMetadata because it
// is the canonical pure function that constructs the
// StockRunMetadata wire shape; this is the godlike/06 SSOT
// surface for the metadata.json envelope. The test pins the
// contract that RunResilient's downstream steps inherit.
package reconcile

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRunOrchestratorResilient_LLMFieldsNil_DefaultsToOmitFromPayload
// pins the PR-007 godlike/07 NO-FAKE-AVAILABILITY contract:
//   - The 6 LLM enrichment fields on StockRunMetadata stay at
//     zero-value (nil / empty) until the LLM enrichment pass lands.
//   - omitempty drops the zero-value fields from the JSON payload.
//   - The JSON wire shape does NOT contain any of the 6 LLM keys.
//
// Failure modes this test catches (regression guards):
//   - A future refactor that drops the omitempty tag (would leak
//     empty strings/zero-ints/nil-slices into the JSON).
//   - A future refactor that adds a placeholder default like
//     Category:"unknown" (would emit fake-availability data).
//   - A future refactor that wires the LLM pass prematurely
//     (would emit real LLM data on a wire the consumers do not
//     yet expect).
//   - A future refactor that flips Entities from nil to an empty
//     slice (omitempty still drops both, but the contract check
//     is loose enough to permit it).
func TestRunOrchestratorResilient_LLMFieldsNil_DefaultsToOmitFromPayload(t *testing.T) {
	// Build a minimal RunInput + chunks + fingerprint. The
	// buildStockRunMetadata helper is the canonical production
	// function that RunResilient's publish/finalize steps call
	// (via writeAndHashMetadata) to construct the metadata.json
	// envelope. Per godlike/06 SSOT, this is the SOLE wire-shape
	// construction site for the per-run metadata.
	in := &RunInput{
		FolderID:      "wf-pr007",
		FolderName:    "pr007-folder",
		DirectURLs:    []string{"https://example.com/source.mp4"},
		TotalMinutes:  1,
		ChunkDuration: 5,
		ClipDuration:  5,
		// LLM enrichment is NOT populated — forward-pointer to the
		// downstream LLM enrichment pass. Per PR-007 contract the
		// fields stay at zero-value.
	}
	chunks := []ChunkState{
		{
			Index:      0,
			ArtifactID: "stock:fp:c0",
			Filename:   "stock_fp_0.mp4",
			SHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SizeBytes:  1024,
		},
	}
	fp := "fp-pr007"

	meta := buildStockRunMetadata(in, chunks, fp)

	// ── 1. godlike/06 SSOT — the 6 LLM fields exist on the struct ──
	// (compile-time check; the struct field declarations are the
	// canonical surface). Runtime check: all 6 stay at zero-value
	// per the plumbing-on-nil contract.
	if got := meta.Category; got != "" {
		t.Errorf("Category = %q, want \"\" (plumbing-on-nil must keep zero-value)", got)
	}
	if got := meta.Event; got != "" {
		t.Errorf("Event = %q, want \"\" (plumbing-on-nil must keep zero-value)", got)
	}
	if got := meta.Round; got != 0 {
		t.Errorf("Round = %d, want 0 (plumbing-on-nil must keep zero-value)", got)
	}
	if got := meta.Scene; got != "" {
		t.Errorf("Scene = %q, want \"\" (plumbing-on-nil must keep zero-value)", got)
	}
	if got := meta.Subject; got != "" {
		t.Errorf("Subject = %q, want \"\" (plumbing-on-nil must keep zero-value)", got)
	}
	// godlike/07 minimum-blast-radius: accept BOTH nil and empty
	// slice (omitempty drops both; the contract is "no entries",
	// not "specifically nil"). A future refactor that returns
	// make([]string, 0) instead of nil is still valid.
	if got := len(meta.Entities); got != 0 {
		t.Errorf("len(Entities) = %d, want 0 (plumbing-on-nil must keep zero-value)", got)
	}

	// ── 2. godlike/07 NO-FAKE-AVAILABILITY — JSON omits the 6 keys ──
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(StockRunMetadata) failed: %v", err)
	}
	jsonStr := string(raw)

	// Each LLM key must be ABSENT from the JSON payload. omitempty
	// drops empty string / zero int / nil slice. The substring
	// check is intentionally strict — the test must FAIL the
	// moment any of the 6 keys leaks into the wire (whether as a
	// real value, a placeholder "unknown", or an empty string).
	forbiddenSubstrings := []string{
		`"category"`,
		`"event"`,
		`"round"`,
		`"scene"`,
		`"subject"`,
		`"entities"`,
	}
	for _, sub := range forbiddenSubstrings {
		if strings.Contains(jsonStr, sub) {
			t.Errorf("JSON payload must NOT contain key %q when LLM fields are nil/empty (godlike/07 NO-FAKE-AVAILABILITY: omitempty must drop zero-value); got JSON: %s", sub, jsonStr)
		}
	}

	// ── 3. Sanity: the canonical per-run fields ARE present ──
	// (regression guard: confirms the JSON is non-empty and the
	// non-LLM fields are not accidentally dropped by a refactor).
	mustHaveSubstrings := []string{
		`"job_id"`,
		`"run_fingerprint"`,
		`"workflow_id"`,
		`"created_at"`,
		`"policy_version"`,
		`"chunks"`,
	}
	for _, sub := range mustHaveSubstrings {
		if !strings.Contains(jsonStr, sub) {
			t.Errorf("JSON payload must contain canonical key %q; got JSON: %s", sub, jsonStr)
		}
	}
}
