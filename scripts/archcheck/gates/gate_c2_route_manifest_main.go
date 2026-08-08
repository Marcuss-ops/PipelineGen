//go:build c2_route_manifest

// Command gate_c2_route_manifest compares the runtime-captured route manifest
// with generated API docs. Its ceiling is derived from HEAD^ and dated milestones;
// the legacy --baseline flag is only a bootstrap fallback when no parent commit
// is available, eliminating duplicated hand-maintained current counts.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type manifestDocument struct {
	Routes []manifestRoute `yaml:"routes"`
}

type manifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

type routeKey struct{ method, path string }

func (k routeKey) String() string { return k.method + " " + k.path }

type violation struct {
	kind  string
	route routeKey
}

type milestone struct {
	due time.Time
	cap int
}

var docsRow = regexp.MustCompile(`^\|\s*([A-Z]+)\s*\|\s*\x60(/[^\x60]*)\x60\s*\|\s*(.*?)\s*\|\s*$`)

func main() {
	var root string
	var legacyBaseline int
	flag.StringVar(&root, "root", ".", "repo root")
	flag.IntVar(&legacyBaseline, "baseline", 0, "legacy bootstrap ceiling; used only when HEAD^ is unavailable")
	flag.Parse()

	manifestPath := filepath.Join(root, "architecture", "routes.yaml")
	docsPath := filepath.Join(root, "docs", "api", "ACTIVE_API_GENERATED.md")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		fatal2("load manifest", err)
	}
	docsData, err := os.ReadFile(docsPath)
	if err != nil {
		fatal2("load generated docs", err)
	}
	current, manifestCount, docsCount, err := diffData(manifestData, docsData)
	if err != nil {
		fatal2("parse current artifacts", err)
	}

	parentCount, parentOK, err := parentDiffCount(root)
	if err != nil {
		fatal2("scan parent artifacts", err)
	}
	ceiling := legacyBaseline
	ceilingSource := "legacy bootstrap"
	if parentOK {
		ceiling = parentCount
		ceilingSource = "HEAD^ artifact scan"
	}
	if dueCap, ok := activeMilestoneCap(time.Now()); ok && (ceiling == 0 || dueCap < ceiling) {
		ceiling = dueCap
		ceilingSource += "+dated milestone"
	}

	sort.Slice(current, func(i, j int) bool {
		if current[i].kind != current[j].kind {
			return current[i].kind < current[j].kind
		}
		return current[i].route.String() < current[j].route.String()
	})

	if len(current) > ceiling {
		for _, v := range current {
			fmt.Printf("%s: %s\n", v.kind, v.route.String())
		}
		fmt.Fprintf(os.Stderr, "C2-E gate: current=%d exceeds ceiling=%d (%s); parent=%d, manifest=%d, docs=%d. Growth is forbidden.\n",
			len(current), ceiling, ceilingSource, parentCount, manifestCount, docsCount)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "C2-E gate: current=%d ceiling=%d (%s) parent=%d manifest=%d docs=%d target=0\n",
		len(current), ceiling, ceilingSource, parentCount, manifestCount, docsCount)
}

func activeMilestoneCap(now time.Time) (int, bool) {
	utc := now.UTC()
	milestones := []milestone{
		{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), 128},
		{time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), 86},
		{time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 43},
		{time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), 0},
	}
	cap, active := 0, false
	for _, m := range milestones {
		if !utc.Before(m.due) {
			cap, active = m.cap, true
		}
	}
	return cap, active
}

func parentDiffCount(root string) (int, bool, error) {
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD^").Run(); err != nil {
		return 0, false, nil
	}
	manifest, err := exec.Command("git", "-C", root, "show", "HEAD^:architecture/routes.yaml").Output()
	if err != nil {
		return 0, false, err
	}
	docs, err := exec.Command("git", "-C", root, "show", "HEAD^:docs/api/ACTIVE_API_GENERATED.md").Output()
	if err != nil {
		return 0, false, err
	}
	violations, _, _, err := diffData(manifest, docs)
	return len(violations), true, err
}

func diffData(manifestData, docsData []byte) ([]violation, int, int, error) {
	manifestSet, err := loadManifest(manifestData)
	if err != nil {
		return nil, 0, 0, err
	}
	docsSet, err := loadGeneratedDocs(docsData)
	if err != nil {
		return nil, 0, 0, err
	}
	return computeDiff(manifestSet, docsSet), len(manifestSet), len(docsSet), nil
}

func loadManifest(data []byte) (map[routeKey]bool, error) {
	var doc manifestDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	set := make(map[routeKey]bool, len(doc.Routes))
	for _, route := range doc.Routes {
		set[routeKey{strings.ToUpper(strings.TrimSpace(route.Method)), strings.TrimSpace(route.Path)}] = true
	}
	return set, nil
}

func loadGeneratedDocs(data []byte) (map[routeKey]bool, error) {
	set := make(map[routeKey]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		match := docsRow.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(match[1]))
		if method == "METHOD" || strings.HasPrefix(method, "--") {
			continue
		}
		set[routeKey{method, strings.TrimSpace(match[2])}] = true
	}
	return set, scanner.Err()
}

func computeDiff(manifestSet, docsSet map[routeKey]bool) []violation {
	var out []violation
	for route := range manifestSet {
		if !docsSet[route] {
			out = append(out, violation{"manifest-only", route})
		}
	}
	for route := range docsSet {
		if !manifestSet[route] {
			out = append(out, violation{"docs-only", route})
		}
	}
	return out
}

func fatal2(label string, err error) {
	fmt.Fprintf(os.Stderr, "C2-E gate: %s: %v\n", label, err)
	os.Exit(2)
}
