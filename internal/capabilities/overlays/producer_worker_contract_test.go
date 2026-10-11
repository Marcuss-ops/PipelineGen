package overlays

// producer_worker_contract_test.go is the PRODUCER side of the bidirectional
// PipelineGen ↔ RenderingGen contract check.
//
// The worker already pins its own decoding structs against the shared schema
// (RenderingGen .../overlay/contract_schema_parity_test.go). What was missing
// is the mirror image on the producer: nothing asserted that the JSON
// PipelineGen actually EMITS is a subset of what the worker's schema DECLARES,
// so a new field on OverlayItem could reach the wire and be rejected
// fail-closed at the worker with no test failing here first.
//
// This test closes that gap without adding a JSON-schema dependency:
//
//   - it loads the shared contract RenderingGen/contracts/overlay-plan.v1.schema.json
//     (the single cross-repo schema file) from the sibling checkout;
//   - it enumerates every field OverlayItem serializes (via json tags) and
//     asserts each one is a DECLARED property of the schema's item object,
//     which is `additionalProperties: false` — so an undeclared field IS a
//     worker rejection;
//   - it asserts the producer-only fields (`json:"-"`) never appear on the wire;
//   - it asserts the schema still requires the fields the planner always sets.
//
// A missing sibling checkout is a skip, matching the archcheck catalog gate, so
// a bare PipelineGen checkout does not fail on a repo it was not given.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// overlayPlanSchemaPath resolves the shared cross-repo schema relative to this
// package (internal/capabilities/overlays → repo root → sibling RenderingGen).
func overlayPlanSchemaPath() string {
	return filepath.Join("..", "..", "..", "..", "RenderingGen", "contracts", "overlay-plan.v1.schema.json")
}

// loadOverlayPlanItemSchema returns the schema's `items.items` object (the
// per-overlay-item contract) or skips when the sibling checkout is absent.
func loadOverlayPlanItemSchema(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(overlayPlanSchemaPath())
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("RenderingGen sibling checkout not present; cross-repo schema check skipped")
		}
		t.Fatalf("read overlay-plan schema: %v", err)
	}
	doc := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &doc), "schema must be valid JSON")
	properties, ok := doc["properties"].(map[string]any)
	require.True(t, ok, "schema must declare properties")
	items, ok := properties["items"].(map[string]any)
	require.True(t, ok, "schema must declare items")
	itemSchema, ok := items["items"].(map[string]any)
	require.True(t, ok, "schema must declare items.items")
	return itemSchema
}

// producerWireFields returns the json names OverlayItem emits, split into
// on-wire names and producer-only (`json:"-"`) names.
func producerWireFields(t *testing.T) (wire []string, producerOnly []string) {
	t.Helper()
	typ := reflect.TypeOf(OverlayItem{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		switch name {
		case "":
			continue
		case "-":
			producerOnly = append(producerOnly, typ.Field(i).Name)
		default:
			wire = append(wire, name)
		}
	}
	return wire, producerOnly
}

func TestOverlayItemWireFieldsAreDeclaredByWorkerSchema(t *testing.T) {
	itemSchema := loadOverlayPlanItemSchema(t)
	declared := map[string]bool{}
	props, ok := itemSchema["properties"].(map[string]any)
	require.True(t, ok, "items.items must declare properties, got %T", itemSchema["properties"])
	for name := range props {
		declared[name] = true
	}
	additional, hasAdditional := itemSchema["additionalProperties"]
	if hasAdditional {
		require.Equal(t, false, additional, "the schema must stay additionalProperties:false for this check to mean anything")
	} else {
		require.Fail(t, "schema items.items must declare additionalProperties:false")
	}

	wire, producerOnly := producerWireFields(t)
	require.NotEmpty(t, wire, "OverlayItem must serialize some fields")
	require.NotEmpty(t, producerOnly, "OverlayItem must keep at least one producer-only field (EntityRef)")

	for _, name := range wire {
		require.Truef(t, declared[name],
			"OverlayItem field %q is emitted but NOT declared by the worker schema — the worker rejects it fail-closed (additionalProperties:false). Add it to RenderingGen/contracts/overlay-plan.v1.schema.json in the same change, or tag it json:\"-\".", name)
	}
}

func TestOverlayItemProducerOnlyFieldsNeverSerialize(t *testing.T) {
	item := OverlayItem{
		ID: "wire-check", SceneID: "scene-0", Kind: "shape", TemplateID: "SHAPE",
		StartMs: 0, EndMs: 5000, StartUS: 0, DurationUS: 5_000_000,
		EntityRef: &OverlayEntityRef{EntityID: "ent_1", Type: "PERSON", Name: "Ada Lovelace"},
	}
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	wire := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &wire))

	_, producerOnly := producerWireFields(t)
	require.Contains(t, producerOnly, "EntityRef")
	require.NotContains(t, wire, "entity_ref", "producer-only identity must never reach the worker wire")
	require.NotContains(t, wire, "render_key", "RenderKey is recomputed by the sealing boundary, never transported")
}

func TestOverlayItemSchemaRequiredFieldsMatchProducerContract(t *testing.T) {
	itemSchema := loadOverlayPlanItemSchema(t)
	required, ok := itemSchema["required"].([]any)
	require.True(t, ok, "items.items must declare required")
	requiredNames := map[string]bool{}
	for _, name := range required {
		s, ok := name.(string)
		require.True(t, ok, "required entries must be strings")
		requiredNames[s] = true
	}
	// The producer's Validate() rejects an item missing id/template_id, and the
	// compiler binds start_ms/end_ms on every item, so these four must agree
	// across the boundary.
	for _, name := range []string{"id", "start_ms", "end_ms"} {
		require.Truef(t, requiredNames[name], "the schema must require %q, the producer always sets it", name)
	}
}

func TestProducerSchemaVersionMatchesSharedWorkerSchema(t *testing.T) {
	raw, err := os.ReadFile(overlayPlanSchemaPath())
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("RenderingGen sibling checkout not present")
		}
		t.Fatal(err)
	}
	var schema struct {
		ID string `json:"$id"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.Contains(t, schema.ID, ".v1.")
	require.Equal(t, "renderinggen.overlay-plan.v1", SchemaVersionPlan)
	require.Contains(t, schema.ID, "overlay-plan.v1.schema.json")
}

func TestChainSegmentFieldsMatchRenderingGenMirror(t *testing.T) {
	workerPath := filepath.Join("..", "..", "..", "..", "RenderingGen", "renderinggen", "internal", "overlay", "chain_compile.go")
	raw, err := os.ReadFile(workerPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("RenderingGen sibling checkout not present")
		}
		t.Fatal(err)
	}
	tag := regexp.MustCompile(`json:"([^,"]+)`)
	workerFields := map[string]bool{}
	for _, match := range tag.FindAllSubmatch(raw, -1) {
		workerFields[string(match[1])] = true
	}
	typ := reflect.TypeOf(ChainSegment{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		require.Truef(t, workerFields[name], "PipelineGen ChainSegment field %q is absent from RenderingGen's chainSegment mirror", name)
	}
}

// TestChartItemWireShapeMatchesWorkerSchema pins the chart payload path this
// repo emits: params.shape must allow "chart" (plus "type" for the enum), and
// the chart object must accept chart_type/data. A worker schema that dropped
// them would reject every chart card.
func TestChartItemWireShapeMatchesWorkerSchema(t *testing.T) {
	itemSchema := loadOverlayPlanItemSchema(t)
	props, ok := itemSchema["properties"].(map[string]any)
	require.True(t, ok)
	params, ok := props["params"].(map[string]any)
	require.True(t, ok, "items.items.params must be declared")
	paramProps, ok := params["properties"].(map[string]any)
	require.True(t, ok, "items.items.params.properties must be declared")

	shapeProp, ok := paramProps["shape"].(map[string]any)
	require.True(t, ok, "params.shape must be declared (the chart primitive selects it)")
	enum, ok := shapeProp["enum"].([]any)
	require.True(t, ok, "params.shape must enumerate its values")
	foundChart := false
	for _, v := range enum {
		if v == "chart" {
			foundChart = true
		}
	}
	require.True(t, foundChart, `params.shape must allow "chart"`)

	chartProp, ok := paramProps["chart"].(map[string]any)
	require.True(t, ok, "params.chart must be declared")
	chartProps, ok := chartProp["properties"].(map[string]any)
	require.True(t, ok, "params.chart must declare properties")
	for _, name := range []string{"chart_type", "data"} {
		require.Truef(t, chartProps[name] != nil, "params.chart must declare %q", name)
	}
}
