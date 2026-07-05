package stockpipeline

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ── VerifyChunks ───────────────────────────────────────────────────────

// fakeSHA is a 64-char hex placeholder used by tests (real SHA-256
// output is 64-char lowercase hex). Centralised here so tests are
// diff-friendly: every chunk carries a deterministic 64-char hash
// derived from the chunk index.
func fakeSHA(i int) string {
	const pad = "0123456789abcdef"
	out := make([]byte, 64)
	for k := 0; k < 64; k++ {
		out[k] = pad[(k+i)%len(pad)]
	}
	return string(out)
}

// ── VerifyChunks / VerifyMetadata — strict SHA256 validation ─────
//
// Commit 0.2 P0 2.4 hardening: the gates reject malformed SHA256
// (len<64 / non-hex / uppercase) BEFORE the panic site at
// BuildFinalizationRequest's `"stock:" + sha[:16]` composition.
// Each sub-test asserts the gate surfaces the typed sentinel
// (ErrStockChunkHashInvalid / ErrStockMetadataHashInvalid) AND that
// the underlying asset.ErrSHA256Invalid is reachable via errors.Is
// — the contract probe for future wrapping layers.

func TestVerifyChunks_RejectsMalformedSHA256(t *testing.T) {
	base := ChunkState{
		Index: 0, ArtifactID: "stock:run:chunk:0",
		LocalPath: "/tmp/c.mp4", RemoteFileID: "drive-id-0", SizeBytes: 1024,
		Filename: "c.mp4",
	}
	cases := []struct {
		name string
		sha  string
	}{
		{"short (len=15)", strings.Repeat("a", 15)},
		{"non-hex (g)", strings.Repeat("a", 63) + "g"},
		{"uppercase", strings.ToUpper(fakeSHA(0))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.SHA256 = tc.sha
			err := VerifyChunks([]ChunkState{c})
			if err == nil {
				t.Fatalf("verifyChunks must reject malformed SHA256 (%s)", tc.name)
			}
			if !errors.Is(err, ErrStockChunkHashInvalid) {
				t.Errorf("err = %v; want errors.Is ErrStockChunkHashInvalid == true", err)
			}
			if !errors.Is(err, asset.ErrSHA256Invalid) {
				t.Errorf("err = %v; want errors.Is asset.ErrSHA256Invalid == true (godlike/07 deep probe)", err)
			}
		})
	}
}

func TestVerifyMetadata_RejectsMalformedSHA256(t *testing.T) {
	base := MetadataState{
		LocalPath: "/tmp/m.json", RemoteFileID: "drive-id-m", SizeBytes: 512,
	}
	cases := []struct {
		name string
		sha  string
	}{
		{"short (len=15)", strings.Repeat("a", 15)},
		{"non-hex (g)", strings.Repeat("a", 63) + "g"},
		{"uppercase", strings.ToUpper(fakeSHA(99))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.SHA256 = tc.sha
			err := VerifyMetadata(m)
			if err == nil {
				t.Fatalf("VerifyMetadata must reject malformed SHA256 (%s)", tc.name)
			}
			if !errors.Is(err, ErrStockMetadataHashInvalid) {
				t.Errorf("err = %v; want errors.Is ErrStockMetadataHashInvalid == true", err)
			}
			if !errors.Is(err, asset.ErrSHA256Invalid) {
				t.Errorf("err = %v; want errors.Is asset.ErrSHA256Invalid == true (godlike/07 deep probe)", err)
			}
		})
	}
}

// TestVerifyChunks_AcceptsCanonicalLowercaseHex64 pins the positive
// contract: existing happy-path SHA256 (fakeSHA produces valid 64-char
// lowercase hex) MUST still pass the gate. Guards against future
// regressions where a too-strict gate would reject canonical inputs.
func TestVerifyChunks_AcceptsCanonicalLowercaseHex64(t *testing.T) {
	chunk := ChunkState{
		Index: 0, ArtifactID: "stock:run:chunk:0",
		LocalPath:    "/tmp/c.mp4",
		RemoteFileID: "drive-id-0",
		SizeBytes:    1024,
		Filename:     "c.mp4",
		SHA256:       fakeSHA(0),
	}
	if err := VerifyChunks([]ChunkState{chunk}); err != nil {
		t.Fatalf("verifyChunks must accept canonical SHA256: %v", err)
	}
}

// TestVerifyChunks_Empty raises the canonical P0 2.1 sentinel
// when no chunks are finalized (the gate's primary failure mode
// — production today, before Commit 4-7 lands the chunk ladder).
func TestVerifyChunks_Empty(t *testing.T) {
	err := VerifyChunks(nil)
	if !errors.Is(err, ErrStockNoChunksFinalized) {
		t.Fatalf("nil chunks: want ErrStockNoChunksFinalized, got %v", err)
	}
	err = VerifyChunks([]ChunkState{})
	if !errors.Is(err, ErrStockNoChunksFinalized) {
		t.Fatalf("zero chunks: want ErrStockNoChunksFinalized, got %v", err)
	}
}

// TestVerifyChunks_HappyPath validates the gate is silent when
// every chunk has LocalPath + RemoteFileID + SHA256 — the
// post-Cutover-4-7 success state.
func TestVerifyChunks_HappyPath(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:run:chunk:0", LocalPath: "/tmp/c.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-id-0", SizeBytes: 1024, Filename: "c.mp4"},
		{Index: 1, ArtifactID: "stock:run:chunk:1", LocalPath: "/tmp/c1.mp4", SHA256: fakeSHA(1), RemoteFileID: "drive-id-1", SizeBytes: 2048, Filename: "c1.mp4"},
	}
	if err := VerifyChunks(chunks); err != nil {
		t.Fatalf("happy path: want nil, got %v", err)
	}
}

// TestVerifyChunks_LocalPathMissing asserts the gate surfaces
// ErrStockChunkNotFinalized for missing LocalPath (the failure
// mode when render produced no output).
func TestVerifyChunks_LocalPathMissing(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:run:chunk:0", LocalPath: "", SHA256: fakeSHA(0), RemoteFileID: "drive-id-0"},
	}
	err := VerifyChunks(chunks)
	if !errors.Is(err, ErrStockChunkNotFinalized) {
		t.Fatalf("missing localpath: want ErrStockChunkNotFinalized, got %v", err)
	}
}

// TestVerifyChunks_RemoteFileIDMissing asserts the gate surfaces
// ErrStockChunkNotFinalized for missing RemoteFileID (the failure
// mode when Publisher returned a corrupt DriveLink).
func TestVerifyChunks_RemoteFileIDMissing(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:run:chunk:0", LocalPath: "/tmp/c.mp4", SHA256: fakeSHA(0), RemoteFileID: ""},
	}
	err := VerifyChunks(chunks)
	if !errors.Is(err, ErrStockChunkNotFinalized) {
		t.Fatalf("missing remote_file_id: want ErrStockChunkNotFinalized, got %v", err)
	}
}

// TestVerifyChunks_SHA256Missing asserts the gate surfaces
// ErrStockChunkHashMissing per P0 2.4 — the consumer of the
// Qdrant index event rejects empty source_version terminal,
// so we fail-closed at the orchestrator (don't dispatch an
// event the consumer rejects).
func TestVerifyChunks_SHA256Missing(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:run:chunk:0", LocalPath: "/tmp/c.mp4", RemoteFileID: "drive-id-0", SHA256: ""},
	}
	err := VerifyChunks(chunks)
	if !errors.Is(err, ErrStockChunkHashMissing) {
		t.Fatalf("missing sha256: want ErrStockChunkHashMissing, got %v", err)
	}
}

// ── VerifyMetadata ─────────────────────────────────────────────────────

// TestVerifyMetadata_HappyPath validates the gate is silent when
// the metadata artifact has LocalPath + RemoteFileID + SHA256.
func TestVerifyMetadata_HappyPath(t *testing.T) {
	m := MetadataState{
		LocalPath: "/tmp/m.json", SHA256: fakeSHA(99), SizeBytes: 512,
		RemoteFileID: "drive-id-m", RemoteWebViewLink: "https://drive/m",
	}
	if err := VerifyMetadata(m); err != nil {
		t.Fatalf("happy path: want nil, got %v", err)
	}
}

// TestVerifyMetadata_LocalPathMissing asserts the gate surfaces
// ErrStockMetadataNotPublished for missing LocalPath.
func TestVerifyMetadata_LocalPathMissing(t *testing.T) {
	err := VerifyMetadata(MetadataState{LocalPath: "", SHA256: fakeSHA(99), RemoteFileID: "drive-id-m"})
	if !errors.Is(err, ErrStockMetadataNotPublished) {
		t.Fatalf("missing localpath: want ErrStockMetadataNotPublished, got %v", err)
	}
}

// TestVerifyMetadata_RemoteFileIDMissing asserts the gate surfaces
// ErrStockMetadataNotPublished for missing RemoteFileID — the
// failure mode if the publisher drops metadata.json on the floor.
func TestVerifyMetadata_RemoteFileIDMissing(t *testing.T) {
	err := VerifyMetadata(MetadataState{LocalPath: "/tmp/m.json", SHA256: fakeSHA(99), RemoteFileID: ""})
	if !errors.Is(err, ErrStockMetadataNotPublished) {
		t.Fatalf("missing remote_file_id: want ErrStockMetadataNotPublished, got %v", err)
	}
}

// TestVerifyMetadata_SHA256Missing asserts the gate surfaces
// ErrStockMetadataNotPublished for missing SHA256 (P0 2.4).
func TestVerifyMetadata_SHA256Missing(t *testing.T) {
	err := VerifyMetadata(MetadataState{LocalPath: "/tmp/m.json", SHA256: "", RemoteFileID: "drive-id-m"})
	if !errors.Is(err, ErrStockMetadataNotPublished) {
		t.Fatalf("missing sha256: want ErrStockMetadataNotPublished, got %v", err)
	}
}

// ── BuildFinalizationRequest ──────────────────────────────────────────

// TestBuildFinalizationRequest_GatesFirst asserts that gate
// failures short-circuit BuildFinalizationRequest before composing
// the request — a partial FinalizationRequest would fail
// JobFinalizer.ValidateRequest and surface a confusing typed
// error; better to surface the orchestrator-level sentinel
// verbatim.
func TestBuildFinalizationRequest_GatesFirst(t *testing.T) {
	_, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte("{}"),
		nil, // empty chunks → ErrStockNoChunksFinalized
		MetadataState{})
	if !errors.Is(err, ErrStockNoChunksFinalized) {
		t.Fatalf("empty chunks: want ErrStockNoChunksFinalized, got %v", err)
	}

	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:job-1:chunk:0", LocalPath: "/tmp/c.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-0", SizeBytes: 1024, Filename: "c.mp4"},
	}
	_, err = BuildFinalizationRequest("job-1", validLease("job-1"), []byte("{}"),
		chunks,
		MetadataState{LocalPath: "/tmp/m.json"}, // missing RemoteFileID, SHA256
	)
	if !errors.Is(err, ErrStockMetadataNotPublished) {
		t.Fatalf("incomplete metadata: want ErrStockMetadataNotPublished, got %v", err)
	}
}

// TestBuildFinalizationRequest_HappyPath validates the request
// composition — Lease echoed, ResultManifest populated, 1 metadata
// artifact + N chunk artifacts, all Required:true.
func TestBuildFinalizationRequest_HappyPath(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:job-1:chunk:0", LocalPath: "/tmp/c0.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-0", SizeBytes: 1024, Filename: "c0.mp4"},
		{Index: 1, ArtifactID: "stock:job-1:chunk:1", LocalPath: "/tmp/c1.mp4", SHA256: fakeSHA(1), RemoteFileID: "drive-1", SizeBytes: 2048, Filename: "c1.mp4"},
	}
	m := MetadataState{LocalPath: "/tmp/m.json", SHA256: fakeSHA(2), SizeBytes: 512,
		RemoteFileID: "drive-m", RemoteWebViewLink: "https://drive/m"}

	req, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":1}`), chunks, m)
	if err != nil {
		t.Fatalf("happy path: want nil, got %v", err)
	}
	if req.Lease.JobID != "job-1" {
		t.Fatalf("lease.JobID=%q, want job-1", req.Lease.JobID)
	}
	if req.Result.JobID != "job-1" {
		t.Fatalf("result.JobID=%q, want job-1", req.Result.JobID)
	}
	if len(req.Artifacts) != 3 {
		t.Fatalf("artifacts count=%d, want 3 (1 metadata + 2 chunks)", len(req.Artifacts))
	}
	for i, a := range req.Artifacts {
		if a.Requirement != finalization.ArtifactRequirementRequired {
			t.Fatalf("artifact[%d] (%s) Requirement=%v, want %v", i, a.ArtifactID, a.Requirement, finalization.ArtifactRequirementRequired)
		}
		if a.IdempotencyKey == "" || a.SHA256 == "" {
			t.Fatalf("artifact[%d] missing IdempotencyKey or SHA256", i)
		}
	}
}

// TestBuildFinalizationRequest_LeaseMismatch asserts the
// Lease.JobID must match the Request JobID. Mismatched leases
// indicate a programming error in the broker wiring — fail-fast.
func TestBuildFinalizationRequest_LeaseMismatch(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:job-1:chunk:0", LocalPath: "/tmp/c0.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-0", SizeBytes: 1024, Filename: "c0.mp4"},
	}
	m := MetadataState{LocalPath: "/tmp/m.json", SHA256: fakeSHA(2), SizeBytes: 512, RemoteFileID: "drive-m"}
	_, err := BuildFinalizationRequest("job-1", validLease("job-2"), []byte("{}"), chunks, m)
	if err == nil {
		t.Fatal("lease mismatch: want error, got nil")
	}
	if errors.Is(err, ErrStockNoChunksFinalized) || errors.Is(err, ErrStockMetadataNotPublished) {
		t.Fatalf("lease mismatch should NOT be classed as a gate failure, got %v", err)
	}
}

// TestBuildFinalizationRequest_JobIDEmpty asserts jobID="" is a
// fail-fast programming error (would propagate to JobFinalizer
// with empty request.JobID).
func TestBuildFinalizationRequest_JobIDEmpty(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:job-1:chunk:0", LocalPath: "/tmp/c0.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-0", SizeBytes: 1024, Filename: "c0.mp4"},
	}
	m := MetadataState{LocalPath: "/tmp/m.json", SHA256: fakeSHA(2), SizeBytes: 512, RemoteFileID: "drive-m"}
	_, err := BuildFinalizationRequest("", validLease(""), []byte("{}"), chunks, m)
	if err == nil {
		t.Fatal("empty jobID: want error, got nil")
	}
}

// ── Idempotency round-trip ────────────────────────────────────────────

// TestBuildFinalizationRequest_Idempotent asserts byte-stable
// FinalizationRequest across re-runs with the same inputs. A
// retry on transient broker failure must produce a byte-equivalent
// request so JobFinalizer.IdempotencyCache + UNIQUE index collapse
// to a single row.
func TestBuildFinalizationRequest_Idempotent(t *testing.T) {
	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:job-1:chunk:0", LocalPath: "/tmp/c0.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-0", SizeBytes: 1024, Filename: "c0.mp4"},
	}
	m := MetadataState{LocalPath: "/tmp/m.json", SHA256: fakeSHA(2), SizeBytes: 512, RemoteFileID: "drive-m"}

	r1, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":"bar"}`), chunks, m)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":"bar"}`), chunks, m)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	r3, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":"bar"}`), chunks, m)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}

	// Same IdempotencyKeys on each invocation per artifact.
	if r1.Artifacts[0].IdempotencyKey != r2.Artifacts[0].IdempotencyKey {
		t.Fatalf("metadata idempotency key drift: %q vs %q", r1.Artifacts[0].IdempotencyKey, r2.Artifacts[0].IdempotencyKey)
	}
	if r1.Artifacts[1].IdempotencyKey != r3.Artifacts[1].IdempotencyKey {
		t.Fatalf("chunk idempotency key drift: %q vs %q", r1.Artifacts[1].IdempotencyKey, r3.Artifacts[1].IdempotencyKey)
	}
}

// ── helper ────────────────────────────────────────────────────────────

func validLease(jobID string) finalization.Lease {
	return finalization.Lease{
		LeaseID:  "lease-1",
		JobID:    jobID,
		WorkerID: "worker-stock-sync",
		Attempt:  1,
		// ExpiresAt zero => Lease.Valid()==true (now.Before(zero)=false), so omit
	}
}
