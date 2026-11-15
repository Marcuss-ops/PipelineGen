package entitycatalog

import (
	"testing"
	"time"
)

func TestParseCatalogTimeDistinguishesLegacyFromEmpty(t *testing.T) {
	if _, legacy := parseCatalogTimeWithLegacy(""); legacy {
		t.Fatal("an absent timestamp is not a legacy value")
	}
	if _, legacy := parseCatalogTimeWithLegacy("2026-11-14T10:00:00Z"); legacy {
		t.Fatal("a valid timestamp must not be marked legacy")
	}
	parsed, legacy := parseCatalogTimeWithLegacy("not-a-timestamp")
	if !legacy {
		t.Fatal("an invalid timestamp must be marked legacy")
	}
	if !parsed.IsZero() {
		t.Fatalf("invalid timestamp parsed as %s, want zero", parsed)
	}
	var target time.Time
	if !parseCatalogTimeInto(&target, "still-not-a-timestamp") {
		t.Fatal("parseCatalogTimeInto did not propagate the legacy flag")
	}
}
