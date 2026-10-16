package migrate

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/migrations"
)

// TestSplitStatementsUnderstandsLiteralContexts pins the constructs where a
// semicolon is text rather than a separator. Each case is a real shape a
// migration can carry; a delimiter scan gets every one of them wrong in a way
// that only surfaces at the server as a syntax error on a line nobody wrote.
func TestSplitStatementsUnderstandsLiteralContexts(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "simple statements",
			script: "CREATE TABLE a (id int);\nCREATE TABLE b (id int);",
			want:   []string{"CREATE TABLE a (id int)", "CREATE TABLE b (id int)"},
		},
		{
			name:   "trailing statement without a semicolon",
			script: "SELECT 1;\nSELECT 2",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "semicolon inside a string literal",
			script: `INSERT INTO a VALUES ('x;y');`,
			want:   []string{`INSERT INTO a VALUES ('x;y')`},
		},
		{
			name:   "doubled quote inside a string literal",
			script: `INSERT INTO a VALUES ('it''s; here');`,
			want:   []string{`INSERT INTO a VALUES ('it''s; here')`},
		},
		{
			name: "semicolon inside a line comment",
			// The comment is dropped (the newline stays), so the second
			// statement is unaffected by the semicolon inside it.
			script: "SELECT 1; -- trailing; note\nSELECT 2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "semicolon inside a block comment",
			// The comment is preserved verbatim and belongs to the statement
			// that follows it; only its semicolon is inert.
			script: "SELECT 1; /* a; b */ SELECT 2;",
			want:   []string{"SELECT 1", "/* a; b */ SELECT 2"},
		},
		{
			name: "nested block comment",
			// PostgreSQL nests block comments, so the inner /* ... */ does not
			// end the outer one.
			script: "SELECT 1 /* outer /* inner; */ still; outer */;",
			want:   []string{"SELECT 1 /* outer /* inner; */ still; outer */"},
		},
		{
			name:   "escape string literal with a backslash-escaped quote",
			script: `SELECT E'a\'; b';`,
			want:   []string{`SELECT E'a\'; b'`},
		},
		{
			name:   "dollar-quoted body",
			script: "CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; PERFORM 2; END; $$ LANGUAGE plpgsql;",
			want:   []string{"CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; PERFORM 2; END; $$ LANGUAGE plpgsql"},
		},
		{
			name:   "tagged dollar quote",
			script: "DO $body$ BEGIN RAISE NOTICE 'hi;'; END; $body$;\nSELECT 1;",
			want:   []string{"DO $body$ BEGIN RAISE NOTICE 'hi;'; END; $body$", "SELECT 1"},
		},
		{
			name:   "positional parameter is not a dollar quote",
			script: "SELECT $1; SELECT $2;",
			want:   []string{"SELECT $1", "SELECT $2"},
		},
		{
			name:   "empty and whitespace-only script",
			script: "  \n\t\n",
			want:   nil,
		},
		{
			name:   "empty statements are dropped",
			script: ";;\nSELECT 1;;",
			want:   []string{"SELECT 1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.script)
			if len(got) != len(tc.want) {
				t.Fatalf("want %d statements, got %d: %#v", len(tc.want), len(got), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("statement %d:\n want %q\n  got %q", i, tc.want[i], got[i])
				}
			}
		})
	}
}

// TestSplitStatementsKeepsUnterminatedBodyWhole pins the fail-safe direction for
// a malformed script: an unterminated dollar quote keeps the remainder as ONE
// statement body. Splitting it would hand the server fragments it never saw.
func TestSplitStatementsKeepsUnterminatedBodyWhole(t *testing.T) {
	got := splitStatements("SELECT 1;\nDO $$ BEGIN PERFORM 1; PERFORM 2;")
	if len(got) != 2 {
		t.Fatalf("want 2 statements, got %d: %#v", len(got), got)
	}
	if !strings.HasPrefix(got[1], "DO $$") {
		t.Fatalf("second statement = %q, want the unterminated body kept whole", got[1])
	}
	if !strings.Contains(got[1], "PERFORM 2;") {
		t.Fatalf("second statement lost part of the body: %q", got[1])
	}
}

// TestShippedMigrationsSplitCleanly guards the real corpus: every migration file
// the queue ships must split into statements with no leftover fragment.
func TestShippedMigrationsSplitCleanly(t *testing.T) {
	names, err := migrations.Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no migrations found; the guard would pass vacuously")
	}
	for _, name := range names {
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		script := string(raw)
		for i, stmt := range splitStatements(script) {
			if strings.TrimSpace(stmt) == "" {
				t.Fatalf("%s: statement %d is empty after splitting", name, i)
			}
			// A statement that lost its tail to a mis-detected delimiter shows
			// up as an unbalanced quote or paren.
			if strings.Count(stmt, "'")%2 != 0 {
				t.Fatalf("%s: statement %d has an odd number of quotes, the splitter cut inside a literal:\n%s", name, i, stmt)
			}
			if strings.Count(stmt, "(") != strings.Count(stmt, ")") {
				t.Fatalf("%s: statement %d has unbalanced parentheses, the splitter cut inside a literal or comment:\n%s", name, i, stmt)
			}
		}
	}
}
