package script

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The dump is the only way to answer a copy-only rejection that names a path on
// the worker machine, so it must write both envelopes verbatim and stay off
// when the knob is unset.
func TestKeepFinalJobEnvelopesWritesBothPhasesOnlyWhenAsked(t *testing.T) {
	prepare := map[string]any{"compiled_render_plan_json": "{}"}
	finalize := map[string]any{"runtime_assets": []any{}}

	dir := t.TempDir()
	t.Setenv("VELOX_FINAL_JOB_DUMP_DIR", "")
	keepFinalJobEnvelopes("run_unset", prepare, finalize)
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("dump wrote with VELOX_FINAL_JOB_DUMP_DIR unset: %v %v", entries, err)
	}

	t.Setenv("VELOX_FINAL_JOB_DUMP_DIR", dir)
	keepFinalJobEnvelopes("run_abc", prepare, finalize)

	for name, want := range map[string]map[string]any{"prepare": prepare, "finalize": finalize} {
		raw, err := os.ReadFile(filepath.Join(dir, "run_abc-"+name+".json"))
		if err != nil {
			t.Fatalf("read %s envelope: %v", name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode %s envelope: %v", name, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s envelope round-trip lost keys: got %v want %v", name, got, want)
		}
	}
}

// An unwritable directory must not abort the handoff: the dump is diagnostic.
func TestKeepFinalJobEnvelopesIgnoresUnwritableDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	t.Setenv("VELOX_FINAL_JOB_DUMP_DIR", dir)
	keepFinalJobEnvelopes("run_abc", map[string]any{"a": 1}, map[string]any{"b": 2})
}
