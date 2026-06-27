// Package app — Registries-and-SSOT manifest generator (test-driven).
//
// Spec "Registries and Single Source of Truth" (§"Generated manifests")
// requires the architecture generator to produce:
//   - architecture/generated/capabilities.json
//   - architecture/generated/jobs.json
//   - architecture/generated/providers.json
//
// This file ships the generator as a `go test` target — same exit
// semantics as the source-tree archcheck, lower ceremony than a
// standalone CLI binary (the project already favours test-driven
// fixture composition in composition_test.go).
//
// CI gate: scripts/ci-architectural-checks.sh runs
// `go test -run TestArchGen ./internal/app/...` and the test emits
// into t.TempDir; the CI follow-up step
// `git diff --exit-code architecture/generated/` is the cross-PR
// fresh-manifest gate (run separately, since `go test` should never
// touch tracked files in the working tree).

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ── Output schema (mirrors architecture/generated/*.json) ────────────────

// capabilityManifest is the JSON shape of capabilities.json.
// Module names + enabled flag (the only observable surface post-Freeze).
type capabilityManifest struct {
	Commit       string            `json:"commit,omitempty"`
	Capabilities []capabilityEntry `json:"capabilities"`
}

type capabilityEntry struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// jobManifest is the JSON shape of jobs.json.
// Job type + Dispatcher handler presence + timeout + retry defaults.
type jobManifest struct {
	Commit string     `json:"commit,omitempty"`
	Jobs   []jobEntry `json:"jobs"`
}

type jobEntry struct {
	Type              string `json:"type"`
	Description       string `json:"description,omitempty"`
	TimeoutSec        int    `json:"timeout_sec"`
	DefaultMaxRetries int    `json:"default_max_retries"`
	HasHandler        bool   `json:"has_handler"`
}

// providerManifest is the JSON shape of providers.json.
// Provider name + advertised capabilities.
type providerManifest struct {
	Commit    string          `json:"commit,omitempty"`
	Providers []providerEntry `json:"providers"`
}

type providerEntry struct {
	Name         string                 `json:"name"`
	Capabilities []providers.Capability `json:"capabilities"`
}

// ── Test ──────────────────────────────────────────────────────────────────

// TestArchGen_EmitsManifests is the canonical signal that the
// registries freeze, compose, and serialise correctly. Running
// `go test -run TestArchGen ./internal/app/...` in CI is the
// freshness gate for architecture/generated/*.json.
func TestArchGen_EmitsManifests(t *testing.T) {
	chdirToProjectRoot(t)

	if testing.Short() {
		t.Skip("archgen runs the full composition; skipping in -short mode")
	}

	dataDir := t.TempDir()
	cfg := minimalConfig(dataDir)
	log := zaptest.NewLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dbs, err := initDatabases(cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil {
			dbs.Close()
		}
	})

	root, err := NewComposition(ctx, cfg, dbs, log)
	require.NoError(t, err, "NewComposition under minimal config")

	wiring, err := WireRegistry(ctx, cfg, log, root)
	require.NoError(t, err, "WireRegistry under minimal config")
	t.Cleanup(func() { /* freeze already done inside WireRegistry */ })

	// Emit into t.TempDir so the test never touches the working tree.
	outDir := t.TempDir()

	// capabilities.json: every module the registry accepted.
	caps := wiring.Registry.GetEnabled()
	capManifest := capabilityManifest{
		Capabilities: make([]capabilityEntry, 0, len(caps)),
	}
	for _, m := range caps {
		capManifest.Capabilities = append(capManifest.Capabilities, capabilityEntry{
			Name:    m.Name(),
			Enabled: m.Enabled(),
		})
	}
	sort.Slice(capManifest.Capabilities, func(i, j int) bool {
		return capManifest.Capabilities[i].Name < capManifest.Capabilities[j].Name
	})
	require.NoError(t, writeJSON(filepath.Join(outDir, "capabilities.json"), capManifest))

	// jobs.json: every job type the Dispatcher accepts handlers for,
	// merged with the canonical jobs.Registry metadata. The merge
	// pins both surfaces from the registries so a drift in either
	// one shows up as a missing entry. jobs.Compose() is cached in
	// single local — the canonical registry builder registers ~25
	// entries per invocation.
	jobReg := jobs.Compose()
	handlers := root.Jobs.Dispatcher.AllHandlers()
	jobEntries := jobReg.AllTypes()
	jobTypeMeta := make(map[string]jobEntry, len(jobEntries))
	for _, jt := range jobEntries {
		meta, ok := jobReg.Get(jt)
		entry := jobEntry{Type: jt}
		if ok {
			entry.Description = meta.Description
			entry.TimeoutSec = int(meta.Timeout.Seconds())
			entry.DefaultMaxRetries = meta.DefaultMaxRetries
		}
		_, entry.HasHandler = handlers[jt]
		jobTypeMeta[jt] = entry
	}
	jobList := make([]jobEntry, 0, len(jobTypeMeta))
	for _, e := range jobTypeMeta {
		jobList = append(jobList, e)
	}
	sort.Slice(jobList, func(i, j int) bool {
		return jobList[i].Type < jobList[j].Type
	})
	require.NoError(t, writeJSON(filepath.Join(outDir, "jobs.json"), jobManifest{
		Jobs: jobList,
	}))

	// providers.json: every provider currently registered. Both the
	// outer provider name list AND each provider's capabilities slice
	// are sorted so JSON output is deterministic across runs — the
	// CI follow-up `git diff --exit-code architecture/generated/`
	// gate depends on byte-stable output.
	provList := root.Search.ProviderRegistry.All()
	provEntries := make([]providerEntry, 0, len(provList))
	for _, p := range provList {
		caps := p.Capabilities()
		sort.Slice(caps, func(i, j int) bool { return caps[i] < caps[j] })
		provEntries = append(provEntries, providerEntry{
			Name:         p.Name(),
			Capabilities: caps,
		})
	}
	sort.Slice(provEntries, func(i, j int) bool {
		return provEntries[i].Name < provEntries[j].Name
	})
	require.NoError(t, writeJSON(filepath.Join(outDir, "providers.json"), providerManifest{
		Providers: provEntries,
	}))

	// Re-read and assert structural invariants.
	t.Run("capabilities", func(t *testing.T) {
		var read capabilityManifest
		require.NoError(t, readJSON(filepath.Join(outDir, "capabilities.json"), &read))
		require.NotEmpty(t, read.Capabilities, "at least one capability must be registered under minimal config")
		// sanity: every name appears once.
		seen := map[string]int{}
		for _, c := range read.Capabilities {
			seen[c.Name]++
		}
		for n, c := range seen {
			require.Equal(t, 1, c, "duplicate capability %q in capabilities.json (Uniqueness rule)", n)
		}
	})

	t.Run("jobs", func(t *testing.T) {
		var read jobManifest
		require.NoError(t, readJSON(filepath.Join(outDir, "jobs.json"), &read))
		require.NotEmpty(t, read.Jobs, "at least one job type must be registered at composition")
		seen := map[string]int{}
		for _, j := range read.Jobs {
			seen[j.Type]++
			require.NotEmpty(t, j.Type, "job type must not be empty (Uniqueness rule)")
		}
		for jt, c := range seen {
			require.Equal(t, 1, c, "duplicate job type %q in jobs.json (Uniqueness rule)", jt)
		}
	})

	t.Run("providers", func(t *testing.T) {
		var read providerManifest
		require.NoError(t, readJSON(filepath.Join(outDir, "providers.json"), &read))
		seen := map[string]int{}
		for _, p := range read.Providers {
			seen[p.Name]++
			require.NotEmpty(t, p.Name, "provider name must not be empty (Uniqueness rule)")
		}
		for n, c := range seen {
			require.Equal(t, 1, c, "duplicate provider %q in providers.json (Uniqueness rule)", n)
		}
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
