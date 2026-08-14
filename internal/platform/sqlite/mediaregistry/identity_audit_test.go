package mediaregistry

import (
	"context"
	"testing"
)

func TestAuditIdentity_CleanIsZero(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	seedSource(t, db, "asset-a", "youtube", "dQw4w9", "")
	seedSource(t, db, "asset-b", "drive", "file1", "")

	report, err := r.AuditIdentity(context.Background())
	if err != nil {
		t.Fatalf("AuditIdentity: %v", err)
	}
	if report.DuplicateSourceIdentity != 0 {
		t.Fatalf("DuplicateSourceIdentity = %d, want 0", report.DuplicateSourceIdentity)
	}
	if report.DuplicateQdrantPoints != 0 {
		t.Fatalf("DuplicateQdrantPoints must stay 0 in the SQLite half, got %d", report.DuplicateQdrantPoints)
	}
}

func TestAuditIdentity_DetectsDuplicateSource(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	// Two assets claim the same (source_type, source_ref).
	seedSource(t, db, "asset-a", "youtube", "dQw4w9", "")
	seedSource(t, db, "asset-b", "youtube", "dQw4w9", "")

	report, err := r.AuditIdentity(context.Background())
	if err != nil {
		t.Fatalf("AuditIdentity: %v", err)
	}
	if report.DuplicateSourceIdentity != 1 {
		t.Fatalf("DuplicateSourceIdentity = %d, want 1", report.DuplicateSourceIdentity)
	}
}

func TestAuditIdentity_CountsDistinctDuplicateTuples(t *testing.T) {
	r, db := newCanonicalIdentityResolver(t)
	seedSource(t, db, "asset-a", "youtube", "dQw4w9", "")
	seedSource(t, db, "asset-b", "youtube", "dQw4w9", "")
	seedSource(t, db, "asset-c", "drive", "file1", "")
	seedSource(t, db, "asset-d", "drive", "file1", "")
	seedSource(t, db, "asset-e", "artlist", "123", "")

	report, err := r.AuditIdentity(context.Background())
	if err != nil {
		t.Fatalf("AuditIdentity: %v", err)
	}
	if report.DuplicateSourceIdentity != 2 {
		t.Fatalf("DuplicateSourceIdentity = %d, want 2 (youtube + drive)", report.DuplicateSourceIdentity)
	}
}
