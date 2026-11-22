package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// canonicalChainSegmentKeys is the single Go statement of the chain_segments
// wire contract. The Rust producer pins the same list in
// `editorial_plan_v2::planner::coalesce::segment_contract_tests::CANONICAL_KEYS` and the
// RenderingGen consumer pins it in `chain_segments_contract_test.go`; a key
// added on one side without the others is a silent decode failure on the
// consumer, so every side asserts the set instead of trusting prose.
var canonicalChainSegmentKeys = []string{
	"text",
	"group_id",
	"subgroup_id",
	"motion_id",
	"preset_id",
	"image_preset_id",
	"template_id",
	"kind",
	"entity_id",
	"entity_caption",
	"caption_motion_id",
	"caption_motion_params",
	"caption_font_family",
	"caption_layout",
	"caption_color",
	"entity_style_id",
	"start_us",
	"duration_us",
	"motion_params",
	"asset_id",
	"assets",
	"image_layers",
	"frame",
	"params",
	"style",
	"map",
}

// TestChainSegmentWireKeysMatchCanonicalContract pins the struct to the
// documented contract. It also pins `omitempty`: the Rust producer drops a
// blank string instead of emitting `""`, and Params feed the plan fingerprint,
// so a Go re-emit that keeps an empty string would change the plan hash of an
// otherwise identical chain.
func TestChainSegmentWireKeysMatchCanonicalContract(t *testing.T) {
	typ := reflect.TypeOf(ChainSegment{})
	require.Equal(t, len(canonicalChainSegmentKeys), typ.NumField(),
		"ChainSegment field count drifted from the canonical contract")

	got := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		require.NotEmptyf(t, tag, "field %s carries no json tag", field.Name)
		got[tag] = true

		if tag == "start_us" || tag == "duration_us" {
			continue
		}
		require.Containsf(t, field.Tag.Get("json"), "omitempty",
			"field %s must stay omitempty so a Go re-emit matches the Rust producer", field.Name)
	}

	want := make(map[string]bool, len(canonicalChainSegmentKeys))
	for _, key := range canonicalChainSegmentKeys {
		want[key] = true
	}
	require.Equal(t, want, got, "ChainSegment json tags drifted from the canonical contract")
}

// TestConformanceFixtureUsesOnlyContractedKeys is the fail-closed half that
// does not depend on a sibling checkout: whatever the producer writes into the
// shared fixture must be decodable by this struct, key for key. An unknown key
// means the producer advanced and the consumers did not.
func TestConformanceFixtureUsesOnlyContractedKeys(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "testdata", "contracts", "chain-segments-v1.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read shared conformance fixture")

	var document struct {
		Plan struct {
			Items []struct {
				Params map[string]json.RawMessage `json:"params"`
			} `json:"items"`
		} `json:"plan"`
	}
	require.NoError(t, json.Unmarshal(data, &document))
	require.Len(t, document.Plan.Items, 1)

	rawSegments, ok := document.Plan.Items[0].Params["chain_segments"]
	require.True(t, ok, "conformance plan carries no chain_segments")
	var segments []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawSegments, &segments))
	require.NotEmpty(t, segments)

	known := make(map[string]bool, len(canonicalChainSegmentKeys))
	for _, key := range canonicalChainSegmentKeys {
		known[key] = true
	}
	for index, segment := range segments {
		for key := range segment {
			require.Truef(t, known[key],
				"fixture segment %d carries key %q that ChainSegment does not model", index, key)
		}
	}

	// The same bytes must survive a decode/encode round trip without changing
	// the key set, which is what a child-plan split and a durable-run reload do.
	var wire []map[string]any
	require.NoError(t, json.Unmarshal(rawSegments, &wire))
	decoded, ok := decodeChainSegments(map[string]any{"chain_segments": wire})
	require.True(t, ok)
	require.Len(t, decoded, len(wire))
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	var roundTripped []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &roundTripped))
	require.Len(t, roundTripped, len(wire))
	for index := range wire {
		require.Equalf(t, wire[index], roundTripped[index],
			"segment %d changed on a decode/encode round trip", index)
	}
}
