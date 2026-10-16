package postgres

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// metricnamesSource locates the worker module's canonical metric vocabulary.
func metricnamesSource(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the test source; skipping worker metric-unit projection")
	}
	// <repo>/queue/internal/repository/postgres -> <repo>/renderinggen/...
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "..",
		"renderinggen", "internal", "metricnames", "metricnames.go")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("worker metric vocabulary not checked out (%v); skipping metric-unit projection", err)
	}
	return filepath.Clean(path)
}

var (
	metricConstDecl = regexp.MustCompile(`(?m)^\s*([A-Za-z_]\w*)\s*=\s*"([^"]*)"`)
	metricVocabPair = regexp.MustCompile(`([A-Za-z_]\w*)\s*:\s*([A-Za-z_]\w*|"[^"]*")`)
)

// TestMetricUnitProjectsWorkerVocabulary pins the one cross-module projection an
// import cannot enforce: the queue derives the `unit` column of
// processing_metrics from the metric NAME (metricUnit), while the worker
// declares name→unit in renderinggen/internal/metricnames. The queue module
// cannot import the worker (the dependency already runs worker→queue), so the
// two could drift with no compiler or test to catch it — a worker metric whose
// suffix stops matching its declared unit would be persisted under the wrong
// unit, silently, and the ledger would then report milliseconds as counts.
//
// The worker's declaration is the canonical owner, so this test reads it and
// asserts the queue derives exactly the declared unit for every name. When the
// sibling module is absent the test SKIPS visibly, like the other cross-repo
// pins.
func TestMetricUnitProjectsWorkerVocabulary(t *testing.T) {
	raw, err := os.ReadFile(metricnamesSource(t))
	if err != nil {
		t.Fatal(err)
	}

	consts := map[string]string{}
	for _, m := range metricConstDecl.FindAllSubmatch(raw, -1) {
		consts[string(m[1])] = string(m[2])
	}

	marker := []byte("var vocab = map[string]string{")
	start := bytes.Index(raw, marker)
	if start < 0 {
		t.Fatal("metricnames.go no longer declares `var vocab = map[string]string{`; re-point this projection pin")
	}
	block := raw[start:]
	if end := bytes.Index(block, []byte("\n}")); end >= 0 {
		block = block[:end]
	}

	resolve := func(token string) (string, bool) {
		if strings.HasPrefix(token, `"`) {
			return strings.Trim(token, `"`), true
		}
		value, ok := consts[token]
		return value, ok
	}

	entries := 0
	unitsSeen := map[string]int{}
	for _, m := range metricVocabPair.FindAllSubmatch(block, -1) {
		name, ok := resolve(string(m[1]))
		if !ok {
			t.Fatalf("vocabulary key %q is not a declared constant; re-point this projection pin", m[1])
		}
		want, ok := resolve(string(m[2]))
		if !ok {
			t.Fatalf("vocabulary unit %q is not a declared constant; re-point this projection pin", m[2])
		}
		entries++
		unitsSeen[want]++
		if got := metricUnit(name); got != want {
			t.Errorf("metricUnit(%q) = %q but the worker declares %q — the queue would persist the wrong unit",
				name, got, want)
		}
	}

	// Anti-vacuity: a parse that silently matched nothing must fail rather than
	// report a green projection pin.
	if entries < 50 {
		t.Fatalf("only %d vocabulary entries parsed; the projection pin is no longer covering the worker vocabulary", entries)
	}
	// Every unit-deriving rule in metricUnit must actually be exercised.
	for _, unit := range []string{"us", "ms", "bytes", "count"} {
		if unitsSeen[unit] == 0 {
			t.Fatalf("no worker metric declares unit %q; the projection pin no longer exercises metricUnit's %q rule", unit, unit)
		}
	}
}

// TestMetricUnitFallbackTableForUndeclaredNames pins the SECOND half of
// metricUnit: names that are not in the worker's declared vocabulary (a
// payload from a producer that predates it, or a key added before it is
// registered). Those rows would otherwise all be persisted as "count", so the
// suffix table is a real contract about what a stored row claims — and it was
// previously unreachable from the vocabulary projection test, which only walks
// declared names.
func TestMetricUnitFallbackTableForUndeclaredNames(t *testing.T) {
	cases := []struct {
		name string
		want string
		why  string
	}{
		{"legacy_upload_us", "us", "microsecond suffix"},
		{"legacy_encode_ms", "ms", "millisecond suffix"},
		{"artifact_bytes", "bytes", "byte count"},
		{"legacy_payload_mb", "mb", "megabyte count"},
		{"steady_avg_fps", "fps", "frame rate"},
		{"compression_ratio", "ratio", "dimensionless ratio"},
		{"cache_hit_percent", "ratio", "percentage"},
		{"ENTITY_COUNT", "count", "case-insensitive, no unit suffix"},
		{"", "count", "empty name is a count by default"},
	}
	for _, tc := range cases {
		if got := metricUnit(tc.name); got != tc.want {
			t.Errorf("metricUnit(%q) = %q, want %q (%s)", tc.name, got, tc.want, tc.why)
		}
	}
	// The suffix rules must win over the content rules: a name that carries
	// BOTH ("_ms" and "bytes") is a duration, not a byte count.
	if got := metricUnit("legacy_bytes_ms"); got != "ms" {
		t.Errorf("metricUnit(\"legacy_bytes_ms\") = %q, want \"ms\" (the suffix rule owns the unit)", got)
	}
}
