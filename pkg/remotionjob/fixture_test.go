package remotionjob

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSharedRenderJobFixture(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "testdata", "render-job.v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared fixture %s: %v", path, err)
	}
	var job RenderJob
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if job.SchemaVersion != SchemaVersion || job.Composition != "YouTubeShortComposition" || job.DurationInFrames <= 0 {
		t.Fatalf("unexpected shared fixture: %+v", job)
	}
}
