package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetFromPath_ResolvesRelativeLexiconRootAgainstConfigDir pins the
// boot-robustness contract for linguistics.lexicon_root: a RELATIVE root
// declared in a config file must resolve against the config file's own
// directory (when the anchored path exists), never against the process
// cwd. This is what makes "works under systemd" equal "works from any
// operator shell" for the lexicon registry bootstrap.
func TestGetFromPath_ResolvesRelativeLexiconRootAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	lexicons := filepath.Join(dir, "config", "lexicons", "fallback")
	if err := os.MkdirAll(lexicons, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	yamlBody := "linguistics:\n  lexicon_root: \"config/lexicons\"\n"
	if err := os.WriteFile(configPath, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Diverge cwd from the config directory: the legacy bug resolved the
	// relative root against this cwd and failed to boot.
	origCwd, err := os.Getwd()
	require0(t, err)
	t.Cleanup(func() { _ = os.Chdir(origCwd) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	cfg, err := GetFromPath(configPath)
	if err != nil {
		t.Fatalf("GetFromPath: %v", err)
	}
	if got := cfg.Linguistics.LexiconRoot; !filepath.IsAbs(got) || got != filepath.Join(dir, "config", "lexicons") {
		t.Fatalf("LexiconRoot = %q, want anchored %q", got, filepath.Join(dir, "config", "lexicons"))
	}
}

// TestGetFromPath_LeavesAbsoluteAndMissingAnchorsUntouched covers the two
// no-op branches: an absolute root is never rewritten, and a relative root
// whose anchor does not exist keeps its original form (the registry then
// fails with its own clear "read root" error instead of a mangled path).
func TestGetFromPath_LeavesAbsoluteAndMissingAnchorsUntouched(t *testing.T) {
	dir := t.TempDir()
	absRoot := filepath.Join(dir, "abs-lexicons")
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("absolute root untouched", func(t *testing.T) {
		configPath := filepath.Join(dir, "abs.yaml")
		body := "linguistics:\n  lexicon_root: \"" + absRoot + "\"\n"
		if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := GetFromPath(configPath)
		if err != nil {
			t.Fatalf("GetFromPath: %v", err)
		}
		if got := cfg.Linguistics.LexiconRoot; got != absRoot {
			t.Fatalf("LexiconRoot = %q, want untouched %q", got, absRoot)
		}
	})

	t.Run("missing anchor keeps original form", func(t *testing.T) {
		configPath := filepath.Join(dir, "missing.yaml")
		body := "linguistics:\n  lexicon_root: \"no/such/dir\"\n"
		if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := GetFromPath(configPath)
		if err != nil {
			t.Fatalf("GetFromPath: %v", err)
		}
		if got := cfg.Linguistics.LexiconRoot; got != "no/such/dir" {
			t.Fatalf("LexiconRoot = %q, want original %q", got, "no/such/dir")
		}
	})
}

func require0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
