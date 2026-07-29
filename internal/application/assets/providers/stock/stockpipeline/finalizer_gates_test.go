package stockpipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
		MetadataState{}, "fp-test")
	if !errors.Is(err, ErrStockNoChunksFinalized) {
		t.Fatalf("empty chunks: want ErrStockNoChunksFinalized, got %v", err)
	}

	chunks := []ChunkState{
		{Index: 0, ArtifactID: "stock:job-1:chunk:0", LocalPath: "/tmp/c.mp4", SHA256: fakeSHA(0), RemoteFileID: "drive-0", SizeBytes: 1024, Filename: "c.mp4"},
	}
	_, err = BuildFinalizationRequest("job-1", validLease("job-1"), []byte("{}"),
		chunks,
		MetadataState{LocalPath: "/tmp/m.json"}, // missing RemoteFileID, SHA256
		"fp-test",
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

	req, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":1}`), chunks, m, "fp-test")
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
	_, err := BuildFinalizationRequest("job-1", validLease("job-2"), []byte("{}"), chunks, m, "fp-test")
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
	_, err := BuildFinalizationRequest("", validLease(""), []byte("{}"), chunks, m, "fp-test")
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

	r1, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":"bar"}`), chunks, m, "fp-test")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":"bar"}`), chunks, m, "fp-test")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	r3, err := BuildFinalizationRequest("job-1", validLease("job-1"), []byte(`{"foo":"bar"}`), chunks, m, "fp-test")
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

// ── ArtifactMetadata round-trip + Source='stock' ─────────────────────

// TestBuildFinalizationRequest_ArtifactMetadata_All22FieldsRoundTrip
// asserts that BuildFinalizationRequest populates ALL 22 metadata keys
// on each chunk's PublishedArtifact.ArtifactMetadata map AND sets
// Source="stock" on both metadata and chunk artifacts. This is the
// canonical regression guard for the semantic-enrichment bridge:
// without it, ChunkState's Title/Round/Tags/Category/SourceProvider/
// DrivePath/etc. are silently lost at the PublishedArtifact boundary
// and the Qdrant PayloadMapper has no rich data.
//
// godlike/07 NO-FAKE-AVAILABILITY: the 22-field list is EXHAUSTIVE —
// if a future PR adds a new field to the chunkMeta map, this test
// must be extended or it will silently pass with the field missing
// (the test asserts PRESENCE, not ABSENCE of extra keys).
func TestBuildFinalizationRequest_ArtifactMetadata_All22FieldsRoundTrip(t *testing.T) {
	// Build a ChunkState with ALL typed enrichment fields populated.
	chunk := ChunkState{
		Index:             2,
		ArtifactID:        "stock:fp-test:chunk:2",
		Filename:          "round-7.mp4",
		LocalPath:         "/tmp/c2.mp4",
		SourceURL:         "https://www.youtube.com/watch?v=YE7VzlLtp-4",
		SourceProvider:    "youtube",
		SourceVideoID:     "YE7VzlLtp-4",
		TotalChunks:       8,
		DrivePath:         "https://drive.google.com/file/d/abc123",
		PolicyVersion:     "stock_timestamp_v1",
		StartSec:          32.0,
		EndSec:            51.0,
		Title:             "La fase di studio e la velocita di Pacquiao",
		Description:       "Round 7: Pacquiao's jab-and-move sequence",
		Round:             7,
		Tags:              []string{"boxing", "pacquiao", "broner"},
		Category:          "Boxe",
		Slug:              "round-7",
		SHA256:            fakeSHA(2),
		SizeBytes:         4096,
		RemoteFileID:      "drive-2",
		RemoteWebViewLink: "https://drive.google.com/file/d/abc123/view",
	}
	m := MetadataState{
		LocalPath:         "/tmp/m.json",
		SHA256:            fakeSHA(99),
		SizeBytes:         512,
		RemoteFileID:      "drive-m",
		RemoteWebViewLink: "https://drive.google.com/file/d/meta/view",
	}

	req, err := BuildFinalizationRequest(
		"job-test-42",
		validLease("job-test-42"),
		[]byte(`{"run":"test"}`),
		[]ChunkState{chunk},
		m,
		"fp-round-trip",
	)
	if err != nil {
		t.Fatalf("BuildFinalizationRequest: %v", err)
	}
	if len(req.Artifacts) != 2 {
		t.Fatalf("artifacts count=%d, want 2 (1 metadata + 1 chunk)", len(req.Artifacts))
	}

	// ── Assert Source='stock' on BOTH artifacts ────────────────────
	for i, a := range req.Artifacts {
		if a.Source != "stock" {
			t.Errorf("artifact[%d] (%s) Source=%q, want %q", i, a.ArtifactID, a.Source, "stock")
		}
	}

	// ── Assert chunk artifact has all 22+ metadata keys ────────────
	chunkArt := req.Artifacts[1] // index 1 = first chunk (0 = metadata)
	meta := chunkArt.ArtifactMetadata
	if meta == nil {
		t.Fatal("chunk ArtifactMetadata is nil — bridge is broken")
	}

	// The 22 deterministic keys (always populated for non-zero values).
	expectedKeys := map[string]any{
		"title":                       chunk.Title,
		"description":                 chunk.Description,
		"start_sec":                   chunk.StartSec,
		"end_sec":                     chunk.EndSec,
		"source_url":                  chunk.SourceURL,
		"source_provider":             chunk.SourceProvider,
		"source_video_id":             chunk.SourceVideoID,
		"total_chunks":                chunk.TotalChunks,
		"drive_path":                  chunk.DrivePath,
		"timestamp_drive_folder_link": chunk.TimestampDriveFolderLink,
		"timestamp_folder_id":         chunk.TimestampFolderID,
		"policy_version":              chunk.PolicyVersion,
		"indexing_status":             "INDEXING_PENDING",
		"chunk_index":                 chunk.Index,
		"job_id":                      "job-test-42",
		"run_fingerprint":             "fp-round-trip",
		"chunk_filename":              chunk.Filename,
		"chunk_duration_sec":          19.0, // end_sec - start_sec = 51 - 32
		"chunk_drive_file_id":         chunk.RemoteFileID,
		"chunk_drive_link":            chunk.RemoteWebViewLink,
		"timestamp_title":             chunk.Title,
		"timestamp_slug":              chunk.Slug,
		"timestamp_start_sec":         chunk.StartSec,
		"timestamp_end_sec":           chunk.EndSec,
	}
	// The 4 conditional keys (populated only when non-zero/non-empty).
	expectedConditional := map[string]any{
		"round":    chunk.Round,
		"tags":     chunk.Tags,
		"category": chunk.Category,
		"slug":     chunk.Slug,
	}

	// Assert all 22 deterministic keys are present with correct values.
	for key, want := range expectedKeys {
		got, ok := meta[key]
		if !ok {
			t.Errorf("chunk ArtifactMetadata missing key %q (want %v)", key, want)
			continue
		}
		// Use fmt.Sprintf for float64 comparison (JSON round-trip may
		// change the concrete type from float64 to any-number).
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("chunk ArtifactMetadata[%q] = %v (%T), want %v (%T)",
				key, got, got, want, want)
		}
	}

	// Assert all 4 conditional keys are present (values are non-zero).
	for key, want := range expectedConditional {
		got, ok := meta[key]
		if !ok {
			t.Errorf("chunk ArtifactMetadata missing conditional key %q (want %v)", key, want)
			continue
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("chunk ArtifactMetadata[%q] = %v (%T), want %v (%T)",
				key, got, got, want, want)
		}
	}

	// ── Assert chunk_duration_sec is end_sec - start_sec ──────────
	dur, ok := meta["chunk_duration_sec"]
	if !ok {
		t.Fatal("chunk_duration_sec missing")
	}
	if durF, ok := dur.(float64); ok {
		if durF != 19.0 {
			t.Errorf("chunk_duration_sec=%.1f, want 19.0 (51-32)", durF)
		}
	} else {
		t.Errorf("chunk_duration_sec is %T, want float64", dur)
	}

	// ── Assert ArtifactID convention ──────────────────────────────
	if chunkArt.ArtifactID != "stock:fp-test:chunk:2" {
		t.Errorf("chunk ArtifactID=%q, want %q", chunkArt.ArtifactID, "stock:fp-test:chunk:2")
	}
	if chunkArt.Kind != finalization.KindVideo {
		t.Errorf("chunk Kind=%v, want KindVideo", chunkArt.Kind)
	}
}

// TestBuildFinalizationRequest_ArtifactMetadata_ZeroFieldsOmitted
// asserts that when Round=0, Tags=nil, Category="", Slug="",
// the 4 conditional keys are NOT present in the chunk metadata map.
// This locks the omitempty contract: deterministic-planner runs
// produce the same wire shape as the pre-PR baseline.
func TestBuildFinalizationRequest_ArtifactMetadata_ZeroFieldsOmitted(t *testing.T) {
	chunk := ChunkState{
		Index:             0,
		ArtifactID:        "stock:fp-zero:chunk:0",
		Filename:          "chunk_000.mp4",
		LocalPath:         "/tmp/c0.mp4",
		StartSec:          10.0,
		EndSec:            20.0,
		Title:             "test",
		SHA256:            fakeSHA(0),
		SizeBytes:         1024,
		RemoteFileID:      "drive-0",
		RemoteWebViewLink: "https://drive/0",
		// Round=0, Tags=nil, Category="", Slug="" — zero values
	}
	m := MetadataState{
		LocalPath: "/tmp/m.json", SHA256: fakeSHA(99),
		SizeBytes: 512, RemoteFileID: "drive-m",
	}

	req, err := BuildFinalizationRequest(
		"job-zero", validLease("job-zero"),
		[]byte("{}"), []ChunkState{chunk}, m, "fp-zero",
	)
	if err != nil {
		t.Fatalf("BuildFinalizationRequest: %v", err)
	}

	chunkMeta := req.Artifacts[1].ArtifactMetadata
	for _, key := range []string{"round", "tags", "category", "slug"} {
		if _, ok := chunkMeta[key]; ok {
			t.Errorf("chunk ArtifactMetadata[%q] should be absent when zero-value, but was present with value %v",
				key, chunkMeta[key])
		}
	}
}

// TestBuildFinalizationRequest_MetadataArtifactHasNoArtifactMetadata
// asserts that the metadata.json artifact (KindMetadata) does NOT
// carry an ArtifactMetadata map — the enrichment bridge only applies
// to chunk artifacts (KindVideo), not the run-level metadata.json.
// This prevents the AssetTxFinalizer from merging spurious keys into
// the metadata.json artifact's media_assets row.
func TestBuildFinalizationRequest_MetadataArtifactHasNoArtifactMetadata(t *testing.T) {
	chunk := ChunkState{
		Index: 0, ArtifactID: "stock:fp-meta:chunk:0",
		Filename: "c.mp4", LocalPath: "/tmp/c.mp4",
		SHA256: fakeSHA(0), SizeBytes: 1024,
		RemoteFileID: "drive-0", RemoteWebViewLink: "https://drive/0",
	}
	m := MetadataState{
		LocalPath: "/tmp/m.json", SHA256: fakeSHA(99),
		SizeBytes: 512, RemoteFileID: "drive-m",
	}

	req, err := BuildFinalizationRequest(
		"job-meta", validLease("job-meta"),
		[]byte("{}"), []ChunkState{chunk}, m, "fp-meta",
	)
	if err != nil {
		t.Fatalf("BuildFinalizationRequest: %v", err)
	}

	// Artifact 0 = metadata.json
	metaArt := req.Artifacts[0]
	if metaArt.Kind != finalization.KindMetadata {
		t.Fatalf("artifact[0] Kind=%v, want KindMetadata", metaArt.Kind)
	}
	if metaArt.ArtifactMetadata != nil {
		t.Errorf("metadata artifact ArtifactMetadata should be nil, got %v", metaArt.ArtifactMetadata)
	}
	// Source should still be 'stock' on the metadata artifact.
	if metaArt.Source != "stock" {
		t.Errorf("metadata artifact Source=%q, want %q", metaArt.Source, "stock")
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
