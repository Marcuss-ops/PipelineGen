package sqlite

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The runtime scope authority (parseMigrationScope in migrations_discovery.go)
// falls back to scope="all" on an unknown token by design: a silently EXCLUDED
// migration is worse than an over-applied one. But that fallback also means a
// TYPO in a `-- database:` header (`-- database: primario`) would widen the
// migration to every target DB without any signal at apply time.
//
// This audit closes that gap at discovery time instead: every scope header
// across the three migration planes must be lexically valid, so the
// unknown-token fallback can only ever be the documented "no directive"
// default — never a typo. Wrong scopes fail the test build, not the next
// migration run.

const (
	// scopeAuditRootLimit bounds the upward search for the repository root
	// (go.mod) so a mislaunched test binary errors instead of scanning the
	// filesystem at large.
	scopeAuditRootLimit = 6
)

func scopeAuditRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("scope audit: getwd: %v", err)
	}
	dir := wd
	for i := 0; i < scopeAuditRootLimit; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("scope audit: cannot locate repository root (go.mod) above %s", wd)
	return ""
}

// parseScopeHeaderDirective mirrors parseMigrationScope's scanning shape
// (leading `--` comment lines only, first non-comment line ends the scan) but
// returns the RAW directive text so the audit can reject anything the runtime
// parser would silently normalise into "all".
func parseScopeHeaderDirective(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scope audit: read %s: %v", path, err)
	}
	content := string(raw)
	// Same BOM handling as parseMigrationScope: without it a BOM would be
	// joined to the first token and every directive would look invalid.
	content = strings.TrimPrefix(content, "\uFEFF")
	var directives []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "--") {
			// First non-comment line: the scan window is over, exactly like
			// the runtime parser.
			break
		}
		if idx := strings.Index(strings.ToLower(trimmed), "-- database:"); idx == 0 {
			rest := strings.TrimSpace(trimmed[len("-- database:"):])
			directives = append(directives, rest)
		}
	}
	return directives
}

func assertScopeTokens(t *testing.T, path string, directive string, allowed map[string]bool) {
	t.Helper()
	if directive == "" {
		t.Errorf("%s: empty `-- database:` directive — either drop the line or declare a scope", path)
		return
	}
	for _, tok := range strings.Split(strings.ToLower(directive), ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			t.Errorf("%s: `-- database: %q` has an empty token between commas", path, directive)
			continue
		}
		if !allowed[tok] {
			t.Errorf("%s: unknown scope token %q in directive %q — the runtime parser would silently fall back to \"all\"; fix the header (allowed: primary|observability|cache|jobs|all)", path, tok, directive)
		}
	}
}

// TestMigrationScopeHeadersEnumeratedTokens audits every .sql in
// migrations/sqlite: any declared `-- database:` header must use only the
// known scope vocabulary, and the runtime parser must agree with the raw
// lexical scope (it falls back to "all" for unknown tokens, so a disagreement
// is exactly the silent-widening failure mode this audit bans).
func TestMigrationScopeHeadersEnumeratedTokens(t *testing.T) {
	root := scopeAuditRepoRoot(t)
	dir := filepath.Join(root, "migrations", "sqlite")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("scope audit: read dir: %v", err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		t.Fatalf("scope audit: no .sql files under %s — probe the census in docs/migrations/PLANE_INVENTORY.md", dir)
	}
	sort.Strings(files)
	for _, name := range files {
		path := filepath.Join(dir, name)
		directives := parseScopeHeaderDirective(t, path)
		switch len(directives) {
		case 0:
			// No directive: the runtime default is "all"; nothing to audit.
		case 1:
			assertScopeTokens(t, path, directives[0], map[string]bool{
				"primary": true, "observability": true, "cache": true, "jobs": true, "all": true,
			})
		default:
			t.Errorf("%s: %d `-- database:` directives in the header window — at most one is authoritative (parseMigrationScope uses the first)", path, len(directives))
		}
	}
}

// TestMigrationScopeHeadersRuntimeParserAgreement pins that the lexical scope
// read from the file equals what the runtime parser produces, so the
// documented "unknown token → all" fallback can never diverge from the
// headers this audit accepts.
func TestMigrationScopeHeadersRuntimeParserAgreement(t *testing.T) {
	root := scopeAuditRepoRoot(t)
	dir := filepath.Join(root, "migrations", "sqlite")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("scope audit: read dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		path := filepath.Join(dir, name)
		directives := parseScopeHeaderDirective(t, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("scope audit: read %s: %v", path, err)
		}
		got := parseMigrationScope(raw)
		if len(directives) == 0 {
			if got != "all" {
				t.Errorf("%s: no header directive but parser returned %q — want \"all\" default", path, got)
			}
			continue
		}
		want := strings.Join(strings.Split(strings.ToLower(directives[0]), ","), ",")
		var normalized []string
		for _, tok := range strings.Split(want, ",") {
			normalized = append(normalized, strings.TrimSpace(tok))
		}
		if got != strings.Join(normalized, ",") {
			t.Errorf("%s: header %q but parsed scope %q — parser and header diverged", path, directives[0], got)
		}
	}
}

// TestMigrationJobsPlaneScopeSelfDocumentation keeps the standalone jobs
// plane honest: its files may scope themselves only to "jobs" (that is
// literally the DB that store wires in internal/platform/config), so a file
// scoped "primary" there is a copy-paste, not intent.
func TestMigrationJobsPlaneScopeSelfDocumentation(t *testing.T) {
	root := scopeAuditRepoRoot(t)
	dir := filepath.Join(root, "migrations", "sqlite_jobs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("scope audit: read dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		for _, directive := range parseScopeHeaderDirective(t, path) {
			for _, tok := range strings.Split(strings.ToLower(directive), ",") {
				tok = strings.TrimSpace(tok)
				if tok != "" && tok != "jobs" {
					t.Errorf("%s: jobs-plane migration scoped to %q — the standalone jobs plane is applied by the jobs store only; scope every file here to jobs (or drop the header)", path, tok)
				}
			}
		}
	}
}

// TestMigrationPostgresPlaneCarriesNoScopeHeaders pins the boundary of the
// scope system: the PostgreSQL media plane is applied by the deployment
// runner / embedded self-bootstrap sequence (see
// migrations/postgres/embed_ddl.go), which has no targetDB concept at all.
// A scope header there would be dead text implying a live selector.
func TestMigrationPostgresPlaneCarriesNoScopeHeaders(t *testing.T) {
	root := scopeAuditRepoRoot(t)
	dir := filepath.Join(root, "migrations", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("scope audit: read dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if directives := parseScopeHeaderDirective(t, path); len(directives) > 0 {
			t.Errorf("%s: %d `-- database:` directive(s) in the postgres media plane — that plane has no targetDB selector; the header is dead text", path, len(directives))
		}
	}
}

// TestMigrationScopeAuditCensusForDoc reports the census referenced by
// docs/migrations/PLANE_INVENTORY.md. It runs under the normal audit only
// when -v so the numbers stay visible without making the gate brittle: the
// census is documentation, not a pinned contract (file counts change with
// every migration wave; the header VOCABULARY is what must stay closed).
func TestMigrationScopeAuditCensusForDoc(t *testing.T) {
	root := scopeAuditRepoRoot(t)
	tokens := map[string]int{}
	total := 0
	dir := filepath.Join(root, "migrations", "sqlite")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		total++
		for _, directive := range parseScopeHeaderDirective(t, filepath.Join(dir, e.Name())) {
			for _, tok := range strings.Split(strings.ToLower(directive), ",") {
				tok = strings.TrimSpace(tok)
				if tok != "" {
					tokens[tok]++
				}
			}
		}
	}
	t.Logf("census @audit-date: %d sqlite files; declared tokens: %v", total, tokens)
	if total == 0 {
		t.Fatal("census found zero files — probe the sweep")
	}
	if _, ok := tokens["primary"]; !ok {
		t.Errorf("census: expected at least one `primary`-scoped file (the largest declared token)")
	}
}
