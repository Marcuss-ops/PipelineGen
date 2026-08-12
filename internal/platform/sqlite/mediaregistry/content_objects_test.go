package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	_ "github.com/mattn/go-sqlite3"
)

// contentObjectsSchema mirrors migrations/sqlite/194_content_objects.sql so
// the adapter tests run against the canonical schema shape in-memory.
const contentObjectsSchema = `
CREATE TABLE IF NOT EXISTS content_objects (
    sha256           TEXT PRIMARY KEY,
    size_bytes       INTEGER NOT NULL,
    mime_type        TEXT,
    storage_uri      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    verified_at      TEXT,
    integrity_status TEXT NOT NULL
);`

func newContentObjectStore(t *testing.T) *ContentObjectStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(contentObjectsSchema); err != nil {
		t.Fatal(err)
	}
	store, err := NewContentObjectStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sampleContentObject() capregistry.ContentObject {
	return capregistry.ContentObject{
		SHA256:          "a1f8c72e00000000000000000000000000000000000000000000000000000001",
		SizeBytes:       47281992,
		MimeType:        "video/mp4",
		StorageURI:      "cas://a1/f8/a1f8c72e00000000000000000000000000000000000000000000000000000001",
		CreatedAt:       "2026-08-12T08:00:00Z",
		IntegrityStatus: capregistry.IntegrityUnverified,
	}
}

func TestContentObjectStore_PutGetRoundTrip(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()
	want := sampleContentObject()

	if err := store.Put(ctx, want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := store.Get(ctx, want.SHA256)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("get returned nil for existing object")
	}
	if got.SHA256 != want.SHA256 || got.SizeBytes != want.SizeBytes ||
		got.MimeType != want.MimeType || got.StorageURI != want.StorageURI ||
		got.CreatedAt != want.CreatedAt || got.IntegrityStatus != capregistry.IntegrityUnverified {
		t.Fatalf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	if got.VerifiedAt != "" {
		t.Fatalf("verified_at must be empty before Verify, got %q", got.VerifiedAt)
	}
}

func TestContentObjectStore_GetMissingReturnsNilNil(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()

	got, err := store.Get(ctx, "ffffffff0000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if got != nil {
		t.Fatalf("get missing returned non-nil object: %+v", got)
	}
}

func TestContentObjectStore_PutIdempotentOnDigest(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()

	first := sampleContentObject()
	if err := store.Put(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Same digest, different metadata: merge, no duplicate row.
	second := first
	second.SizeBytes = 12345
	second.MimeType = "audio/mpeg"
	if err := store.Put(ctx, second); err != nil {
		t.Fatal(err)
	}

	n, err := store.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count after re-put same digest = %d, want 1", n)
	}
	got, err := store.Get(ctx, first.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if got.SizeBytes != 12345 || got.MimeType != "audio/mpeg" {
		t.Fatalf("merged fields not updated: %+v", got)
	}
}

func TestContentObjectStore_VerifyMarksVerified(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()
	obj := sampleContentObject()
	if err := store.Put(ctx, obj); err != nil {
		t.Fatal(err)
	}

	if err := store.Verify(ctx, obj.SHA256, "2026-08-12T09:00:00Z"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, err := store.Get(ctx, obj.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntegrityStatus != capregistry.IntegrityVerified {
		t.Fatalf("integrity_status = %q, want %q", got.IntegrityStatus, capregistry.IntegrityVerified)
	}
	if got.VerifiedAt != "2026-08-12T09:00:00Z" {
		t.Fatalf("verified_at = %q, want %q", got.VerifiedAt, "2026-08-12T09:00:00Z")
	}
}

func TestContentObjectStore_VerifyMissingReturnsNotFound(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()

	err := store.Verify(ctx, "ffffffff0000000000000000000000000000000000000000000000000000000001", "2026-08-12T09:00:00Z")
	if !errors.Is(err, capregistry.ErrContentObjectNotFound) {
		t.Fatalf("verify missing: err=%v, want ErrContentObjectNotFound", err)
	}
}

func TestContentObjectStore_Delete(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()
	obj := sampleContentObject()
	if err := store.Put(ctx, obj); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, obj.SHA256); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := store.Get(ctx, obj.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("object still present after delete: %+v", got)
	}
	// Delete of a missing object is an idempotent no-op.
	if err := store.Delete(ctx, obj.SHA256); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestContentObjectStore_Count(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()

	if n, err := store.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count on empty store = %d err=%v", n, err)
	}
	obj := sampleContentObject()
	for _, sha := range []string{
		"a1f8c72e0000000000000000000000000000000000000000000000000000000001",
		"a1f8c72e0000000000000000000000000000000000000000000000000000000002",
		"a1f8c72e0000000000000000000000000000000000000000000000000000000003",
	} {
		o := obj
		o.SHA256 = sha
		o.StorageURI = "cas://a1/f8/" + sha
		if err := store.Put(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := store.Count(ctx); err != nil || n != 3 {
		t.Fatalf("count = %d err=%v, want 3", n, err)
	}
}

func TestContentObjectStore_RejectsInvalid(t *testing.T) {
	store := newContentObjectStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		obj  capregistry.ContentObject
	}{
		{"empty sha256", func() capregistry.ContentObject {
			o := sampleContentObject()
			o.SHA256 = ""
			return o
		}()},
		{"empty storage_uri", func() capregistry.ContentObject {
			o := sampleContentObject()
			o.StorageURI = ""
			return o
		}()},
		{"negative size", func() capregistry.ContentObject {
			o := sampleContentObject()
			o.SizeBytes = -1
			return o
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Put(ctx, tc.obj); !errors.Is(err, capregistry.ErrContentObjectInvalid) {
				t.Fatalf("put: err=%v, want ErrContentObjectInvalid", err)
			}
		})
	}
}

func TestContentObjectStore_NotWired(t *testing.T) {
	var store *ContentObjectStore
	ctx := context.Background()

	if err := store.Put(ctx, sampleContentObject()); !errors.Is(err, ErrContentObjectsNotWired) {
		t.Fatalf("put on nil store: err=%v, want ErrContentObjectsNotWired", err)
	}
	if _, err := store.Get(ctx, "abc"); !errors.Is(err, ErrContentObjectsNotWired) {
		t.Fatalf("get on nil store: err=%v, want ErrContentObjectsNotWired", err)
	}
	if err := store.Delete(ctx, "abc"); !errors.Is(err, ErrContentObjectsNotWired) {
		t.Fatalf("delete on nil store: err=%v, want ErrContentObjectsNotWired", err)
	}
	if err := store.Verify(ctx, "abc", "2026-08-12T00:00:00Z"); !errors.Is(err, ErrContentObjectsNotWired) {
		t.Fatalf("verify on nil store: err=%v, want ErrContentObjectsNotWired", err)
	}
	if _, err := store.Count(ctx); !errors.Is(err, ErrContentObjectsNotWired) {
		t.Fatalf("count on nil store: err=%v, want ErrContentObjectsNotWired", err)
	}
}
