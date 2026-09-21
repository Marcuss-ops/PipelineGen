package rendering

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docsDescriptionRow matches one generated markdown table row:
//
//	| METHOD | `/path` | description |
//
// The pattern is deliberately the same shape the C2-E route-manifest gate
// reads (cmd/archcheck/gates/gate_c2_route_manifest_main.go), so the two
// consumers cannot disagree about what a generated row looks like.
var docsDescriptionRow = regexp.MustCompile("^\\|\\s*([A-Z]+)\\s*\\|\\s*`(/[^`]*)`\\s*\\|\\s*(.*?)\\s*\\|\\s*$")

// TestCommittedAPIDocsDescriptionsMatchGenerator is the local half of the CI
// "generated docs are up to date" step.
//
// CI regenerates docs/api/ACTIVE_API_GENERATED.md through the live router and
// diffs it. That check is the strongest one available, but it needs the whole
// composition root to wire (PostgreSQL media SSOT among it) and it fails on
// ANY drift, so the cheapest way to notice a wrong description — the committed
// docs claiming `POST /api/drive/folders` "List Drive folders" while the
// handler creates them — was to run the full generator.
//
// This test closes that window without wiring anything: it reads the committed
// table, re-derives each row's description through the same getDescription
// lookup the generator uses, and fails on any mismatch. A description edit in
// routeDescriptions that is not reflected in the committed markdown (or a
// hand-edited markdown row that disagrees with the map) is caught here in
// milliseconds.
func TestCommittedAPIDocsDescriptionsMatchGenerator(t *testing.T) {
	path := filepath.Join(docsSyncProjectRoot(t), "docs", "api", "ACTIVE_API_GENERATED.md")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open committed API docs: %v", err)
	}
	defer f.Close()

	rows := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		match := docsDescriptionRow.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		method, route, committed := match[1], match[2], match[3]
		if method == "METHOD" || strings.HasPrefix(method, "--") {
			continue
		}
		rows++
		if want := getDescription(route, method); want != committed {
			t.Errorf("%s %s: committed docs say %q, routeDescriptions says %q — regenerate with `go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md`",
				method, route, committed, want)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan committed API docs: %v", err)
	}
	if rows == 0 {
		t.Fatalf("no route rows parsed from %s: the parse pattern or the committed file changed shape, and this test would pass vacuously", path)
	}
}

// docsSyncProjectRoot walks up from the package directory to the module root
// (the directory holding go.mod and the generated docs), so the test does not
// hardcode a depth that would silently break if the package moved.
func docsSyncProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "docs", "api", "ACTIVE_API_GENERATED.md")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate the module root holding docs/api/ACTIVE_API_GENERATED.md")
		}
		dir = parent
	}
}
