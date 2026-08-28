package procmetrics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadGPURetriesAfterTransientFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires a POSIX host")
	}

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	script := filepath.Join(dir, "nvidia-smi")
	body := fmt.Sprintf(`#!/bin/sh
if [ ! -f %q ]; then
  : > %q
  exit 1
fi
printf '12, 100, 4, 5, 60\n'
`, state, state)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	p := New(Options{NvidiaSMI: script})
	if got := p.readGPU(context.Background()); got != nil {
		t.Fatalf("first transient failure must produce a missing sample, got %+v", got)
	}
	got := p.readGPU(context.Background())
	if got == nil {
		t.Fatal("second sample must retry nvidia-smi and recover")
	}
	if got.utilPct != 12 || got.encoderPct != 4 || got.decoderPct != 5 || got.tempC != 60 {
		t.Fatalf("unexpected recovered GPU sample: %+v", got)
	}
	wantBytes := int64(100 * 1024 * 1024)
	if got.usedBytes != wantBytes {
		t.Fatalf("usedBytes=%d, want %d", got.usedBytes, wantBytes)
	}
}
