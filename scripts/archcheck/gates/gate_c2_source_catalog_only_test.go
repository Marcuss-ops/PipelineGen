//go:build c2_source_catalog_only

package main

import "testing"

func TestScanSourceCountsCanonicalSwitchAndIfRows(t *testing.T) {
	rows, err := scanSource("fixture.go", []byte(`package fixture
func resolve(source string) string {
 switch source { case "youtube": return "x" }
 if source == SourceArtlist { return "y" }
 return ""
}
`))
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2: %#v", len(rows), rows)
	}
}

func TestScanSourceIgnoresNonCanonicalValues(t *testing.T) {
	rows, err := scanSource("fixture.go", []byte(`package fixture
func resolve(source string) string {
 if source == "not-a-catalog-kind" { return source }
 return ""
}
`))
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows=%d, want 0: %#v", len(rows), rows)
	}
}
