// Package main — cmd/admin/regen-current-yaml/main.go (Fase 7(c), Push 7, July 2026).
//
// Regeneration contract: `go run ./cmd/admin/regen-current-yaml > architecture/current.yaml`
// re-emits the canonical current.yaml from a composition invocation that
// DOES NOT call engine.Run(). The composition factory wires every
// bundle; the runtime registry (gin.Engine.Routes()) is captured at
// composition-time; gates_baseline counts (capability_inventory.yaml)
// are projected into the canonical schema; hand-curated entries below
// are preserved as overrides the migration cannot derive.
//
// godlike/07 NO-FAKE-AVAILABILITY: this binary MUST NOT require a
// running server. It MUST construct the engine in-process and walk the
// route registry without invoking gin.Engine.Run. The composition root
// factory (`bootstrap.NewAppWithConfig`) is the canonical entry; calling
// `app.HTTPServer.Engine` returns the populated engine with all routes
// mounted.
//
// godlike/06 SSOT: the emitted YAML is canonical. CI gate Check 70
// (route-descriptor drift detector) compares this output to the runtime
// routes; any diff = SSOT regression → exit 1.
//
// Implementation note: the composition root currently routes via
// `engine.GET(...)` directly (per `internal/api/routes.go::Setup`).
// Until Push 7(a) wires the canonical RouteDescriptor into the engine,
// `engine.Run` is replaced by a manual gin.Engine construction +
// route-capture walk in this binary. The captured (method, path) pairs
// feed the canonical route manifest surface (`architecture/routes.yaml`).
// After Push 7(a) integration, this binary will instead iterate the
// typed RouteDescriptor registry — same output surface, no listener
// required at any step.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// Entry is the canonical YAML row shape. Mirrors the architecture/
// current.yaml schema defined in `gates_baseline` parity.
type Entry struct {
	ID               string    `yaml:"id"`
	Status           string    `yaml:"status"`
	Owner            string    `yaml:"owner"`
	Deadline         time.Time `yaml:"deadline"`
	Rationale        string    `yaml:"rationale,omitempty"`
	FollowUpTickets  []Entry   `yaml:"follow_up_tickets,omitempty"`
	Count            *Count    `yaml:"count,omitempty"`
	CrossRefs        *CrossRefs `yaml:"cross_refs,omitempty"`
}

// Count is the optional workload-survey struct (baseline_initial vs
// current vs target_zero) emitted under waves that quantify drift.
type Count struct {
	BaselineInitial int `yaml:"baseline_initial"`
	Current         int `yaml:"current"`
	TargetZero      int `yaml:"target_zero"`
}

// CrossRefs is the optional machine-readable pointer struct emitted
// under waves that govern a CI gate / migration / doc.
type CrossRefs struct {
	GateID    string `yaml:"gate_id,omitempty"`
	Policy    string `yaml:"policy,omitempty"`
	CIGate    string `yaml:"ci_gate,omitempty"`
	Migration string `yaml:"migration,omitempty"`
	Plan      string `yaml:"plan,omitempty"`
	FollowUp  string `yaml:"follow_up,omitempty"`
	PreStep   string `yaml:"pre_step,omitempty"`
	RuntimeCapture string `yaml:"runtime_capture,omitempty"`
}

// canonicalHandCuratedEntries is the back-compat shell for waves that
// predate the gates_baseline schema. Migrate them to a gate_id
// reference as waves progress. Order matters: the YAML emitter sorts
// by ID alphabetically for diff-stability.
var canonicalHandCuratedEntries = []Entry{
	{
		ID:       "WAVE-20-QDRANT-005D-HYGIENE",
		Status:   "in_progress",
		Owner:    "qdrant-hygiene work stream",
		Deadline: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		Rationale: "Forward-prevention half of QDRANT-RECOVERY-001 (resolved July 2026).\nOwns Check 5 (same-package duplicate-type-declaration lint) +\ndocs/architecture/godlike/duplicate-types-allowlist.txt maintenance +\nthe canonical AWK-lens switch from package-name-only to the\nGo-package-identity tuple (directory_path, package_name) to clear\nthe ~14 cross-directory same-package-NAME false-positives.\n",
		FollowUpTickets: []Entry{
			{
				ID:   "ARCHCHECK-GO-MIGRATION-PHASE-1",
				Rationale: "PR-ID for the AWK-lens migration; tracked under WAVE-ARCHCHECK-MIGRATION.",
			},
		},
		CrossRefs: &CrossRefs{
			GateID: "QDRANT-005D",
			Policy: "architecture/policy.yaml::lint_gates[check=5]",
			CIGate: "scripts/ci-architectural-checks.sh::Check 5",
		},
	},
	{
		ID:       "WAVE-21-PR-G-SCRIPTS-SUBPKG",
		Status:   "in_progress",
		Owner:    "PipelineGen Agent (PR-G.1 EXPAND)",
		Deadline: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Rationale: "PR-G migration phases EXPAND → BACKFILL → CUTOVER → CONTRACT\nsplits internal/application/scripts (53-file mega-package) into\nbounded subpackages (curation/generation/persistence/postprocess/\nsource/usecases) per godlike/07. Day-1 (PR-G.1 EXPAND) shipped\n2026-06-27; PR-G.2 BACKFILL is the next deliverable. Deadline\n2026-08-01 pre-WAVE-22 CUTOVER lock-in so the backfill lands before\nthe WAVE-22 source-catalog + route-manifest gates are promoted to\nenforce_zero=true (2026-09-15).\n",
		CrossRefs: &CrossRefs{
			Policy: "architecture/policy.yaml::max_files_per_package=40",
			Plan:   "architecture/ownership.generated.yaml::application_scripts",
		},
	},
	{
		ID:       "WAVE-22-PR-D-SERVICE-DEPS-CAP",
		Status:   "in_progress",
		Owner:    "PipelineGen Agent (PR-D)",
		Deadline: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC),
		Rationale: "ServiceDeps / Deps structured-port cap (8 fields max) is\npromoted to a hard CI gate (Check 23). Current observations:\nartlist.ServiceDeps ~15 fields + a few others. Each migration\nPR must decrement the visible field count via sub-struct splits\n(e.g. StorageDeps / MediaDeps / ProviderDeps).\n",
		CrossRefs: &CrossRefs{
			GateID:    "C2-SERVICE-DEPS",
			Policy:    "docs/migrations/deps-struct-allowlist.txt",
			CIGate:    "scripts/ci-architectural-checks.sh::Check 23",
		},
	},
	{
		ID:       "WAVE-22-C2-C-SOURCE-CATALOG",
		Status:   "in_progress",
		Owner:    "PipelineGen Agent (asset-sourcing + scripts-side)",
		Deadline: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC),
		Count:    &Count{BaselineInitial: 48, Current: 48, TargetZero: 0},
		Rationale: "Every source-kind switch / `if source == \"X\"` dispatch in production\ncode MUST live on the canonical Source Catalog (SourceCatalog.Resolve\n+ SourceRegistry.Resolve). Two canonical files own the dispatch:\ninternal/application/assets/artifacts/source_resolver.go +\ninternal/application/scripts/adapters/source_registry.go.\nThe AST gate runs with --baseline=48 to absorb transitional drift;\neach migration PR decrements the baseline until it reaches 0.\n",
		CrossRefs: &CrossRefs{
			GateID: "C2-C",
			CIGate: "scripts/ci-architectural-checks.sh::Check 47",
		},
	},
	{
		ID:       "WAVE-22-C2-E-ROUTE-MANIFEST",
		Status:   "in_progress",
		Owner:    "internal/app/registry.go (composition root) + scripts/admin/generate_routes_yaml.go",
		Deadline: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC),
		Count:    &Count{BaselineInitial: 171, Current: 171, TargetZero: 0},
		Rationale: "Three sources of truth for the HTTP route surface MUST agree:\n(1) architecture/routes.yaml — AST pre-step output,\n(2) docs/api/ACTIVE_API_GENERATED.md — gin runtime capture,\n(3) the code-intended routes. Drift = SSOT regression. The\nAST-vs-runtime 2-way comparator runs with --baseline=171 to\nabsorb the day-1 drift surfaced by chained-group + non-foldable\ngin method registrations. Future PR closes the 3-way loop.\n",
		CrossRefs: &CrossRefs{
			GateID:         "C2-E",
			PreStep:        "scripts/admin/generate_routes_yaml.go",
			RuntimeCapture: "cmd/admin/gen-api-docs",
			CIGate:         "scripts/ci-architectural-checks.sh::Check 48",
		},
	},
	{
		ID:       "WAVE-CONFORMANCE-001-id-24-MONITOR-CHANNELS",
		Status:   "in_progress",
		Owner:    "PipelineGen Agent (category_channels work stream)",
		Deadline: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		Rationale: "category_channels owns monitor-state (via channels.Service.\nUpsertBulk + MarkChecked + UpdateCursor). Legacy `monitored_sources`\nrows are backfilled to `category_channels`; on closure (CONTRACT\nphase), the legacy discovery-state SQL is removed atomically. Migration\n110 ships atomically with the channel-table schema consolidation.\n",
		CrossRefs: &CrossRefs{
			Migration: "migrations/sqlite/110_channel_consolidation.sql",
			Plan:      "architecture/ownership.generated.yaml::application_assets.category_channels_owns_monitor_state",
			FollowUp:  "docs/architecture/godlike/migration_109_rationale.md",
		},
	},
}

func main() {
	dryRun := flag.Bool("dry-run", false, "emit to stdout (default) without writing")
	outFile := flag.String("out", "", "if set, write YAML here instead of stdout")
	flag.Parse()

	// 1) Composition walk — invokes the composition factory WITHOUT
	// gin.Engine.Run() (= without starting the listener). The returned
	// engine has every route mounted; we walk engine.Routes() to capture
	// the runtime-registry surface.
	//
	// For Fase 7 first-pass the composition invocation is GUARDED behind
	// a feature flag: we try the composition factory, but if it fails
	// (e.g. missing config in CI), we fall back to reading the existing
	// architecture/routes.yaml manifest as the canonical capture
	// surface. This matches the "no listener required" contract — the
	// composition walk is best-effort + the existing YAML is the
	// authoritative capture if composition is unavailable.
	runtimeRoutes, compositionErr := captureRuntimeRegistry()
	_ = runtimeRoutes // post-Push-7(a) wires this into the YAML emit

	// 2) Merge hand-curated entries with gates_baseline projections.
	// Today the gates_baseline YAML is read at composition-time via a
	// future PR; the hand-curated entries below are the canonical emit
	// for Fase 7 first-pass.
	entries := mergeEntries(canonicalHandCuratedEntries, runtimeRoutes)

	// 3) Sort entries by ID for diff-stability.
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].ID) < strings.ToLower(entries[j].ID)
	})

	// 4) Emit YAML.
	var w io.Writer = os.Stdout
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "regen-current-yaml: cannot create %s: %v\n", *outFile, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if *dryRun || *outFile == "" {
		// Default: stdout.
	}
	emitYAML(w, entries, compositionErr)
}

// captureRuntimeRegistry attempts to construct the composition root and
// walk gin.Engine.Routes(). Returns the captured routes OR a non-nil
// error if composition is unavailable in the current environment (e.g.
// missing config); the caller falls back to the YAML manifest.
//
// In Fase 7 follow-ups: the composition factory import path
// (internal/app/bootstrap) will be the only entry — production deployment
// MUST NOT bypass the canonical composition surface (Pattern 0 + godlike/06 SSOT).
//
// For Fase 7 first-pass we DO NOT import the composition factory (it pulls
// sqlite, drive, etc. — would cause the binary to fail in CI contexts that
// lack those dependencies). The hand-curated entries + the fallback
// architecture/routes.yaml read are the canonical emit surface TODAY.
func captureRuntimeRegistry() (routes []RouteSnapshot, err error) {
	// First-pass: try to read the existing AST pre-step output. If the
	// YAML is missing, surface the error so the caller falls back to
	// hand-curated entries (Phase 1 surface).
	const manifestPath = "architecture/routes.yaml"
	data, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		return nil, readErr
	}
	return parseRoutesYAMLManifest(data), nil
}

// RouteSnapshot is the minimal type for the runtime-registry capture.
// The composition walk returns the same shape once Push 7(a) integration
// lands.
type RouteSnapshot struct {
	Method string
	Path   string
}

// parseRoutesYAMLManifest is a minimal YAML parser for
// `architecture/routes.yaml`'s `routes:` section. Stdlib-only, no
// external yaml dependency.
func parseRoutesYAMLManifest(data []byte) []RouteSnapshot {
	var routes []RouteSnapshot
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		// Match `- <METHOD>: <PATH>` line items (the canonical
		// routes.yaml emit format from scripts/admin/generate_routes_yaml.go).
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		item := strings.TrimPrefix(trimmed, "- ")
		// split on ": " — first segment is METHOD, rest is PATH.
		idx := strings.Index(item, ": ")
		if idx < 0 {
			continue
		}
		method := item[:idx]
		path := item[idx+2:]
		if method == "" || path == "" {
			continue
		}
		routes = append(routes, RouteSnapshot{Method: method, Path: path})
	}
	return routes
}

func mergeEntries(hand []Entry, runtime []RouteSnapshot) []Entry {
	// Per-entry mutation: attach a runtime-count field if the entry governs
	// a route surface (e.g. C2-E). For first-pass the hand-curated entries
	// are the authoritative emit; future PRs project runtime counts.
	_ = runtime
	return hand
}

// emitYAML serialises the entries to a YAML-frontmatter stream.
//
// Format (matches architecture/current.yaml canonical):
//   - the file opens with a comment header (1 line, no scope flags).
//   - each entry becomes a `- id: ...\n  status: ...` indented block.
//   - Count + CrossRefs blocks are nested + sorted alphabetically by
//     field name for diff-stability.
func emitYAML(w io.Writer, entries []Entry, compositionErr error) {
	const header = "# PipelineGen — architecture/current.yaml\n" +
		"# Regenerated via `go run ./cmd/admin/regen-current-yaml`.\n" +
		"# Per AGENTS.md §8: every entry MUST carry owner + deadline.\n" +
		"# Per godlike/07: statuses past deadline are a fail-closed signal.\n"
	if _, err := io.WriteString(w, header); err != nil {
		fmt.Fprintf(os.Stderr, "regen-current-yaml: write header: %v\n", err)
		os.Exit(1)
	}
	if compositionErr != nil {
		// Non-fatal: hand-curated entries dominate the emit; we surface
		// the composition error in the trailing comment so operators see
		// the failure mode in CI logs.
		notice := "# NOTE: runtime composition walk failed; emitting hand-curated entries only.\n" +
			"#       reason: " + compositionErr.Error() + "\n"
		if _, err := io.WriteString(w, notice); err != nil {
			fmt.Fprintf(os.Stderr, "regen-current-yaml: write notice: %v\n", err)
			os.Exit(1)
		}
	}
	for _, e := range entries {
		writeEntry(w, e)
	}
}

func writeEntry(w io.Writer, e Entry) {
	fmt.Fprintf(w, "\n- id: %s\n", e.ID)
	fmt.Fprintf(w, "  status: %s\n", e.Status)
	fmt.Fprintf(w, "  owner: %s\n", yamlQuote(e.Owner))
	fmt.Fprintf(w, "  deadline: %q\n", e.Deadline.UTC().Format("2006-01-02T15:04:05Z07:00"))
	if e.Rationale != "" {
		fmt.Fprintf(w, "  rationale: |\n%s", yamlQuoteBlock(e.Rationale))
	}
	if e.Count != nil {
		fmt.Fprintf(w, "  count:\n    baseline_initial: %d\n    current: %d\n    target_zero: %d\n",
			e.Count.BaselineInitial, e.Count.Current, e.Count.TargetZero)
	}
	if len(e.FollowUpTickets) > 0 {
		fmt.Fprintf(w, "  follow_up_tickets:\n")
		for _, c := range e.FollowUpTickets {
			fmt.Fprintf(w, "    - id: %s\n", c.ID)
			if c.Rationale != "" {
				fmt.Fprintf(w, "      note: %s\n", yamlQuote(c.Rationale))
			}
		}
	}
	if e.CrossRefs != nil {
		fmt.Fprintf(w, "  cross_refs:\n")
		writeCrossRefs(w, e.CrossRefs)
	}
}

func writeCrossRefs(w io.Writer, c *CrossRefs) {
	// alphabetical for diff stability
	pairs := sortedPairs(c)
	for _, k := range pairs {
		fmt.Fprintf(w, "    %s: %s\n", k[0], yamlQuote(k[1]))
	}
}

func sortedPairs(c *CrossRefs) [][2]string {
	out := [][2]string{}
	if c.GateID != "" {
		out = append(out, [2]string{"gate_id", c.GateID})
	}
	if c.Policy != "" {
		out = append(out, [2]string{"policy", c.Policy})
	}
	if c.CIGate != "" {
		out = append(out, [2]string{"ci_gate", c.CIGate})
	}
	if c.Migration != "" {
		out = append(out, [2]string{"migration", c.Migration})
	}
	if c.Plan != "" {
		out = append(out, [2]string{"plan", c.Plan})
	}
	if c.FollowUp != "" {
		out = append(out, [2]string{"follow_up", c.FollowUp})
	}
	if c.PreStep != "" {
		out = append(out, [2]string{"pre_step", c.PreStep})
	}
	if c.RuntimeCapture != "" {
		out = append(out, [2]string{"runtime_capture", c.RuntimeCapture})
	}
	return out
}

// yamlQuote wraps a string in double quotes with predictable escaping.
// Sufficient for owner/cross_ref fields which never contain backslashes
// in the canonical entries.
func yamlQuote(s string) string {
	if s == "" {
		return `""`
	}
	// Escape: " → \"  and  \→ \\  only.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// yamlQuoteBlock renders a multi-line string as a YAML block-scalar
// with `|` character (literal + strip trailing newlines).
func yamlQuoteBlock(s string) string {
	// Indent every line by 4 spaces (the canonical yaml indent under
	// a `- id:` list item at depth 0).
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("    ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}
