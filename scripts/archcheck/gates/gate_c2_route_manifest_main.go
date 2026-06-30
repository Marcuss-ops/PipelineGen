// Package main implements the Blocco C2-E architectural gate (June 2026):
//
//	every route declared in the canonical manifest MUST agree with the
//	generated API docs:
//
//	  - canonical manifest:        architecture/routes.yaml
//	  - generated API docs:        docs/api/ACTIVE_API_GENERATED.md
//
//	Both artifacts are derived from the same internal/api/**/RegisterRoutes
//	source:
//
//	  - routes.yaml             — pre-step generator output (AST-scan of
//	                              internal/api/**/RegisterRoutes bodies,
//	                              see scripts/admin/generate_routes_yaml.go
//	                              for the canonical generator; current
//	                              implementation transcribes the canonical
//	                              module route map from
//	                              architecture/ownership/modules.yaml)
//	  - ACTIVE_API_GENERATED.md — runtime capture via `gin.Engine.Routes()`
//	                              built by cmd/admin/gen_api_docs.go (uses
//	                              the canonical app.WireServices + router.Setup()
//	                              boot path)
//
//	A canonical manifest + canonical runtime capture MUST agree for any
//	given state of the codebase; if they disagree, one of two things is
//	wrong: either the pre-step generator has drifted from the source code,
//	or the runtime has gained/lost routes without the manifest catching
//	up. Both are SSOT regressions for the `route ≡ manifest ≡ docs`
//	invariant documented in godlike/09 §"zero-legacy-policy" + AGENTS.md
//	Pattern 8.
//
//	Detected diff kinds:
//
//	  1. MANIFEST-ONLY route: in routes.yaml but absent from generated docs.
//	     Symptom: a route registers at runtime but the docs are stale
//	     OR the pre-step generator produced a phantom route that never
//	     reaches the gin engine.
//	  2. DOCS-ONLY route: in generated docs but absent from manifest.
//	     Symptom: gin.Engine.Routes() surfaces a route that the canonical
//	     capability manifest does not list — typically a NEW ad-hoc route
//	     landed bypassing the canonical composition.
//
//	Method mismatch (same path registered with different HTTP methods
//	across manifest and docs) is surfaced implicitly as separate
//	manifest-only / docs-only rows. The method-mismatch aggregation is
//	left as a future PR refinement.
//
//	Out-of-scope for this PR (documented followups):
//
//	  - AST-vs-runtime equivalence verification — i.e. ensuring the
//	    pre-step generator's AST scan against internal/api/**/RegisterRoutes
//	    agrees with cmd/admin/gen_api_docs.go's `engine.Routes()` capture.
//	    The nested-group prefix folding (rg.Group("/api/foo").POST("/bar"))
//	    is non-trivial; a future PR introduces the folded-prefix AST
//	    detection as a third-source check on top of the present
//	    manifest+docs comparison. Today the pre-step generator transcribes
//	    the canonical module route map so the manifest is structurally
//	    aligned with the runtime tree by construction.
//
//	Walker scope: none (this gate is purely artifact-to-artifact).
//
//	Exit codes: 0 (no violations), 1 (mismatches found), 2 (invocation error).
//go:build c2_route_manifest

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifestDocument is the parsed shape of architecture/routes.yaml.
type manifestDocument struct {
	Routes []manifestRoute `yaml:"routes"`
}

type manifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Source string `yaml:"source,omitempty"`
}

// routeKey is the canonical (method, path) identifier used for set
// membership. Same path with different methods produces TWO distinct
// keys (the gate reports these as separate violation rows rather than
// deduping on path; semantic-method-mismatch detection is out of scope
// for this PR).
type routeKey struct {
	method string
	path   string
}

func (k routeKey) String() string {
	return fmt.Sprintf("%s %s", k.method, k.path)
}

// main -- orchestrate the two-source comparison. The gate's two inputs
// are produced by separate pipelines:
//
//  1. routes.yaml  — written by scripts/admin/generate_routes_yaml.go
//     (AST scan of internal/api/**/RegisterRoutes bodies),
//     normally executed as a pre-step by cmd/server's
//     gen-routes CLI subcommand or by CI's pre-Check-49
//     hook (see scripts/ci-architectural-checks.sh:49).
//  2. ACTIVE_API_GENERATED.md — written by cmd/admin/gen_api_docs.go
//     (runtime gin.Engine.Routes() capture).
//
// Both generators expect the SAME source code at the SAME point in
// time; CI runs the generators in the correct order before invoking
// Check 49, so the freshness invariant holds.
func main() {
	var root string
	flag.StringVar(&root, "root", ".", "repo root (defaults to current directory)")
	flag.Parse()

	manifestPath := filepath.Join(root, "architecture", "routes.yaml")
	docsPath := filepath.Join(root, "docs", "api", "ACTIVE_API_GENERATED.md")

	manifestSet, err := loadManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-E gate (load manifest): %v\n", err)
		os.Exit(2)
	}

	docsSet, err := loadGeneratedDocs(docsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-E gate (load generated docs): %v\n", err)
		os.Exit(2)
	}

	violations := computeDiff(manifestSet, docsSet)

	if len(violations) == 0 {
		fmt.Fprintf(os.Stderr,
			"C2-E gate: 0 violations — routes (manifest: %d) ≡ generated docs (%d)\n",
			len(manifestSet), len(docsSet))
		return
	}

	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].kind != violations[j].kind {
			return violations[i].kind < violations[j].kind
		}
		return violations[i].route.String() < violations[j].route.String()
	})

	for _, v := range violations {
		fmt.Printf("%s: %s\n", v.kind, v.route)
	}
	fmt.Fprintf(os.Stderr,
		"\nC2-E gate: %d violation(s) — manifest (%d) vs generated docs (%d) disagree\n",
		len(violations), len(manifestSet), len(docsSet))
	fmt.Fprintf(os.Stderr,
		"Remediation:\n"+
			"  1. If a route is 'manifest-only', regenerate ACTIVE_API_GENERATED.md via\n"+
			"     `go run ./cmd/admin gen-api-docs` (mirrors the canonical CI flow).\n"+
			"  2. If a route is 'docs-only', it likely was added bypassing the canonical\n"+
			"     composition root. Either migrate to the canonical RegisterRoutes site\n"+
			"     (godlike/07 EXPAND)), or transpose the route into the pre-step-generated\n"+
			"     routes.yaml + scripts/admin/generate_routes_yaml.go source so the\n"+
			"     canonical agreement is preserved.\n")
	os.Exit(1)
}

type violation struct {
	kind  string // manifest-only | docs-only
	route routeKey
	clue  string // optional supplement e.g. the source field from manifest
}

func loadManifest(path string) (map[routeKey]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing manifest = unresolved pre-step. The CI runner
			// must surface the missing-file root cause distinctly
			// from a violation row, so we return a non-nil error here
			// on first encounter (unlike the docs parser, which is
			// treated as empty-set for forward-compat with brand-new
			// codebases). The CI runner catches this exit-2 case and
			// instructs the operator to run the pre-step.
			return nil, nil, fmt.Errorf("missing manifest at %s — run `go run ./cmd/admin gen-api-routes` (or the equivalent AST-scan pre-step generator) to populate", path)
		}
		return nil, nil, err
	}
	var doc manifestDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	set := make(map[routeKey]bool, len(doc.Routes))
	for _, r := range doc.Routes {
		key := routeKey{method: strings.ToUpper(r.Method), path: r.Path}
		set[key] = true
	}
	return set, nil
}

// loadGeneratedDocs parses ACTIVE_API_GENERATED.md by finding the
// route tables (lines of the form `| METHOD | `/path` | ...`). The
// parser is regex-driven because the docs are generated as markdown
// from gin.RouteInfo slices (NOT structured YAML).
func loadGeneratedDocs(path string) (map[routeKey]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[routeKey]bool{}, nil
		}
		return nil, err
	}
	out := make(map[routeKey]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	rowRe := regexp.MustCompile(`^\|\s*([A-Z]+)\s*\|\s*\x60(/[^\x60]*)\x60\s*\|\s*(.*?)\s*\|\s*$`)
	for scanner.Scan() {
		line := scanner.Text()
		m := rowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(m[1]))
		path := strings.TrimSpace(m[2])
		if method == "METHOD" || strings.HasPrefix(method, "--") {
			// Skip the table-header row (`| Method | Path | Description |`)
			// and MD table separator rows (`|----|-----|-------|`).
			continue
		}
		out[routeKey{method: method, path: path}] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// computeDiff returns the symmetric difference between the manifest
// set and the docs set. Method-keyed set membership means same path +
// different methods produce two distinct violation rows (manifest-only
// vs docs-only); callers who want method-mismatch aggregation can
// post-process the output.
func computeDiff(manifestSet, docsSet map[routeKey]bool) []violation {
	var out []violation
	for r := range manifestSet {
		if !docsSet[r] {
			out = append(out, violation{kind: "manifest-only", route: r})
		}
	}
	for r := range docsSet {
		if !manifestSet[r] {
			out = append(out, violation{kind: "docs-only", route: r})
		}
	}
	return out
}
