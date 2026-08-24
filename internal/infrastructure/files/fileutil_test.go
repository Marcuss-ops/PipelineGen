package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteJSONAndReadJSON(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("roundtrip", func(t *testing.T) {
		path := filepath.Join(tmpDir, "roundtrip.json")
		input := map[string]any{"name": "test", "count": 42, "nested": map[string]any{"ok": true}}

		if err := WriteJSON(path, input, false); err != nil {
			t.Fatalf("WriteJSON failed: %v", err)
		}

		var output map[string]any
		if err := ReadJSON(path, &output); err != nil {
			t.Fatalf("ReadJSON failed: %v", err)
		}

		if output["name"] != "test" || output["count"] != float64(42) {
			t.Errorf("unexpected output: %v", output)
		}
	})

	t.Run("indent", func(t *testing.T) {
		path := filepath.Join(tmpDir, "indent.json")
		input := map[string]string{"a": "b"}

		if err := WriteJSON(path, input, true); err != nil {
			t.Fatalf("WriteJSON failed: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}

		if !strings.Contains(string(data), "\n") {
			t.Error("indented JSON should contain newlines")
		}
	})

	t.Run("read missing file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "missing.json")
		var v map[string]string
		if err := ReadJSON(path, &v); err == nil {
			t.Error("expected error for missing file")
		}
	})
}
