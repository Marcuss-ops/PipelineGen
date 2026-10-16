package migrate

import (
	"strings"
	"testing"
)

// FuzzSplitStatements is the property side of the migration splitter's
// coverage. The table test in split_statements_test.go pins the constructs the
// splitter is KNOWN to handle; this fuzzer attacks the ones nobody thought of.
// The cost of a missed boundary is a migration that fails at the server on a
// line nobody wrote, and a hand-written parser is exactly the kind of code
// where "the cases I enumerated" is not the same as "the cases that exist".
//
// The invariants are the contract, not the implementation:
//
//   - the splitter never panics, whatever bytes it is handed. Malformed SQL
//     (an unterminated quote, comment or dollar quote) is the server's problem,
//     never a crash in the migration runner;
//   - a statement, once split, splits into itself: re-joining the output and
//     splitting again is a fixed point. This is what turns "a boundary was
//     missed" into a test failure instead of a statement that gets Exec'd as
//     one blob on a re-run;
//   - no output statement is empty or untrimmed, so a caller never Execs
//     whitespace and never depends on the splitter's whitespace policy;
//   - the splitter cannot invent statements: the output count is bounded by the
//     number of semicolons plus one.
func FuzzSplitStatements(f *testing.F) {
	seeds := []string{
		"",
		"   \n\t  ",
		";;;",
		"SELECT 1;",
		"SELECT 1",
		"CREATE TABLE a (id int);\nCREATE TABLE b (id int);",
		`INSERT INTO a VALUES ('it''s; here');`,
		`INSERT INTO a VALUES (E'back\\slash; here');`,
		"SELECT 1; -- trailing; note\nSELECT 2;",
		"SELECT 1 /* a; b */; SELECT 2;",
		"SELECT 1 /* outer /* inner; */ still; outer */;",
		"$$ body; with; semicolons $$;",
		"$tag$ body; $1; $tag$;",
		"DO $$ BEGIN PERFORM 1; END $$;",
		"SELECT $1;",
		"SELECT '$1';",
		// Malformed shapes: these must be handled, not rejected.
		"/* unterminated ;",
		"'; unterminated",
		"E'\\';",
		"$$ unterminated ;",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, script string) {
		statements := splitStatements(script)

		if limit := strings.Count(script, ";") + 1; len(statements) > limit {
			t.Fatalf("split %q into %d statements; a script with %d semicolons cannot produce more than %d",
				script, len(statements), strings.Count(script, ";"), limit)
		}
		for _, statement := range statements {
			if statement == "" || strings.TrimSpace(statement) != statement {
				t.Fatalf("statement %q from %q is empty or untrimmed", statement, script)
			}
		}

		rejoined := strings.Join(statements, ";")
		again := splitStatements(rejoined)
		if len(again) != len(statements) {
			t.Fatalf("split is not a fixed point for %q: %d statement(s), then %d from %q",
				script, len(statements), len(again), rejoined)
		}
		for i := range again {
			if again[i] != statements[i] {
				t.Fatalf("split is not a fixed point for %q at index %d: %q, then %q",
					script, i, statements[i], again[i])
			}
		}
	})
}

// TestSplitStatementsToleratesMalformedInput states the malformed cases
// explicitly instead of leaving them to the fuzzer's discovery order: an
// unterminated literal or comment swallows the rest of the script into the
// statement that opened it, and the splitter still returns exactly one
// statement rather than panicking or dropping the text.
func TestSplitStatementsToleratesMalformedInput(t *testing.T) {
	for _, script := range []string{
		"/* unterminated ; SELECT 1",
		"'; SELECT 1",
		"E'\\'; SELECT 1",
		"$$ body; SELECT 1",
		"$tag$ body; SELECT 1",
	} {
		got := splitStatements(script)
		if len(got) != 1 {
			t.Errorf("split(%q) = %q, want the whole script as one statement", script, got)
		}
		if len(got) == 1 && got[0] != strings.TrimSpace(script) {
			t.Errorf("split(%q) = %q, want the script preserved verbatim", script, got[0])
		}
	}
}
