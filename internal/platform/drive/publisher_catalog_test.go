// publisher_catalog_test.go — TDD coverage for the catalog-first
// folder resolution path (DoD item 6, SEMANTIC-LOCATION-API-2026-07-06).
//
// Tests the Publisher.lookupCatalogFolder helper against:
//   - nil catalog (returns "")
//   - cache hit (returns folder ID)
//   - cache miss (returns "", falls through to EnsureFolder)
//   - infrastructure error (returns "", logs Warn)
//
// godlike/07 NO-FAKE-AVAILABILITY: every error path explicitly
// documents that the Publisher falls back to EnsureFolder rather
// than failing the Publish call. The catalog is a best-effort
// optimisation — never a hard requirement.
package drive

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// testCatalogLookup implements CatalogFolderLookup for unit tests.
type testCatalogLookup struct {
	folderID string
	err      error
}

func (t *testCatalogLookup) LookupFolder(_ context.Context, _, _ string) (string, error) {
	return t.folderID, t.err
}

// TestLookupCatalogFolder_NilCatalog returns "" when no catalog is wired.
func TestLookupCatalogFolder_NilCatalog(t *testing.T) {
	pub := &Publisher{log: zap.NewNop()}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "Boxe/pexels/Mike-Tyson")
	if got != "" {
		t.Errorf("expected empty string (nil catalog), got %q", got)
	}
}

// TestLookupCatalogFolder_CacheHit returns the cached folder ID.
func TestLookupCatalogFolder_CacheHit(t *testing.T) {
	catalog := &testCatalogLookup{folderID: "cached-folder-abc"}
	pub := &Publisher{log: zap.NewNop(), catalogLookup: catalog}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "Boxe/pexels/Mike-Tyson")
	if got != "cached-folder-abc" {
		t.Errorf("expected 'cached-folder-abc', got %q", got)
	}
}

// TestLookupCatalogFolder_CacheMiss returns "" when the adapter
// returns ("", nil) — no active entry in catalog.
func TestLookupCatalogFolder_CacheMiss(t *testing.T) {
	catalog := &testCatalogLookup{folderID: ""}
	pub := &Publisher{log: zap.NewNop(), catalogLookup: catalog}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "Boxe/pexels/Mike-Tyson")
	if got != "" {
		t.Errorf("expected empty string (cache miss), got %q", got)
	}
}

// TestLookupCatalogFolder_InfrastructureError returns "" when the
// catalog adapter returns an infrastructure error (e.g. DB down).
// The Publisher falls back to EnsureFolder rather than failing the
// Publish call. Per godlike/07 NO-FAKE-AVAILABILITY: catalog is a
// best-effort optimisation — never a hard requirement.
func TestLookupCatalogFolder_InfrastructureError(t *testing.T) {
	catalog := &testCatalogLookup{err: errors.New("simulated DB error")}
	pub := &Publisher{log: zap.NewNop(), catalogLookup: catalog}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "Boxe/pexels/Mike-Tyson")

	if got != "" {
		t.Errorf("expected empty string (infrastructure error), got %q", got)
	}
}

// TestLookupCatalogFolder_EmptyInput returns "" for empty path (edge case).
func TestLookupCatalogFolder_EmptyInput(t *testing.T) {
	catalog := &testCatalogLookup{folderID: ""}
	pub := &Publisher{log: zap.NewNop(), catalogLookup: catalog}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "")
	if got != "" {
		t.Errorf("expected empty string (empty input), got %q", got)
	}
}

// TestSetCatalogLookup_NilDisablesLookups verifies that passing nil
// to SetCatalogLookup disables the feature.
func TestSetCatalogLookup_NilDisablesLookups(t *testing.T) {
	pub := &Publisher{log: zap.NewNop()}
	pub.SetCatalogLookup(nil)
	if pub.catalogLookup != nil {
		t.Error("SetCatalogLookup(nil): catalogLookup must be nil")
	}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "test")
	if got != "" {
		t.Errorf("expected empty string (nil catalog after SetCatalogLookup(nil)), got %q", got)
	}
}

// TestSetCatalogLookup_WiredEnablesLookups verifies that passing
// a non-nil CatalogFolderLookup enables the feature.
func TestSetCatalogLookup_WiredEnablesLookups(t *testing.T) {
	catalog := &testCatalogLookup{folderID: "wired-folder"}
	pub := &Publisher{log: zap.NewNop()}
	pub.SetCatalogLookup(catalog)
	if pub.catalogLookup == nil {
		t.Fatal("SetCatalogLookup: catalogLookup must be non-nil")
	}
	got := pub.lookupCatalogFolder(context.Background(), delivery.DestinationStock, "test")
	if got != "wired-folder" {
		t.Errorf("expected 'wired-folder', got %q", got)
	}
}
