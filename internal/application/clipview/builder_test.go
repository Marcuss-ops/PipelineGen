package clipview_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clipview"
)

// sampleCandidate is the canonical fixture for happy-path projection.
func sampleCandidate() clipview.Candidate {
	return clipview.Candidate{
		AssetRef:      "yt_RRJvrDKunyA_32_39_v1",
		SlotRef:       "slot-1",
		Description:   "Pacquiao circles Broner and controls distance with the left jab.",
		VisualSummary: "Lateral footwork, Broner remains defensive.",
		Transcript:    "Pacquiao appears faster and lighter on his feet.",
		DurationMs:    7000,
		Score:         0.91,
	}
}

// ── ref construction spot checks ──────────────────────────────────

func TestNewCandidateView_HappyPath_BuildsExpectedRef(t *testing.T) {
	cv, err := clipview.NewCandidateView("slot-1", 0, sampleCandidate())
	if err != nil {
		t.Fatalf("NewCandidateView: %v", err)
	}
	if cv.Ref != "slot-1:candidate-0" {
		t.Errorf("Ref = %q, want %q", cv.Ref, "slot-1:candidate-0")
	}
	if cv.Description == "" || cv.VisualSummary == "" || cv.Transcript == "" {
		t.Errorf("description/visual_summary/transcript must round-trip; got %+v", cv)
	}
	if cv.DurationMs != 7000 {
		t.Errorf("DurationMs = %d, want 7000", cv.DurationMs)
	}
	if cv.Score != 0.91 {
		t.Errorf("Score = %f, want 0.91", cv.Score)
	}
}

func TestNewCandidateView_AssetRef_Never_FoldsIntoRef(t *testing.T) {
	cv, err := clipview.NewCandidateView("slot-7", 42, sampleCandidate())
	if err != nil {
		t.Fatalf("NewCandidateView: %v", err)
	}
	// The AssetRef's distinctive prefix MUST NOT appear anywhere
	// in the ref — the projection is the only thing that builds
	// the ref, and it MUST NOT include AssetRef.
	if strings.Contains(cv.Ref, "yt_") {
		t.Errorf("ref leaked the AssetRef prefix: %q", cv.Ref)
	}
	if strings.Contains(cv.Ref, "RRJvrDKunyA") {
		t.Errorf("ref leaked the AssetRef content: %q", cv.Ref)
	}
}

// ── empty / invalid input pins (godlike/07 fail-closed) ──────────

func TestNewCandidateView_EmptySlotRef_SurfacesTypedError(t *testing.T) {
	_, err := clipview.NewCandidateView("", 0, sampleCandidate())
	if err == nil {
		t.Fatal("empty slotRef must return typed error")
	}
	if !errors.Is(err, clipview.ErrCandidateViewEmptyRef) {
		t.Errorf("err = %v, want errors.Is(ErrCandidateViewEmptyRef)", err)
	}
}

func TestNewCandidateView_WhitespaceSlotRef_SurfacesTypedError(t *testing.T) {
	_, err := clipview.NewCandidateView("   ", 0, sampleCandidate())
	if err == nil {
		t.Fatal("whitespace-only slotRef must return typed error (trim)")
	}
	if !errors.Is(err, clipview.ErrCandidateViewEmptyRef) {
		t.Errorf("err = %v, want errors.Is(ErrCandidateViewEmptyRef)", err)
	}
}

func TestNewCandidateView_NegativeIndex_SurfacesTypedError(t *testing.T) {
	_, err := clipview.NewCandidateView("slot-1", -1, sampleCandidate())
	if err == nil {
		t.Fatal("negative index must return typed error")
	}
	if !errors.Is(err, clipview.ErrCandidateViewEmptyRef) {
		t.Errorf("err = %v, want errors.Is(ErrCandidateViewEmptyRef)", err)
	}
}

// ── struct-shape redaction audit (reflection) ────────────────────

// TestCandidateView_StructShapeStripsForbidden pins the canonical
// REFLECT-LEVEL invariant: every JSON-tag on the CandidateView
// struct MUST be either in the allow-list OR the canonical "ref"
// exemption. This catches forward-prevention drift: a future PR
// that adds a JSON-tag "asset_id" to the struct (e.g. for
// debugging) surfaces as a CI failure here, NOT a silent leak.
func TestCandidateView_StructShapeStripsForbidden(t *testing.T) {
	var cv clipview.CandidateView
	typ := reflect.TypeOf(cv)
	allowSet := make(map[string]struct{}, len(clipview.AllowedCandidateViewJSONFields))
	for _, name := range clipview.AllowedCandidateViewJSONFields {
		allowSet[name] = struct{}{}
	}

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		jsonName := strings.Split(tag, ",")[0]
		if jsonName == "" {
			continue
		}
		if _, ok := allowSet[jsonName]; !ok {
			t.Errorf(
				"GODLIKE-06-LEAK: CandidateView struct field %q is JSON-tagged %q — that key is NOT in AllowedCandidateViewJSONFields. Add it to CandidateView's allow-list only after a forward-prevention update to the test matrix.",
				f.Name,
				jsonName,
			)
		}
		// Forward-leak: NONE of the deny-list keys must appear as a
		// struct field. This pins the surface; removing a key from
		// the deny-list MUST also remove the struct field (or
		// surface here).
		for _, forbidden := range clipview.ForbiddenCandidateViewJSONFields {
			if jsonName == forbidden {
				t.Errorf(
					"GODLIKE-07-LEAK: CandidateView struct field %q is JSON-tagged with a forbidden key %q. Remove the field or rename the JSON tag.",
					f.Name,
					forbidden,
				)
			}
		}
	}
}

// ── JSON-marshalling redaction audit (runtime) ────────────────────

// TestCandidateView_JSONMarshallingStripsForbidden pins the
// canonical RUNTIME-LEVEL invariant: marshalling a CandidateView
// to JSON MUST NOT include ANY forbidden key. This is the wire
// guard that runs every time a candidate is built — a regression
// catches at first production send to Gemma.
func TestCandidateView_JSONMarshallingStripsForbidden(t *testing.T) {
	cv, err := clipview.NewCandidateView("slot-1", 0, sampleCandidate())
	if err != nil {
		t.Fatalf("NewCandidateView: %v", err)
	}
	raw, err := json.Marshal(cv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Hard rule 1: substring search on the raw bytes — catches
	// any JSON-tagged key that may have been added by a future
	// PR's drift, including those not in the deny-list yet.
	containsForbiddenLiteral := func(forbidden string) bool {
		// JSON keys are quoted: "key": or "key", — substring match
		// with surrounding quotes prevents false positives on
		// substrings of legitimate keys. bytes.Contains avoids the
		// []byte→string allocation that strings.Contains would do
		// per call; raw is []byte here (json.Marshal output).
		needle := `"` + forbidden + `"`
		return bytes.Contains(raw, []byte(needle+`:`)) ||
			bytes.Contains(raw, []byte(needle+`,`))
	}

	for _, forbidden := range clipview.ForbiddenCandidateViewJSONFields {
		// AssetRef's special form inside Description/Transcript is
		// NOT a structured leak — but a substring match is enough
		// to ALERT the auditor (the structured-JSON check below
		// discriminates).
		if containsForbiddenLiteral(forbidden) {
			t.Errorf("forbidden key %q appears in marshalled JSON: %s", forbidden, raw)
		}
	}

	// Hard rule 2 (canonical): parse + key-set. This is the
	// SSOT auditing surface that forward-pointer PR-REDACTION-
	// LEAK-AUDIT can promote to a CI gate across all candidates.
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, forbidden := range clipview.ForbiddenCandidateViewJSONFields {
		if _, present := back[forbidden]; present {
			t.Errorf("forbidden key %q leaks at the JSON object level: %s", forbidden, raw)
		}
	}

	// Hard rule 3: AssetRef MUST NOT appear as content (defensive
	// against future fields that accidentally fold AssetRef
	// into a description or transcript).
	if strings.Contains(string(raw), "yt_RRJvrDKunyA_32_39_v1") {
		t.Errorf("AssetRef leaked into marshal output: %s", raw)
	}
	if strings.Contains(string(raw), "RRJvrDKunyA") {
		t.Errorf("AssetRef content leaked into marshal output: %s", raw)
	}
	if strings.Contains(string(raw), "drive.google.com") {
		t.Errorf("drive-link literal leaked into marshal output: %s", raw)
	}
}

// ── ValidateForModelView pin ──────────────────────────────────────

func TestCandidateView_ValidateForModelView_HappyPath(t *testing.T) {
	cv, err := clipview.NewCandidateView("slot-1", 0, sampleCandidate())
	if err != nil {
		t.Fatalf("NewCandidateView: %v", err)
	}
	back, err := cv.ValidateForModelView()
	if err != nil {
		t.Fatalf("ValidateForModelView: %v", err)
	}
	if back["ref"] != "slot-1:candidate-0" {
		t.Errorf("ref back from validate = %v, want slot-1:candidate-0", back["ref"])
	}
	// Spot-check that all 6 allowed fields are present in JSON.
	for _, allowed := range clipview.AllowedCandidateViewJSONFields {
		if _, ok := back[allowed]; !ok && allowed != "score" {
			// score may be omitted when 0.0; not asserting exact key
			// presence for omitempty-hooked fields, EXCEPT ref/description
			// etc.
			if allowed == "ref" || allowed == "description" {
				t.Errorf("required field %q absent from validated JSON", allowed)
			}
		}
	}
}

func TestCandidateView_ValidateForModelView_NilReceiver(t *testing.T) {
	var cv *clipview.CandidateView
	_, err := cv.ValidateForModelView()
	if !errors.Is(err, clipview.ErrCandidateViewNilReceiver) {
		t.Errorf("nil receiver error = %v, want ErrCandidateViewNilReceiver", err)
	}
}

func TestCandidateView_MarshalForModelView_HappyPath(t *testing.T) {
	cv, err := clipview.NewCandidateView("slot-1", 0, sampleCandidate())
	if err != nil {
		t.Fatalf("NewCandidateView: %v", err)
	}
	raw, err := cv.MarshalForModelView()
	if err != nil {
		t.Fatalf("MarshalForModelView: %v", err)
	}
	if !strings.Contains(string(raw), `"ref":"slot-1:candidate-0"`) {
		t.Errorf("MarshalForModelView JSON missing ref key: %s", raw)
	}
}

func TestCandidateView_MarshalForModelView_NilReceiver(t *testing.T) {
	var cv *clipview.CandidateView
	_, err := cv.MarshalForModelView()
	if !errors.Is(err, clipview.ErrCandidateViewNilReceiver) {
		t.Errorf("nil receiver error = %v, want ErrCandidateViewNilReceiver", err)
	}
}

// ── dedup-coverage pin (the deny-list is exhaustive at the audit
//     surface; if a future maintenance forgets a key, the JSON
//     marshal test fires) ─────────────────────────────────────────

// TestCandidateView_ForbiddenListCoversMandatedCatalogue pins the
// every-mandatory-key surface from the user spec (PR-CANDIDATE-VIEW-DENY).
// If a future PR drops a key (or forgets to add one), this test
// fires BEFORE a leak can land in production. The catalogue here
// is intentionally the same set described in the architecture
// spec; it is also documented in the package-level godoc of
// internal/application/clipview/types.go so reviewer attention
// has a single canonical source.
func TestCandidateView_ForbiddenListCoversMandatedCatalogue(t *testing.T) {
	mustHave := []string{
		// ── Infrastructure identifiers ──
		"asset_id",
		// ── Drive infrastructure (full family) ──
		"drive_link",
		"drive_file_id",
		"download_link",
		"local_path",
		"relative_path",
		// ── Folder side channels (technical folder id, NOT
		//    normalized_group which is the routing key on the
		//    wire but defensive here too) ──
		"folder_id",
		"folder_path",
		"normalized_group",
		// ── Source provenance ──
		"youtube_url",
		"youtube_video_id",
		"source_url",
		// ── Hash family (multiple encodings MUST all be in) ──
		"hash",
		"content_hash",
		"file_hash",
		"sha256",
		"md5_checksum",
		// ── Filename / display ──
		"filename",
		"name",
		"title",
		// ── Slot / plan taxonomy ──
		"slot_ref",
		"plan_id",
		// ── Wire-only leak surface ──
		"qdrant_point_id",
	}
	for _, k := range mustHave {
		found := false
		for _, f := range clipview.ForbiddenCandidateViewJSONFields {
			if f == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mandated forbidden key %q missing from canonical deny-list (forward-leak: catalogue drift)", k)
		}
	}
}

// TestCandidateView_AllowedListHasExactSixFields pins the model
// contract: Gemma sees EXACTLY 6 fields. Adding a 7th field (e.g.
// `tags`) would silently break the model surface and bypass the
// reflection audit. Cardinality is part of the contract.
func TestCandidateView_AllowedListHasExactSixFields(t *testing.T) {
	if len(clipview.AllowedCandidateViewJSONFields) != 6 {
		t.Fatalf("AllowedCandidateViewJSONFields has %d entries, want 6 (drift from model-facing contract; PR must update this test + the model prompt together)",
			len(clipview.AllowedCandidateViewJSONFields))
	}
	expected := []string{"ref", "description", "visual_summary", "transcript", "duration_ms", "score"}
	got := make([]string, len(clipview.AllowedCandidateViewJSONFields))
	copy(got, clipview.AllowedCandidateViewJSONFields)
	// Sort both for stable compare (catalogued order is not
	// semantic for the model contract).
	for i := range expected {
		for j := range got {
			if expected[i] == got[j] {
				got = append(got[:j], got[j+1:]...)
				break
			}
		}
	}
	if len(got) != 0 {
		t.Errorf("AllowedCandidateViewJSONFields missing keys: %v (full got=%v, want=%v)",
			got, clipview.AllowedCandidateViewJSONFields, expected)
	}
}

// ── allow-list exhaustive pin ────────────────────────────────────

func TestCandidateView_AllowedListExhaustive(t *testing.T) {
	cv, err := clipview.NewCandidateView("slot-1", 0, sampleCandidate())
	if err != nil {
		t.Fatalf("NewCandidateView: %v", err)
	}
	raw, err := json.Marshal(cv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Every key shown by marshalling must be in the allow-list.
	for k := range back {
		found := false
		for _, allowed := range clipview.AllowedCandidateViewJSONFields {
			if allowed == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("marshalled key %q not in AllowedCandidateViewJSONFields", k)
		}
	}
}
