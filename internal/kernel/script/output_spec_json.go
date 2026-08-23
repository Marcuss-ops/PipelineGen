package script

import "encoding/json"

// UnmarshalJSON decodes OutputSpec with Toggle tri-state support.
// It performs a 2-pass decode:
//  1. Alias-decode via Alias OutputSpec (recursion-safe) so standard
//     json.Unmarshal handles struct field assignment via Toggle's
//     UnmarshalJSON method (bool/string/null forms).
//  2. Raw-map pre-pass to default OMITTED Toggle keys to ToggleDefault.
//     Go's default decode leaves absent Toggle fields at the zero value
//     Toggle(""), which AsBool() would treat as true. This method fixes
//     that by defaulting omitted keys to ToggleDefault.
//
// If the raw-map pre-pass fails, pass-1 alias values are preserved.
func (o *OutputSpec) UnmarshalJSON(data []byte) error {
	// Step 1: alias-decode to populate all fields via Toggle's
	// UnmarshalJSON (handles string/bool/null forms correctly).
	type Alias OutputSpec
	var tmp Alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*o = OutputSpec(tmp)

	// Step 2: raw-map pre-pass to detect OMITTED Toggle keys
	// individually (no clobber of pass-1 values). If the payload is
	// not a JSON object, skip the raw-map step — pass-1 alias values
	// are preserved (which may be Toggle("") for unmappable data,
	// but that's safer than collapsing to ToggleDefault and erasing
	// any valid string-form keys the alias pass managed to read).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if _, ok := raw["extract_entities"]; !ok {
			o.ExtractEntities = ToggleDefault
		}
		if _, ok := raw["generate_metadata"]; !ok {
			o.GenerateMetadata = ToggleDefault
		}
		if _, ok := raw["generate_scene_images"]; !ok {
			o.GenerateSceneImages = ToggleDefault
		}
		if _, ok := raw["stock_enabled"]; !ok {
			o.StockEnabled = ToggleDefault
		}
	}
	return nil
}

// MarshalJSON is the canonical godlike/06 SSOT owner of OutputSpec's
// wire-shape egress. It converts every ToggleDefault value to the
// empty string before delegating to the alias marshaler — the alias
// (which has no custom MarshalJSON method) emits Toggle as a plain
// string, and the standard json:",omitempty" tag suppresses the key
// when the underlying string is empty. This restores the
// outbound-compact wire-shape that pre-PR-3 callers enjoy (5 toggle
// keys collapsed to ONLY the explicitly-set ones).
//
// godlike/06 SSOT: this lives ONLY here (canonical) — HTTP DTOs and
// any future JSON middleware MUST NOT duplicate it.
func (o OutputSpec) MarshalJSON() ([]byte, error) {
	type Alias OutputSpec
	tmp := Alias(o)

	// Collapse ToggleDefault → "" so omitempty kicks in. The other
	// 3 canonical states (ToggleEnabled/ToggleDisabled plus any
	// non-canonical value) are marshaled as-is.
	hideIfDefault := func(t *Toggle) {
		if *t == ToggleDefault {
			*t = ""
		}
	}

	hideIfDefault(&tmp.ExtractEntities)
	hideIfDefault(&tmp.GenerateMetadata)
	hideIfDefault(&tmp.GenerateSceneImages)
	hideIfDefault(&tmp.StockEnabled)

	return json.Marshal(tmp)
}
