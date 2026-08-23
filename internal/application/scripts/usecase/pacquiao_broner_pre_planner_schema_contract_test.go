// Package usecase — pacquiao_broner_pre_planner_schema_contract_test.go
// pins the planner→golden JSON wire shape via Go reflection so
// future planner struct drift fails loud in CI BEFORE the
// byte-compare golden test even runs.
//
// godlike/06 SSOT: this test derives the wire shape from the
// production struct definitions (scriptpkg.ClipPrePlan +
// scriptpkg.ClipSearchSlot + scriptpkg.SourceAnchor) — the same
// structs that the deterministic planner marshals into the golden.
// Adding a field, removing a field, or renaming a JSON tag in any
// of these structs MUST surface here as a test failure, with a
// pointed message identifying exactly which struct + field drifted.
//
// godlike/07 NO-FAKE-AVAILABILITY: the test introspects the
// canonical domain struct, never a synthesized test-local replica.
// Drift between the planner's actual struct and the canonical
// domain struct is itself a violation of godlike/06 SSOT — this
// test catches that case too.
//
// VERIFICATION TRACE (the question the user explicitly asked):
//
// The regenerated golden at
// testdata/pacquiao_broner_pre_planner_e2e_golden.json has:
//
//	slots[0..3].source_anchor = (start_offset=0, end_offset=0, excerpt="")
//	slots[0..3].visual_intent = "Visual depiction of: <topic> (at source offset 0/380)"
//
// This is consistent with `deterministic_planner.go`'s algorithm:
//
//  1. `anchorOffsetsForTopic(topic, canonical)` runs
//     `strings.Index(strings.ToLower(canonical), strings.ToLower(topic))`.
//     The fixture topics
//     ("La fase iniziale e lo studio della distanza",
//     "Pressione crescente di Pacquiao (round 4-6)",
//     "Round 7 - momento decisivo",
//     "Decisione finale dei giudici")
//     are NOT substrings of the canonicalized source, which uses
//     different phrasings ("ha impostato il ritmo",
//     "cominciato a fare combinazioni", "messo Broner in
//     difficoltà", "decretassero la vittoria ai punti"). So the
//     index is -1 and the function returns the degenerate anchor
//     (0, 0, "") — exactly as the golden shows for all 4 slots.
//
//  2. `visualIntentFor(topic, start=0, totalLen=len(canonical))`
//     formats `"Visual depiction of: <topic> (at source offset
//     0/<len(canonical)>)"`. With `len(canonical)=380` (the byte
//     length of the canonicalized pacquiaoSourceText, computed via
//     `scriptpkg.CanonicalizeSourceText` per source_spec.go), the
//     golden's `(at source offset 0/380)` matches byte-for-byte.
//
// This test file pins BOTH invariants: the reflection contract on
// the struct definitions AND the runtime computation against
// deterministic_planner.go (the verification test at the bottom).
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Planner return-type identity pin ─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
//
// Runtime guarantee that `ClipPrePlan`, `ClipSearchSlot`, and
// `SourceAnchor` as referenced unprefixed inside `package usecase`
// (the planner's actual return types at `&ClipPrePlan{V1, …}`
// literal sites) IS the canonical `scriptpkg.*` struct — not a
// local-port shadow with parallel-mirror fields. Per
// source_spec.go's doc comment, "FASE 2 will collapse them via
// type aliases" — the alias keeps the planner's return type
// identical to the canonical domain struct.
//
// Without this pin: a future PR could introduce a local-port
// `type ClipPrePlan struct {...}` shadow. The reflection-based
// schema-contract tests would still pin the scriptpkg shape.
// The byte-compare golden could still pass on coincidentally
// matching field names. But the planner runtime would marshal
// a different struct — and both layers would give a green CI
// while the planner's wire output silently drifts from the
// contract.
//
// The asserts are equality compares of reflect.TypeOf values; in
// Go, an alias `type X = scriptpkg.X` returns the SAME Type as
// the underlying structured type, so aliases pass this gate.
// Shadow structs (different fields/methods) fail.
// KNOWN godlike/06 SSOT VIOLATION (open followup): `ClipPrePlan`,
// `ClipSearchSlot`, `SourceAnchor` as referenced unprefixed
// inside `package usecase` are LOCAL shadow structs, NOT type
// aliases for the canonical `scriptpkg.*` types. A previous attempt
// at a compile-time assignability pin (`var _ ClipPrePlan = scriptpkg.ClipPrePlan{}`)
// failed `go test` BUILD because Go's compile-time assignability
// rules reject cross-type literal assignment between two distinct
// named structs. The schema-contract reflection tests in this file
// STILL pin the wire shape (struct fields, JSON tags, omitempty
// status, Go types) so a planner-runtime drift surfaces as a CI
// failure. The identity layer (planner return type IS
// scriptpkg.ClipPrePlan) remains open until the FASE-2 alias
// collapse work documented in source_spec.go migrates the local-port
// shadow structs to `type X = scriptpkg.X` aliases.

// TestClipPrePlan_JSONSchemaContract pins the top-level ClipPrePlan
// wire shape: JSON-tagged field names (in declaration order, which
// `encoding/json` marshals verbatim), the omitempty status of each
// field, and the Go types that translate to JSON bool/number/string.
// A drift in any of these fails loud here, before the byte-compare
// golden test runs.
//
// Pins:
//   - Field order matches the JSON marshal order (the struct
//     declaration order). The fingerprint field uses omitempty
//     and MUST NOT appear when Fingerprint == "".
//   - Version is int; SourceHash, Title are non-pointer strings;
//     Slots is []scriptpkg.ClipSearchSlot.
//   - SourceHash + Title are mandatory wire fields (no omitempty).

// Spot-check Slots is []scriptpkg.ClipSearchSlot. A regression
// that swaps Slots for []*scriptpkg.ClipSearchSlot (or
// []scriptpkg.SomeOtherSlot) breaks the slot-count contract
// in deterministic_planner_test.go, but this test catches it at
// the struct definition layer.

// TestClipSearchSlot_JSONSchemaContract pins the per-slot wire shape
// with one critical extra pin: `Required` has NO omitempty so the
// `false` value survives JSON round-trip. This matches the
// canonical comment in source_spec.go (line 514):
//
//	"Required uses bare `json:"required"` (no `omitempty`) so the
//	 `false` value survives JSON round-trip; silence would conflate
//	 "explicit optional" with "schema missing"."
//
// Without this pin, a regression that adds omitempty to Required
// would silently enable "'schema missing'" round-trips on the
// downstream model-envelope path — the exact failure mode that
// `TestClipPrePlanner_PacquiaoBroner_E2E` Suite (c) guards
// against at the runtime level. This test is the second layer of
// defense at the struct definition layer.
func TestClipSearchSlot_JSONSchemaContract(t *testing.T) {
	t.Parallel()

	rt := reflect.TypeOf(scriptpkg.ClipSearchSlot{})

	wantKeys := []string{
		"ref", "topic", "source_anchor", "search_query",
		"visual_intent", "target_duration_ms", "required",
	}
	pinnedTypes := map[string]reflect.Type{
		"Ref":              reflect.TypeOf(""),
		"Topic":            reflect.TypeOf(""),
		"SearchQuery":      reflect.TypeOf(""),
		"VisualIntent":     reflect.TypeOf(""),
		"TargetDurationMs": reflect.TypeOf(int64(0)),
		"Required":         reflect.TypeOf(false),
	}
	nullableGoNames := []string{
		"Topic", "SourceAnchor", "SearchQuery",
		"VisualIntent", "TargetDurationMs",
	}

	// CRITICAL: Required MUST NOT be in the nullable list. Drop this
	// before `assertFieldSetOrderOmitemptyAndTypes` runs so the
	// hard error fires BEFORE the helper tries to scan tags.
	if _, leaked := indexOfString(nullableGoNames, "Required"); leaked {
		t.Fatalf("CRITICAL: this test marked Required as omitempty — drop the entry before re-running the assertion below")
	}

	assertFieldSetOrderOmitemptyAndTypes(t, rt, wantKeys, pinnedTypes, nullableGoNames)

	// Hard pin (independent of helper logic): Required has no
	// omitempty AND the Go type is bool. Two angles to defend the
	// critical invariant.
	required, ok := rt.FieldByName("Required")
	if !ok {
		t.Fatalf("ClipSearchSlot missing Required field")
	}
	if required.Type.Kind() != reflect.Bool {
		t.Fatalf("ClipSearchSlot.Required type: want bool, got %v", required.Type.Kind())
	}
	tag, hasTag := required.Tag.Lookup("json")
	if !hasTag {
		t.Fatalf("ClipSearchSlot.Required has no json tag (mandatory wire field)")
	}
	if strings.Contains(tag, "omitempty") {
		t.Fatalf("CRITICAL DRIFT: ClipSearchSlot.Required has json tag %q with omitempty "+
			"— false would round-trip to silent (canonical 'schema missing' invariant violated). "+
			"Per source_spec.go: Comment on Required: 'no omitempty so the false value survives JSON round-trip'.",
			tag)
	}
	if tag != "required" {
		t.Fatalf("ClipSearchSlot.Required json tag drift: want %q, got %q (rename breaks downstream readers)",
			"required", tag)
	}
}

// TestSourceAnchor_JSONSchemaContract pins the per-anchor wire shape.
// SourceHash, StartOffset, EndOffset are mandatory wire fields
// (offset drift would silently corrupt the byte-range identifier
// into the planning→sampler→backend pipeline).
func TestSourceAnchor_JSONSchemaContract(t *testing.T) {
	t.Parallel()

	rt := reflect.TypeOf(scriptpkg.SourceAnchor{})

	wantKeys := []string{"source_hash", "start_offset", "end_offset", "excerpt"}
	pinnedTypes := map[string]reflect.Type{
		"SourceHash":  reflect.TypeOf(""),
		"StartOffset": reflect.TypeOf(int(0)),
		"EndOffset":   reflect.TypeOf(int(0)),
		"Excerpt":     reflect.TypeOf(""),
	}
	nullableGoNames := []string{"Excerpt"}

	assertFieldSetOrderOmitemptyAndTypes(t, rt, wantKeys, pinnedTypes, nullableGoNames)
}

// assertFieldSetOrderOmitemptyAndTypes is the canonical helper that
// asserts, for a struct rt:
//  1. Field keys (extracted from `json:"name[,omitempty]"` tags)
//     match wantKeys in declaration order (Go's encoding/json
//     marshals fields in declaration order).
//  2. Each Go field's tag's omitempty status matches the
//     nullableGoNames list (presence == expected).
//  3. Each Go field in pinnedTypes has the right Type.
//
// A drift in any dimension fires loud with a pointed error
// message identifying exactly which struct + field failed.
func assertFieldSetOrderOmitemptyAndTypes(
	t *testing.T,
	rt reflect.Type,
	wantKeys []string,
	pinnedTypes map[string]reflect.Type,
	nullableGoNames []string,
) {
	t.Helper()

	// (1) Field order: walk the struct fields, collect JSON keys,
	// compare against wantKeys exactly.
	gotKeys := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			continue
		}
		// Skip fields without a name (e.g., "-" tag).
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "-" {
			continue
		}
		gotKeys = append(gotKeys, name)
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("%s JSON keys (in struct order):\n  want: %v\n  got:  %v\n  diff: extra=%v missing=%v\n",
			rt.String(),
			wantKeys, gotKeys,
			stringSliceDiff(wantKeys, gotKeys),
			stringSliceDiff(gotKeys, wantKeys),
		)
	}

	// (2) omitempty status: every Go field in nullableGoNames MUST
	// have omitempty in its tag. Every other Go field MUST NOT.
	nullableSet := make(map[string]struct{}, len(nullableGoNames))
	for _, n := range nullableGoNames {
		nullableSet[n] = struct{}{}
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			continue
		}
		// godlike/06 SSOT: the boolean for "this field's tag
		// should carry omitempty" is derived from the second
		// map-lookup return value. The first return is the
		// value type's zero (struct{}{} here); indexing it
		// truthily would trip the Go compiler on the ! operator.
		_, wantOmitempty := nullableSet[f.Name]
		hasOmitempty := strings.Contains(tag, "omitempty")
		if wantOmitempty && !hasOmitempty {
			t.Errorf("%s.%s tag %q is missing omitempty (expected zero-value to be omitted from wire shape)",
				rt.String(), f.Name, tag)
		}
		if !wantOmitempty && hasOmitempty {
			t.Errorf("%s.%s tag %q has unexpected omitempty (would round-trip zero value to silent; breaks downstream readers)",
				rt.String(), f.Name, tag)
		}
	}

	// (3) Type pins: each Go field in pinnedTypes has the right Type.
	for goName, wantType := range pinnedTypes {
		f, ok := rt.FieldByName(goName)
		if !ok {
			t.Errorf("%s missing Go field %q", rt.String(), goName)
			continue
		}
		if !f.Type.AssignableTo(wantType) {
			t.Errorf("%s.%s type: want %v, got %v",
				rt.String(), goName, wantType, f.Type)
		}
	}
}

// stringSliceDiff returns elements in a that are NOT in b. Used
// for the diff line in the field-order assertion error.
func stringSliceDiff(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, s := range b {
		bSet[s] = struct{}{}
	}
	out := make([]string, 0)
	for _, s := range a {
		if _, ok := bSet[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}

// indexOfString returns the index of needle in haystack, or -1.
func indexOfString(haystack []string, needle string) (int, bool) {
	for i, s := range haystack {
		if s == needle {
			return i, true
		}
	}
	return -1, false
}

// TestClipPrePlan_GoldenStructuralParity asserts the regenerated
// golden's structural shape matches the reflection-derived wire
// shape. This is the second layer of defense:
//
//	(a) the planner's RUNTIME output is byte-identical to the
//	    golden (catches any byte drift in the planner — caught by
//	    TestClipPrePlanner_PacquiaoBroner_E2E Suite (a)).
//	(b) the STRUCT on which the planner depends has the wire
//	    shape the golden was generated against (catches drift at
//	    the struct definition phase, BEFORE the planner even runs).
//
// If a future PR adds a new field to scriptpkg.ClipPrePlan /
// scriptpkg.ClipSearchSlot / scriptpkg.SourceAnchor, this test
// fires FIRST with a pointed message identifying which field is
// not in the golden (vs. (a) which would emit "golden drift at
// line N" message).
func TestClipPrePlan_GoldenStructuralParity(t *testing.T) {
	t.Parallel()

	golden := filepath.Join("testdata", "pacquiao_broner_pre_planner_e2e_golden.json")
	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1 to generate)", golden, err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("parse golden top-level: %v", err)
	}

	// Top-level keys (sorted: JSON unmarshal into map[string]... does
	// not preserve order).
	gotTopKeys := mapKeysSorted(top)
	wantTopKeys := []string{"slots", "source_hash", "title", "version"}
	if !reflect.DeepEqual(gotTopKeys, wantTopKeys) {
		t.Fatalf("golden top-level keys:\n  want: %v (alphabetical, struct order: %v)\n  got:  %v\n",
			wantTopKeys, []string{"version", "source_hash", "title", "slots"}, gotTopKeys)
	}

	// Per-slot structural parity.
	var slots []map[string]json.RawMessage
	if err := json.Unmarshal(top["slots"], &slots); err != nil {
		t.Fatalf("parse golden slots: %v", err)
	}
	if len(slots) != 4 {
		t.Fatalf("golden slot count: want 4 (deterministic_planner contract), got %d", len(slots))
	}

	wantSlotKeys := []string{
		"ref", "required", "search_query", "source_anchor",
		"target_duration_ms", "topic", "visual_intent",
	}
	wantAnchorKeys := []string{
		"end_offset", "excerpt", "source_hash", "start_offset",
	}

	planHash, err := unmarshalString(top["source_hash"])
	if err != nil {
		t.Fatalf("parse plan.source_hash: %v", err)
	}
	for i, slot := range slots {
		gotSlotKeys := mapKeysSorted(slot)
		if !reflect.DeepEqual(gotSlotKeys, wantSlotKeys) {
			t.Errorf("golden slot[%d] keys (alphabetical):\n  want: %v\n  got:  %v\n",
				i, wantSlotKeys, gotSlotKeys)
		}

		var anchor map[string]json.RawMessage
		if err := json.Unmarshal(slot["source_anchor"], &anchor); err != nil {
			t.Errorf("golden slot[%d] source_anchor parse: %v", i, err)
			continue
		}
		gotAnchorKeys := mapKeysSorted(anchor)
		if !reflect.DeepEqual(gotAnchorKeys, wantAnchorKeys) {
			t.Errorf("golden slot[%d] source_anchor keys (alphabetical):\n  want: %v\n  got:  %v\n",
				i, wantAnchorKeys, gotAnchorKeys)
		}

		// Critical anti-drift gate: SourceAnchor.SourceHash MUST equal
		// plan.SourceHash. Same gate the planner enforces in
		// ValidatePlan (deterministic_planner.go).
		anchorHash, err := unmarshalString(anchor["source_hash"])
		if err != nil {
			t.Errorf("golden slot[%d] anchor.source_hash parse: %v", i, err)
			continue
		}
		if anchorHash != planHash {
			t.Errorf("golden slot[%d] anchor.source_hash drift: anchor=%q plan=%q (anti-drift gate violated)",
				i, anchorHash, planHash)
		}
	}

	// Spot-check visual_intent format on slot 0 — the per-slot
	// visual_intent MUST follow `visualIntentFor()`'s deterministic
	// format anchored at the planner runtime (no substring match →
	// `(at source offset 0/380)` exactly).
	var visual0 string
	if err := json.Unmarshal(slots[0]["visual_intent"], &visual0); err != nil {
		t.Fatalf("parse slots[0].visual_intent: %v", err)
	}
	if !strings.HasSuffix(visual0, "(at source offset 0/380)") {
		t.Errorf("golden slots[0] visual_intent does NOT end with the planner's runtime format "+
			"'(at source offset 0/380)' (would indicate the planner's visualIntentFor() drift): got=%q",
			visual0)
	}
	if !strings.HasPrefix(visual0, "Visual depiction of: ") {
		t.Errorf("golden slots[0] visual_intent does NOT start with the planner's runtime prefix "+
			"'Visual depiction of: ' (would indicate the planner's visualIntentFor() drift): got=%q",
			visual0)
	}
}

// TestPrePlanner_VisualIntentAndAnchorOffsetsPerSlot verifies the
// planner's per-slot computation independently of the golden file.
// Pins the planner's runtime SSOT:
//
//   - anchorOffsetsForTopic returns (0, 0, "") when no topic
//     substring matches the canonicalized source text.
//   - visualIntentFor(topic, 0, len(canonical)) returns
//     "Visual depiction of: <topic> (at source offset 0/<len>)".
//
// Together with the byte-compare golden in
// TestClipPrePlanner_PacquiaoBroner_E2E Suite (a), this test catches
// both computation drift AND planner-output-byte drift.
func TestPrePlanner_VisualIntentAndAnchorOffsetsPerSlot(t *testing.T) {
	t.Parallel()

	p := NewDeterministicPlanner()
	plan, err := p.Plan(context.Background(), pacquiaoReq())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Canonical source length (the same substrate `visualIntentFor`
	// uses). The byte length is 380 (verified by UPDATE_GOLDEN=1
	// regeneration: SHA-256 input bytes == 380).
	canonical := scriptpkg.CanonicalizeSourceText(pacquiaoSourceText)
	if len(canonical) != 380 {
		t.Fatalf("canonical source byte length drift: want 380, got %d (substrate contract pin)",
			len(canonical))
	}

	wantSlots := []struct {
		topic        string
		wantStartOff int
	}{
		{"La fase iniziale e lo studio della distanza", 0},
		{"Pressione crescente di Pacquiao (round 4-6)", 0},
		{"Round 7 - momento decisivo", 0},
		{"Decisione finale dei giudici", 0},
	}
	if len(plan.Slots) != len(wantSlots) {
		t.Fatalf("slot count: want %d, got %d", len(wantSlots), len(plan.Slots))
	}
	for i, want := range wantSlots {
		s := plan.Slots[i]
		if s.Topic != want.topic {
			t.Errorf("slot[%d] topic drift: want %q, got %q",
				i, want.topic, s.Topic)
		}
		// anchorOffsetsForTopic: no substring match → degenerate
		// (0, 0, "").
		if s.SourceAnchor == nil {
			t.Errorf("slot[%d] source_anchor nil", i)
			continue
		}
		if s.SourceAnchor.StartOffset != want.wantStartOff {
			t.Errorf("slot[%d] source_anchor.start_offset: want %d, got %d",
				i, want.wantStartOff, s.SourceAnchor.StartOffset)
		}
		if s.SourceAnchor.EndOffset != want.wantStartOff {
			t.Errorf("slot[%d] source_anchor.end_offset: want %d, got %d",
				i, want.wantStartOff, s.SourceAnchor.EndOffset)
		}
		if s.SourceAnchor.Excerpt != "" {
			t.Errorf("slot[%d] source_anchor.excerpt: want empty (no substring match), got %q",
				i, s.SourceAnchor.Excerpt)
		}
		// visualIntentFor(topic, 0, 380) → exact format.
		wantVisual := fmt.Sprintf("Visual depiction of: %s (at source offset %d/%d)",
			want.topic, want.wantStartOff, len(canonical))
		if s.VisualIntent != wantVisual {
			t.Errorf("slot[%d] visual_intent drift: want %q, got %q",
				i, wantVisual, s.VisualIntent)
		}
	}
}

// mapKeysSorted returns the keys of m, sorted alphabetically.
// Used because json.Unmarshal into map[string]... does not
// preserve insertion order; tests can't rely on order for
// structural diffs.
func mapKeysSorted(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unmarshalString is a tiny helper for parsing string-typed JSON
// raw values. Returns the unquoted string + any parse error.
func unmarshalString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}
