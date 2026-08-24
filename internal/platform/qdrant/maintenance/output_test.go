// internal/platform/qdrant/maintenance/output_test.go — godlike/06
// SSOT proof-of-correctness tests for the CLIOutput adapter.
//
// godlike/07 minimum-blast-radius: this file establishes the typed
// adapter's invariant surface so future per-mode handlers can rely
// on round-trip semantics without re-proving the adapter contract
// per call site.
package maintenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCLIOutput_JSON_MarshalsStruct asserts the round-trip contract:
// pass any JSON-marshalable value, capture on a bytes.Buffer, parse
// the buffer back, see the same fields. This pins the machine-consumer
// output surface for audit/delete modes (`--json` flag).
func TestCLIOutput_JSON_MarshalsStruct(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	o := NewCLIOutput(&buf)

	input := map[string]any{"applied": 5, "collection": "v3-stock-active"}
	if err := o.JSON(input); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["collection"] != "v3-stock-active" {
		t.Errorf("collection round-trip mismatch: got %v", got)
	}
	if applied, ok := got["applied"].(float64); !ok || applied != 5 {
		t.Errorf("applied round-trip mismatch: got %v (type %T)", got["applied"], got["applied"])
	}
}

// TestCLIOutput_JSON_ReturnsErrorOnCircularStruct asserts the
// godlike/07 NO-FAKE-AVAILABILITY contract: marshal errors from the
// typed adapter must SURFACE through a typed error, not be silently
// swallowed (per CR-thinker Q4).
func TestCLIOutput_JSON_ReturnsErrorOnCircularStruct(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	o := NewCLIOutput(&buf)

	// Circular self-reference: json.Marshal cannot resolve.
	type cycle struct {
		Self *cycle
	}
	c := &cycle{}
	c.Self = c

	err := o.JSON(c)
	if err == nil {
		t.Fatalf("expected error on circular struct; got nil")
	}
	if !strings.Contains(err.Error(), "marshal failed") {
		t.Errorf("err must contain 'marshal failed'; got %q", err.Error())
	}
	// Buffer should be untouched (no partial write).
	if buf.Len() != 0 {
		t.Errorf("buffer should be empty after marshal error; got %d bytes: %q", buf.Len(), buf.String())
	}
}

// TestCLIOutput_HumanLine_WritesLineWithNewline pins the canonical
// "\n-terminated line" surface for block headers ("=== qdrant-maintenance
// audit ===") and pre-formatted strings (legacyaudit.StringifyReport output).
func TestCLIOutput_HumanLine_WritesLineWithNewline(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	o := NewCLIOutput(&buf)

	o.HumanLine("=== qdrant-maintenance audit ===")

	want := "=== qdrant-maintenance audit ===\n"
	if buf.String() != want {
		t.Errorf("expected %q; got %q", want, buf.String())
	}
}

// TestCLIOutput_HumanLinef_WritesFormattedLine pins the printf-style
// formatted-line surface for the per-mode "Collection: %s" / "With
// drive_link: %d" report fields. The trailing newline is owned by
// the caller (included in the format string), matching the
// pre-existing fmt.Fprintf behavior byte-equivalently.
func TestCLIOutput_HumanLinef_WritesFormattedLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	o := NewCLIOutput(&buf)

	o.HumanLinef("  Collection:       %s\n", "stock-active")

	want := "  Collection:       stock-active\n"
	if buf.String() != want {
		t.Errorf("expected %q; got %q", want, buf.String())
	}
}

// TestCLIOutput_HumanLinef_WritesMultiArgFormat pins the variadic-args
// contract for the [i] %s error-trace line in repair-locators mode.
func TestCLIOutput_HumanLinef_WritesMultiArgFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	o := NewCLIOutput(&buf)

	o.HumanLinef("    [%d] %s\n", 7, "fetch timeout")

	want := "    [7] fetch timeout\n"
	if buf.String() != want {
		t.Errorf("expected %q; got %q", want, buf.String())
	}
}

// TestCLIOutput_NewCLIOutput_NilWriterDefaultsToStdout asserts the
// godlike/07 fail-closed-at-construction default: nil writer is
// replaced with os.Stdout so CLI UX never silently no-ops. The actual
// os.Stdout pointer is not dereferenced here (we never call a method
// because that would leak "=== qdrant-maintenance audit ===\n" into
// test output).
func TestCLIOutput_NewCLIOutput_NilWriterDefaultsToStdout(t *testing.T) {
	t.Parallel()
	o := NewCLIOutput(nil)
	if o == nil {
		t.Fatal("expected non-nil CLIOutput for nil writer")
	}
	if o.w == nil {
		t.Fatal("expected non-nil writer (default os.Stdout must be applied)")
	}
}
