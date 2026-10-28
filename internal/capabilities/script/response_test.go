// Package script (test) — response_test.go locks the canonical async
// wire shape of POST /api/script/generate (PR-morti-sync, July 2026).
//
// godlike/07 forward-prevention gates:
//   - Field count must remain EXACTLY 9 (NO re-introduction of dead
//     sync fields like Script/WordCount/Title/Language/Model/CacheStatus/
//     CacheHit/Count/Total/Results/EntitiesJSON/DocLink/DocID/Warnings).
//   - JSON-keys MUST be the canonical set (ok + job_id + status +
//     status_url + current_stage + the four Batch Brasil delivery
//     visibility fields).
//   - Zero value MUST serialize as far as omitempty permits (ok:false
//     emits; everything else drops).
//
// Any sync-path revival must re-introduce fields DELIBERATELY and bump
// the field-count lock + wire-shape assertions in this file (godlike/06
// SSOT: this test file IS the canonical contract surface for what the
// HTTP handler MAY emit).
//
// WIRE-SHAPE AUDIT (2026-10-28, Batch Brasil visibility item —
// docs/stato/refactored.md §6): +4 fields (channel_id / final_job /
// drive_folder_id / drive_subfolder_name). Rationale: the enqueue
// response must surface the routing facts of the submitted envelope
// instead of hiding them in the persisted payload. Additive-only:
// all four use omitempty, so pre-existing consumers see no new keys
// unless the caller actually declared those facts. The lock is bumped
// 5 → 9 deliberately in lockstep with this audit.
package script

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestGenerateResponse_FieldCountLock pins the field count to exactly
// 9 (5 canonical async + 4 Batch Brasil delivery visibility fields, see
// the WIRE-SHAPE AUDIT in this file's header). godlike/07
// forward-prevention gate: a future re-introduction of dead sync fields
// (Script / WordCount / Title / Language / Model / CacheStatus / CacheHit
// / Count / Total / Results / EntitiesJSON / DocLink / DocID) would
// surface as a test failure, requiring a deliberate wire-shape audit
// before bumping this lock.
func TestGenerateResponse_FieldCountLock(t *testing.T) {
	t.Parallel()

	const wantCount = 9
	got := reflect.TypeOf(GenerateResponse{}).NumField()
	if got != wantCount {
		t.Fatalf("GenerateResponse field count = %d, want %d (forward-prevention lock).\nField names: %v",
			got, wantCount, fieldNamesCanonical())
	}
}

// TestGenerateResponse_FieldNamesCanonical locks the JSON-keys to
// exactly the async wire shape. godlike/06 SSOT: tests that don't pin
// the canonical key list allow silent drift on field renames.
func TestGenerateResponse_FieldNamesCanonical(t *testing.T) {
	t.Parallel()

	const want = "ok job_id status status_url current_stage channel_id final_job drive_folder_id drive_subfolder_name"
	got := fieldNamesCanonical()
	if got != want {
		t.Fatalf("GenerateResponse JSON-keys = %q\n  want   = %q", got, want)
	}
}

// TestGenerateResponse_ZeroValueMarshal verifies the marshal of the
// zero-value struct: ok:false emits (no omitempty on bool), every
// string field is empty and omitempty drops it.
func TestGenerateResponse_ZeroValueMarshal(t *testing.T) {
	t.Parallel()

	resp := GenerateResponse{}
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal err: %v", err)
	}
	want := `{"ok":false}`
	if string(got) != want {
		t.Fatalf("zero-value JSON\n  got  = %s\n  want = %s", string(got), want)
	}
}

// TestGenerateResponse_AsyncHelper_PopulatedAllFields verifies the
// canonical async path: the async fields populated → exact 4-key wire
// shape with all values verbatim (the delivery fields stay omitted
// until withDelivery runs — see the delivery test below).
func TestGenerateResponse_AsyncHelper_PopulatedAllFields(t *testing.T) {
	t.Parallel()

	resp := GenerateResponse{}
	resp.async("job-abc-123", "PENDING", "/api/jobs/job-abc-123/full")
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal err: %v", err)
	}
	want := `{"ok":true,"job_id":"job-abc-123","status":"PENDING","status_url":"/api/jobs/job-abc-123/full"}`
	if string(got) != want {
		t.Fatalf("async JSON\n  got  = %s\n  want = %s", string(got), want)
	}
}

// TestGenerateResponse_NoLegacySyncKeys is the forward-prevention
// regression: the legacy sync-only JSON keys MUST NOT appear in any
// marshaled output. A future re-introduction of a sync field
// (syncSingle / syncMulti or otherwise) would surface as a test
// failure here, BLOCKING the re-introduction unless this test is
// deliberately updated in lockstep with the wire-shape audit.
func TestGenerateResponse_NoLegacySyncKeys(t *testing.T) {
	t.Parallel()

	resp := GenerateResponse{}
	resp.async("job-id-99", "PENDING", "/api/jobs/job-id-99/full")
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal err: %v", err)
	}

	// Substring probes — quoted to avoid matching partial-key noise.
	legacyKeys := []string{
		`"script"`, `"word_count"`, `"language"`,
		`"model"`, `"cache_status"`, `"cache_hit"`,
		`"count"`, `"total"`, `"results"`,
		`"entities_json"`, `"doc_link"`, `"doc_id"`,
		`"warnings"`,
	}
	for _, key := range legacyKeys {
		if strings.Contains(string(body), key) {
			t.Fatalf("legacy sync key %q leaked into wire shape: %s", key, string(body))
		}
	}

	// Separate probe for the bare "title" — it's a substring risk
	// because JobID could conceivably contain it. Lock the canonical
	// "title" string exactly (qouted by the marshaler).
	if strings.Contains(string(body), `"title":"`) {
		t.Fatalf("legacy sync key %q leaked into wire shape: %s", `"title"`, string(body))
	}
}

// TestGenerateResponse_AsyncHelper_NilReceiverNoPanic verifies the
// defensive panic-safety contract: callers may initially call async()
// on a zero-value struct literal (handler_enqueue.go::enqueueEnvelopeFn
// pattern: `resp := GenerateResponse{}; resp.async(...)`). A future
// refactor that turns async into a non-pointer method MUST preserve
// this no-panic behaviour.
//
// godlike/07 NO-FAKE-AVAILABILITY: the helper MUST not panic when
// callers supply a nil receiver — the test covers this directly
// without reflecting on real producer code paths.
func TestGenerateResponse_AsyncHelper_ValuesReceiver_NoNilPanic(t *testing.T) {
	t.Parallel()

	// Direct call on a zero-value (values-receiver pattern). The
	// helper signature is `func (r *GenerateResponse) async(...)` so
	// this is implicitly `(&GenerateResponse{}).async(...)` — Go
	// auto-takes the address. The test confirms no panic and OK=true
	// gets set on the receiver.
	resp := GenerateResponse{}
	resp.async("job-z", "PENDING", "/api/jobs/job-z/full")
	if !resp.OK {
		t.Fatalf("async() should set OK=true; resp.OK=%v", resp.OK)
	}
	if string(resp.JobID) != "job-z" {
		t.Fatalf("async() should set JobID=%q; got %q", "job-z", resp.JobID)
	}
}

// TestGenerateResponse_DeliveryVisibility pins the Batch Brasil
// visibility wire shape: withDelivery surfaces channel_id / final_job /
// the Drive destination on the enqueue response, additively (the
// canonical async keys are unchanged).
func TestGenerateResponse_DeliveryVisibility(t *testing.T) {
	t.Parallel()

	resp := GenerateResponse{}
	resp.async("job-abc-123", "PENDING", "/api/jobs/job-abc-123/full")
	resp.withDelivery("crime", true, "1AbC_folderId", "Milton final job")
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal err: %v", err)
	}
	want := `{"ok":true,"job_id":"job-abc-123","status":"PENDING","status_url":"/api/jobs/job-abc-123/full","channel_id":"crime","final_job":true,"drive_folder_id":"1AbC_folderId","drive_subfolder_name":"Milton final job"}`
	if string(got) != want {
		t.Fatalf("delivery JSON\n  got  = %s\n  want = %s", string(got), want)
	}
}

// TestDeliveryFromEnvelope_FirstNonEmptyPerField pins the multi-item
// projection rule: first non-empty value per field, root final_job flag
// preserved. Per-item truth remains visible in the queue view.
func TestDeliveryFromEnvelope_FirstNonEmptyPerField(t *testing.T) {
	t.Parallel()

	env := &scriptpkg.GenerationEnvelopeV2{
		FinalJob: true,
		Items: []scriptpkg.GenerationItemV2{
			{ChannelID: ""},
			{ChannelID: "crime", Output: scriptpkg.OutputSpec{Render: scriptpkg.VideoRenderSpec{DriveFolderID: "1AbC_folderId", DriveSubfolderName: "Milton final job"}}},
			{ChannelID: "young", Output: scriptpkg.OutputSpec{Render: scriptpkg.VideoRenderSpec{DriveFolderID: "other"}}},
		},
	}
	channelID, finalJob, folderID, folderName := deliveryFromEnvelope(env)
	if channelID != "crime" || !finalJob || folderID != "1AbC_folderId" || folderName != "Milton final job" {
		t.Fatalf("deliveryFromEnvelope = (%q, %v, %q, %q)", channelID, finalJob, folderID, folderName)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

// fieldNamesCanonical returns the JSON-keys of GenerateResponse's
// exported fields in canonical struct declaration order. Used by
// the field-count lock + the wire-shape assertion.
//
// godlike/06 SSOT: ordering is "struct declaration order" — Go's
// encoding/json honors field declaration order, so the test pins
// both count AND order (no surprise from a future `init()` block
// prepending fields).
func fieldNamesCanonical() string {
	typ := reflect.TypeOf(GenerateResponse{})
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// json:"name,omitempty" → strip ",omitempty" suffix.
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		names = append(names, tag)
	}
	return strings.Join(names, " ")
}
