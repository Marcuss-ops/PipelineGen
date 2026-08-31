// Package config (test) — path_resolution_test.go.
//
// FASE-N EvalSymlinks audit (July 2026): when StorageConfig.DataDir is
// left at its documented default ("./data") and the operator runs the
// server from a cwd whose EvalSymlinks results differ from "./data"'s
// relative resolution, the IsLocalFolderAllowed symlink-canonicalization
// guard at internal/application/clips/bulk_upload_helpers.go silently
// fails because the path strings it receives are relative. The fix is at
// the method layer: StorageConfig.{MediaPath, TempPath, FullPath,
// mediaSubPath, AbsDataDir} now route through filepath.Abs so any caller
// that asks for the canonical root gets an absolute string regardless
// of the caller's process cwd.
//
// This test pins the contract end-to-end: with DataDir="./data", the
// three primary path APIs MUST return absolute paths regardless of
// which of three different cwds the test process is in. If a future
// refactor reintroduces a relative-form leak (e.g. someone optimises
// absDataDir() away or replaces filepath.Abs with filepath.Join+mkdir
// territory), this test catches it loudly.
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStorageConfig_PathsAreAbsoluteForAnyCWD guards the FASE-N
// path-canonicalization contract.
func TestStorageConfig_PathsAreAbsoluteForAnyCWD(t *testing.T) {
	// MediaDir / TempDir are set explicitly because applyDefaults()
	// only runs when the struct is loaded via the env-var loader;
	// a struct literal (as below) leaves the un-tagged fields at
	// their zero value ("") and filepath.Join on an absolute root
	// + "" yields the root unchanged. We want to test the join
	// semantics, not the zero-value fallback.
	cfg := StorageConfig{
		DataDir:  "./data",
		MediaDir: "media",
		TempDir:  "tmp",
	}

	origCwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	// Probe under three deterministically-different cwds. The os.Stat
	// gate skips cwds absent on minimal containers (e.g. /opt may not
	// exist on Alpine-scratch Docker images) without failing the test
	// for the wrong reason — at least /tmp is universally present.
	cwds := []string{}
	for _, c := range []string{"/tmp", "/var", "/opt"} {
		if _, err := os.Stat(c); err == nil {
			cwds = append(cwds, c)
		}
	}
	require.NotEmpty(t, cwds, "no representative cwd is present on this host")

	for _, cwd := range cwds {
		require.NoError(t, os.Chdir(cwd))
		mediaPath := cfg.MediaPath()
		tempPath := cfg.TempPath()
		absDataDir := cfg.AbsDataDir()
		// The user's stated contract is "data_dir='./data' + cwd=q
		// → assoluto consistente" — a relative DataDir MUST resolve
		// to an absolute path regardless of cwd. Pure IsAbs is the
		// tightest expression of that contract; structural-layout
		// contains-checks were removed in the post-review refinement
		// because they would couple the test to the production
		// default string values (DefaultMediaDir / DefaultTempDir)
		// and silently mask default-renaming regressions.
		require.True(t, filepath.IsAbs(mediaPath),
			"MediaPath() must be absolute from cwd=%s got %q", cwd, mediaPath)
		require.True(t, filepath.IsAbs(tempPath),
			"TempPath() must be absolute from cwd=%s got %q", cwd, tempPath)
		require.True(t, filepath.IsAbs(absDataDir),
			"AbsDataDir() must be absolute from cwd=%s got %q", cwd, absDataDir)
	}
}

func TestStorageConfig_PrimaryDBPathDefaultsToCanonicalLocation(t *testing.T) {
	dataDir := t.TempDir()
	cfg := StorageConfig{DataDir: dataDir, MediaDir: "custom-media"}
	want := filepath.Join(dataDir, "media", "media.db.sqlite")
	if got := cfg.PrimaryDBFullPath(); got != want {
		t.Fatalf("PrimaryDBFullPath() = %q, want %q", got, want)
	}
	if err := cfg.ValidatePrimaryDBPath(); err != nil {
		t.Fatalf("default primary path rejected: %v", err)
	}
}

func TestStorageConfig_RejectsNonCanonicalPrimaryDBPaths(t *testing.T) {
	dataDir := t.TempDir()
	canonical := filepath.Join(dataDir, "media", "media.db.sqlite")
	for _, path := range []string{
		filepath.Join(dataDir, "media.db.sqlite"),
		filepath.Join(dataDir, "cmd", "admin", "data", "media", "media.db.sqlite"),
		filepath.Join(dataDir, "legacy", "media.db.sqlite"),
		"data/media.db.sqlite",
	} {
		t.Run(path, func(t *testing.T) {
			cfg := StorageConfig{DataDir: dataDir, PrimaryDBPath: path}
			if err := cfg.ValidatePrimaryDBPath(); err == nil {
				t.Fatalf("ValidatePrimaryDBPath(%q) returned nil; canonical path is %q", path, canonical)
			}
			if got := cfg.PrimaryDBFullPath(); got != path {
				t.Fatalf("PrimaryDBFullPath() = %q, want invalid path %q preserved for fail-closed handling", got, path)
			}
		})
	}
}

func TestStorageConfig_AcceptsCanonicalPrimaryDBOverride(t *testing.T) {
	dataDir := t.TempDir()
	canonical := filepath.Join(dataDir, "media", "media.db.sqlite")
	cfg := StorageConfig{DataDir: dataDir, PrimaryDBPath: canonical}
	if err := cfg.ValidatePrimaryDBPath(); err != nil {
		t.Fatalf("canonical override rejected: %v", err)
	}
	if got := cfg.PrimaryDBFullPath(); got != canonical {
		t.Fatalf("PrimaryDBFullPath() = %q, want %q", got, canonical)
	}
}
