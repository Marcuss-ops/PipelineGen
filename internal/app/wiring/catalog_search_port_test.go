package wiring

import (
	"context"
	"testing"
)

// TestPostgresCatalogPort_FailsClosedWithoutSearcher pins the engine-named
// contract of the SourceCatalog port: an unwired media search surface must
// report an ERROR, not a successful empty catalog.
//
// This is the reusable lesson from the P2-9 read migrations: an unwired engine
// that answers "no matches" is indistinguishable from a real empty result, so a
// closed media plane would silently look like a catalog with nothing in it.
// Returning the error is what lets the caller refuse to resolve the source.
func TestPostgresCatalogPort_FailsClosedWithoutSearcher(t *testing.T) {
	var port *postgresCatalogPort // the nil-receiver form must not panic
	results, err := port.SearchAll(context.Background(), "ocean")
	if err == nil {
		t.Fatalf("expected an error without a media searcher, got results=%v", results)
	}
	if results != nil {
		t.Fatalf("expected no results on failure, got %v", results)
	}

	results, err = (&postgresCatalogPort{}).SearchAll(context.Background(), "ocean")
	if err == nil {
		t.Fatalf("expected an error without a media searcher, got results=%v", results)
	}
	if results != nil {
		t.Fatalf("expected no results on failure, got %v", results)
	}
}
