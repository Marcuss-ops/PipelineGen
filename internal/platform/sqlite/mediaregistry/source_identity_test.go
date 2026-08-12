package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	_ "github.com/mattn/go-sqlite3"
)

func newSourceIdentityDB(t *testing.T) (*sql.DB, *SourceIdentityStore) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE source_identity_registry (
			source_type         TEXT NOT NULL,
			source_key          TEXT NOT NULL,
			content_sha256      TEXT NOT NULL,
			source_version      TEXT NOT NULL DEFAULT '',
			discovered_at       TEXT NOT NULL,
			last_seen_at        TEXT NOT NULL,
			verification_status TEXT NOT NULL DEFAULT 'UNVERIFIED',
			PRIMARY KEY (source_type, source_key)
		);`); err != nil {
		t.Fatal(err)
	}
	store, err := NewSourceIdentityStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, store
}

func TestSourceIdentityRecordAndLookup(t *testing.T) {
	_, store := newSourceIdentityDB(t)
	ctx := context.Background()

	sha := "a1f8c72e"
	id := capregistry.SourceIdentity{
		SourceType:         capregistry.SourceIdentityURL,
		SourceKey:          "https://example.com/video.mp4",
		ContentSHA256:      sha,
		SourceVersion:      "etag-v1",
		DiscoveredAt:       "2026-08-12T00:00:00Z",
		LastSeenAt:         "2026-08-12T00:00:00Z",
		VerificationStatus: capregistry.SourceIdentityUnverified,
	}
	if err := store.Record(ctx, id); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := store.Lookup(ctx, capregistry.SourceIdentityURL, "https://example.com/video.mp4")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Fatal("lookup after record returned nil")
	}
	if got.ContentSHA256 != sha || got.SourceVersion != "etag-v1" {
		t.Fatalf("lookup mismatch: %+v", got)
	}
	if got.VerificationStatus != capregistry.SourceIdentityUnverified {
		t.Fatalf("verification status default: %+v", got)
	}
}

func TestSourceIdentityRecordIdempotentUpsert(t *testing.T) {
	db, store := newSourceIdentityDB(t)
	ctx := context.Background()

	id := capregistry.SourceIdentity{
		SourceType:    capregistry.SourceIdentityDrive,
		SourceKey:     "1Gp1ue8",
		ContentSHA256: "a1f8c72e",
		DiscoveredAt:  "2026-08-12T00:00:00Z",
		LastSeenAt:    "2026-08-12T00:00:00Z",
	}
	if err := store.Record(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Re-record with a refreshed digest + version: must UPDATE, not duplicate.
	id.ContentSHA256 = "b2f8d999"
	id.SourceVersion = "etag-v2"
	id.LastSeenAt = "2026-08-13T00:00:00Z"
	id.VerificationStatus = capregistry.SourceIdentityVerified
	if err := store.Record(ctx, id); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_identity_registry`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("re-record must not duplicate: got %d rows, want 1", count)
	}

	got, err := store.Lookup(ctx, capregistry.SourceIdentityDrive, "1Gp1ue8")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ContentSHA256 != "b2f8d999" || got.SourceVersion != "etag-v2" {
		t.Fatalf("upsert must refresh mapping: %+v", got)
	}
	if got.VerificationStatus != capregistry.SourceIdentityVerified {
		t.Fatalf("upsert must refresh verification status: %+v", got)
	}
}

func TestSourceIdentityLookupUnknownReturnsNil(t *testing.T) {
	_, store := newSourceIdentityDB(t)
	ctx := context.Background()
	got, err := store.Lookup(ctx, capregistry.SourceIdentityURL, "https://unknown.example/v.mp4")
	if err != nil {
		t.Fatalf("lookup unknown: %v", err)
	}
	if got != nil {
		t.Fatalf("unknown identity must return nil, got %+v", got)
	}
}

func TestSourceIdentityCount(t *testing.T) {
	_, store := newSourceIdentityDB(t)
	ctx := context.Background()
	if n, err := store.Count(ctx); err != nil || n != 0 {
		t.Fatalf("empty count: n=%d err=%v", n, err)
	}
	base := capregistry.SourceIdentity{
		ContentSHA256: "a1f8c72e",
		DiscoveredAt:  "2026-08-12T00:00:00Z",
		LastSeenAt:    "2026-08-12T00:00:00Z",
	}
	for i, srcType := range []string{
		capregistry.SourceIdentityDrive,
		capregistry.SourceIdentityArtlist,
		capregistry.SourceIdentityURL,
	} {
		base.SourceType = srcType
		base.SourceKey = "key-" + srcType
		if err := store.Record(ctx, base); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if n, err := store.Count(ctx); err != nil || n != 3 {
		t.Fatalf("count after 3 records: n=%d err=%v", n, err)
	}
}

func TestSourceIdentityValidation(t *testing.T) {
	_, store := newSourceIdentityDB(t)
	ctx := context.Background()

	cases := []capregistry.SourceIdentity{
		{}, // all empty
		{SourceType: "url", SourceKey: "k", ContentSHA256: "a", DiscoveredAt: "d", LastSeenAt: ""},
		{SourceType: "", SourceKey: "k", ContentSHA256: "a", DiscoveredAt: "d", LastSeenAt: "l"},
	}
	for i, c := range cases {
		if err := store.Record(ctx, c); !errors.Is(err, capregistry.ErrSourceIdentityInvalid) {
			t.Fatalf("case %d: want ErrSourceIdentityInvalid, got %v", i, err)
		}
	}
	if _, err := store.Lookup(ctx, "", "k"); !errors.Is(err, capregistry.ErrSourceIdentityInvalid) {
		t.Fatalf("lookup empty source_type: want ErrSourceIdentityInvalid, got %v", err)
	}
	if _, err := store.Lookup(ctx, "url", ""); !errors.Is(err, capregistry.ErrSourceIdentityInvalid) {
		t.Fatalf("lookup empty source_key: want ErrSourceIdentityInvalid, got %v", err)
	}
}

func TestSourceIdentityNilDBFailsClosed(t *testing.T) {
	var store *SourceIdentityStore
	ctx := context.Background()
	if _, err := store.Lookup(ctx, "url", "k"); !errors.Is(err, ErrSourceIdentityNotWired) {
		t.Fatalf("nil store lookup: want ErrSourceIdentityNotWired, got %v", err)
	}
	if err := store.Record(ctx, capregistry.SourceIdentity{}); !errors.Is(err, ErrSourceIdentityNotWired) {
		t.Fatalf("nil store record: want ErrSourceIdentityNotWired, got %v", err)
	}
}

func TestSourceIdentityNewNilDB(t *testing.T) {
	if _, err := NewSourceIdentityStore(nil); err == nil {
		t.Fatal("NewSourceIdentityStore(nil) must fail")
	}
}
