package workflow

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
)

// storage_search_contract_test.go locks the B4 migration contract so
// future contributors cannot regress to inline http.NewRequest calls.
//
// PR-IMAGES-AI-VS-NORMAL-PLAN B4 (July 2026): replaced 9 inline
// http.NewRequest + Do + ReadAll copies in storage_search.go with
// canonical pkg/httpjson.GetJSON / GetBytes single-call sites. The
// godoc on pkg/httpjson/get_json.go cites the post-migration exit-gate:
// `rg "http.NewRequest" storage_search → 0`.
//
// Two forward-prevention layers lock the contract (godlike/06 SSOT:
// one-fact two-layers):
//   - In-process test (this file) — compile-time pin of pkg/httpjson
//     surface + runtime source-file scan over the package directory.
//   - CI Gate (Check 63 in scripts/ci-architectural-checks.sh) — fail-closed
//     on http.NewRequest reintroductions in storage_search.go specifically.
//
// Lockstep note (per godlike/06 SSOT one-owner-per-fact): the test
// covers the WHOLE package directory (any future split to
// storage_search_wikipedia.go + storage_search_searxng.go +
// storage_search_ddg.go stays regression-locked), the CI gate covers
// the canonical storage_search.go specifically (mirrors B4's
// exit-gate one-for-one). A future contributor that adds a
// new file to the package MUST update BOTH layers if they intend to
// re-introduce http.NewRequest.

// forbiddenHTTPNewRequestCall is the function-call shape banned in
// package images post-B4. The open-paren suffix is load-bearing:
// matching `http.NewRequest` alone would catch string literals
// (e.g. a log line `s.log.Info("falling back to
// http.NewRequestWithContext for %s")`) as false-positives.
//
// forbiddenHTTPNewRequestStruct covers the address-of struct-literal
// shape `&http.NewRequest{...}` which is also forbidden post-B4
// (no legitimate reason to take the constructor's address either).
// The CI gate regex `http\.NewRequest[\({]` matches BOTH shapes.
const (
	forbiddenHTTPNewRequestCall   = "http.NewRequest("
	forbiddenHTTPNewRequestStruct = "&http.NewRequest{"
)

// ── Test 1: pkg/httpjson exports GetJSON[T] with the canonical signature ──
//
// Compile-time pin via typed assignment. Signature drift in the
// canonical surface surfaces as a build failure, not a runtime panic.
func TestPkgHTTPJSON_GetJSONExported(t *testing.T) {
	var _ func(
		ctx context.Context,
		client httpjson.Client,
		targetURL string,
		opts *httpjson.Options,
	) (map[string]any, error) = httpjson.GetJSON[map[string]any]

	opts := &httpjson.Options{UserAgent: "test-ua"}
	if opts.UserAgent != "test-ua" {
		t.Fatalf("httpjson.Options.UserAgent not settable: got %q", opts.UserAgent)
	}

	se := &httpjson.StatusError{URL: "https://example.com/x", StatusCode: 503, Body: []byte("overload")}
	if se.StatusCode != 503 || se.URL != "https://example.com/x" || string(se.Body) != "overload" {
		t.Fatalf("httpjson.StatusError field drift: got %+v", se)
	}
}

// ── Test 2: storage_search.go has zero http.NewRequest references ───
//
// Runtime source-file scan over the package directory (cwd-independent
// via runtime.Caller(0) anchoring). Catches both function-call shape
// (`http.NewRequest(`) AND address-of struct-literal shape
// (`&http.NewRequest{`).
func TestStorageSearch_NoInlineHTTPNewRequest(t *testing.T) {
	violations := scanPackageForHTTPNewRequest(t)
	if len(violations) > 0 {
		t.Fatalf("B4 migration lock violated: http.NewRequest( or &http.NewRequest{ found in package (post-B4 contract = 0):\n  %s\n"+
			"Fix: route the call through pkg/httpjson.GetJSON[T] or pkg/httpjson.GetBytes "+
			"(the canonical single-call surface). If the call is genuinely a streaming upload "+
			"(GetBytes cannot handle), prepend an ARCH-ALLOWLIST marker per Check 54 git etiquette "+
			"and document the justification in the PR body.",
			strings.Join(violations, "\n  "))
	}
}

// scanPackageForHTTPNewRequest walks the package directory containing
// this test file and returns every line whose content (excluding
// // {@code /*/* leading comments) matches the B4-forbidden call shape.
func scanPackageForHTTPNewRequest(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("scanPackageForHTTPNewRequest: runtime.Caller(0) unavailable")
	}
	pkgDir := thisFile[:strings.LastIndex(thisFile, "/")]
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("scanPackageForHTTPNewRequest: cannot read %s: %v", pkgDir, err)
	}
	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		fpath := pkgDir + "/" + entry.Name()
		raw, err := os.ReadFile(fpath)
		if err != nil {
			t.Fatalf("scanPackageForHTTPNewRequest: cannot read %s: %v", fpath, err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if !strings.Contains(line, forbiddenHTTPNewRequestCall) && !strings.Contains(line, forbiddenHTTPNewRequestStruct) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			violations = append(violations, fpath+":"+strconv.Itoa(i+1)+": "+line)
		}
	}
	return violations
}
