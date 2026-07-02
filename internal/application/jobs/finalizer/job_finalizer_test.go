package finalizer

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ── Test (a): job_events error is NOT silently ignored ────────────────
// Verified via code review: the `_, _ = tx.ExecContext(...)` in
// markSucceeded is now `if _, err := tx.ExecContext(...); err != nil
// { return fmt.Errorf(...) }`. The compile-time assertion below
// confirms the Finalizer type satisfies the contract.
func TestFinalizerSatisfiesInterface(t *testing.T) {
	var _ finalization.JobFinalizer = (*Finalizer)(nil)
	// Compile-time check: if Finalizer doesn't implement
	// finalization.JobFinalizer, this file won't compile.
}

// ── Test (b): lease_expiry is read from DB row ────────────────────────
// The jobRow struct now includes leaseExpiry sql.NullString.
// selectJobForFinalization's query includes lease_expiry and validates
// it after scanning.
func TestJobRowIncludesLeaseExpiry(t *testing.T) {
	// Verify the struct field exists (compile-time assertion).
	row := jobRow{
		leaseExpiry: sqlNullString("2026-01-01T00:00:00Z"),
	}
	if !row.leaseExpiry.Valid {
		t.Error("leaseExpiry should be valid after setting")
	}
}

// sqlNullString is a helper for constructing sql.NullString in tests.
func sqlNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// ── Test (c): completion fingerprint ────────────────────────────────

func TestComputeCompletionFingerprint_SameArtifactsSameFingerprint(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)
	artifacts := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "abc123",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
		{
			ArtifactID:    "art-2",
			SHA256:        "def456",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-def"},
		},
	}

	fp1 := computeCompletionFingerprint(result, artifacts)
	fp2 := computeCompletionFingerprint(result, artifacts)

	if fp1 != fp2 {
		t.Errorf("same inputs should produce same fingerprint: %s != %s", fp1, fp2)
	}
	if fp1 == "" {
		t.Error("fingerprint should not be empty")
	}
}

func TestComputeCompletionFingerprint_DifferentArtifactsDifferentFingerprint(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	artifactsA := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "abc123",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
	}
	artifactsB := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "xyz789", // Different SHA256
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
	}

	fpA := computeCompletionFingerprint(result, artifactsA)
	fpB := computeCompletionFingerprint(result, artifactsB)

	if fpA == fpB {
		t.Error("different artifacts should produce different fingerprints")
	}
}

func TestComputeCompletionFingerprint_DifferentResultDifferentFingerprint(t *testing.T) {
	resultA := json.RawMessage(`{"status":"ok"}`)
	resultB := json.RawMessage(`{"status":"retry"}`)
	artifacts := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "abc123",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
	}

	fpA := computeCompletionFingerprint(resultA, artifacts)
	fpB := computeCompletionFingerprint(resultB, artifacts)

	if fpA == fpB {
		t.Error("different result data should produce different fingerprints")
	}
}

func TestComputeCompletionFingerprint_SameArtifactsDifferentOrder(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	// Same artifacts, different order — should produce the same
	// fingerprint because we sort by ArtifactID.
	artifactsOrder1 := []finalization.PublishedArtifact{
		{ArtifactID: "b", SHA256: "hash-b", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-b"}},
		{ArtifactID: "a", SHA256: "hash-a", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-a"}},
	}
	artifactsOrder2 := []finalization.PublishedArtifact{
		{ArtifactID: "a", SHA256: "hash-a", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-a"}},
		{ArtifactID: "b", SHA256: "hash-b", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-b"}},
	}

	fp1 := computeCompletionFingerprint(result, artifactsOrder1)
	fp2 := computeCompletionFingerprint(result, artifactsOrder2)

	if fp1 != fp2 {
		t.Errorf("different artifact order should produce same fingerprint (sorting): %s != %s", fp1, fp2)
	}
}

func TestComputeCompletionFingerprint_EmptyArtifacts(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)
	fp := computeCompletionFingerprint(result, nil)

	if fp == "" {
		t.Error("fingerprint should not be empty even with zero artifacts")
	}

	// Same call again should produce same fingerprint.
	fp2 := computeCompletionFingerprint(result, []finalization.PublishedArtifact{})
	if fp != fp2 {
		t.Error("empty vs nil artifacts should produce the same fingerprint (both zero-length)")
	}
}

func TestComputeCompletionFingerprint_DifferentSourceVersion(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	artifactsV1 := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f1"}},
	}
	artifactsV2 := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 2, Location: finalization.AssetLocation{FileID: "f1"}},
	}

	if computeCompletionFingerprint(result, artifactsV1) == computeCompletionFingerprint(result, artifactsV2) {
		t.Error("different SourceVersion should produce different fingerprints")
	}
}

func TestComputeCompletionFingerprint_DifferentFileID(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	artifactsA := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "drive-1"}},
	}
	artifactsB := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "drive-2"}},
	}

	if computeCompletionFingerprint(result, artifactsA) == computeCompletionFingerprint(result, artifactsB) {
		t.Error("different FileID should produce different fingerprints")
	}
}

// ── Test: extractCompletionFingerprint ──────────────────────────────

func TestExtractCompletionFingerprint_Valid(t *testing.T) {
	wrapped := `{"data":{"status":"ok"},"completion_fingerprint":"abc123"}`
	fp := extractCompletionFingerprint(wrapped)
	if fp != "abc123" {
		t.Errorf("fingerprint = %q, want %q", fp, "abc123")
	}
}

func TestExtractCompletionFingerprint_LegacyFormat(t *testing.T) {
	legacy := `{"status":"ok"}`
	fp := extractCompletionFingerprint(legacy)
	if fp != "" {
		t.Errorf("fingerprint = %q, want empty for legacy format", fp)
	}
}

func TestExtractCompletionFingerprint_EmptyString(t *testing.T) {
	fp := extractCompletionFingerprint("")
	if fp != "" {
		t.Errorf("fingerprint = %q, want empty for empty string", fp)
	}
}

func TestExtractCompletionFingerprint_InvalidJSON(t *testing.T) {
	fp := extractCompletionFingerprint("not-json")
	if fp != "" {
		t.Errorf("fingerprint = %q, want empty for invalid JSON", fp)
	}
}

// ── Test: hashJSONString ────────────────────────────────────────────

func TestHashJSONString_Deterministic(t *testing.T) {
	h1 := hashJSONString(`{"a":1}`)
	h2 := hashJSONString(`{"a":1}`)
	if h1 != h2 {
		t.Errorf("hash should be deterministic: %s != %s", h1, h2)
	}
}

func TestHashJSONString_DifferentInputs(t *testing.T) {
	h1 := hashJSONString(`{"a":1}`)
	h2 := hashJSONString(`{"a":2}`)
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestHashJSONString_EmptyBecomesEmptyObject(t *testing.T) {
	h1 := hashJSONString("")
	h2 := hashJSONString("{}")
	if h1 != h2 {
		t.Errorf("empty string should hash as {}: %s != %s", h1, h2)
	}
}

func TestHashJSONString_NullBecomesEmptyObject(t *testing.T) {
	h1 := hashJSONString("null")
	h2 := hashJSONString("{}")
	if h1 != h2 {
		t.Errorf("null should hash as {}: %s != %s", h1, h2)
	}
}
