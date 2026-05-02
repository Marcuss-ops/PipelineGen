package assetops

import (
	"encoding/json"
)

// BuildMetadata builds a JSON metadata string from a map of fields
func BuildMetadata(fields map[string]interface{}) string {
	data, err := json.Marshal(fields)
	if err != nil {
		return "{}"
	}
	return string(data)
}
