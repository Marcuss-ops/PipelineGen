package finaljob

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RuntimeAssetFromMap is the single ingress from the legacy map transport into
// the typed asset contract. No caller should carry the map past this boundary.
func RuntimeAssetFromMap(values map[string]any) (RuntimeAssetV1, error) {
	if values == nil {
		return RuntimeAssetV1{}, fmt.Errorf("final_job runtime asset is nil")
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return RuntimeAssetV1{}, fmt.Errorf("encode final_job runtime asset: %w", err)
	}
	var asset RuntimeAssetV1
	if err := json.Unmarshal(raw, &asset); err != nil {
		return RuntimeAssetV1{}, fmt.Errorf("decode final_job runtime asset: %w", err)
	}
	return asset, nil
}

// Map is the single egress from the typed asset back to the deployed Master
// map. It is called only by the wire functions, never by the preparation core.
func (a RuntimeAssetV1) Map() (map[string]any, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encode final_job typed runtime asset: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode final_job typed runtime asset: %w", err)
	}
	normalizeRuntimeAssetNumbers(values)
	return values, nil
}

// normalizeRuntimeAssetNumbers restores the integer representation the legacy
// payload builder already produced for the Master. A json.Number that is not
// an integer remains a float so no precision is invented.
func normalizeRuntimeAssetNumbers(values map[string]any) {
	for key, value := range values {
		switch typed := value.(type) {
		case json.Number:
			if i, err := typed.Int64(); err == nil {
				values[key] = i
				continue
			}
			if f, err := typed.Float64(); err == nil {
				values[key] = f
			}
		case map[string]any:
			normalizeRuntimeAssetNumbers(typed)
		}
	}
}
