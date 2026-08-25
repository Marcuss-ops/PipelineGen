package filesystem

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteJSON marshals v to JSON and writes it to the file at path.
// If indent is true, the output is pretty-printed with 2-space indentation.
// It ensures the parent directory exists before writing.
func WriteJSON(path string, v any, indent bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var data []byte
	var err error
	if indent {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ReadJSON reads the file at path and unmarshals its JSON content into v.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
