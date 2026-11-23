package gc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCASObject(t *testing.T, root, address string, raw []byte, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(root, address[:2], address[2:4], address)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir CAS shard: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write CAS object: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("age CAS object: %v", err)
	}
	return path
}

func TestPruneResumeCASKeepsActiveAndAssets(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	staleAddress := strings.Repeat("a", 64)
	activeAddress := strings.Repeat("b", 64)
	assetAddress := strings.Repeat("c", 64)
	stale := writeCASObject(t, root, staleAddress, []byte(`{"resume_marker":true}`), now.Add(-10*24*time.Hour))
	active := writeCASObject(t, root, activeAddress, []byte(`{"resume_marker":true}`), now.Add(-10*24*time.Hour))
	asset := writeCASObject(t, root, assetAddress, []byte("not a resume document"), now.Add(-10*24*time.Hour))

	cfg := ResumeConfig{
		Root:       root,
		Retention:  7 * 24 * time.Hour,
		ActiveRefs: map[string]struct{}{activeAddress: {}},
		Parse: func(raw []byte) (bool, error) {
			return bytes.Contains(raw, []byte("resume_marker")), nil
		},
	}

	dry, err := pruneResumeCAS(context.Background(), cfg, now, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Files != 2 || dry.ActiveFiles != 1 || dry.EligibleFiles != 1 || dry.DeletedFiles != 0 {
		t.Fatalf("dry report = %+v, want 2 resume docs, 1 active, 1 eligible, 0 deleted", dry)
	}
	for _, path := range []string{stale, active, asset} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry run removed %s: %v", path, err)
		}
	}

	applied, err := pruneResumeCAS(context.Background(), cfg, now, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.DeletedFiles != 1 || applied.DeletedBytes == 0 {
		t.Fatalf("apply report = %+v, want one deleted resume", applied)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale resume still exists: %v", err)
	}
	for _, path := range []string{active, asset} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("apply removed a protected object %s: %v", path, err)
		}
	}
}

func TestRefsInJSONPayloadsFindsEveryAddress(t *testing.T) {
	first := strings.Repeat("A", 64)
	second := strings.Repeat("d", 64)
	refs := RefsInJSONPayloads([]string{
		`{"continuation":{"resume":{"sha256":"` + first + `"}}}`,
		`{"plan_sha256":"` + second + `","nested":["` + second + `"]}`,
		`{"ignored":"short"}`,
	})
	if len(refs) != 2 {
		t.Fatalf("refs = %v, want two addresses", refs)
	}
	if _, ok := refs[strings.ToLower(first)]; !ok {
		t.Fatalf("uppercase address was not normalised: %v", refs)
	}
	if _, ok := refs[second]; !ok {
		t.Fatalf("second address missing: %v", refs)
	}
}
