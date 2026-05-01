package jsonutil

import (
	"encoding/json"
	"os"
)

// ReadJSON reads a JSON file into the destination.
func ReadJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}
