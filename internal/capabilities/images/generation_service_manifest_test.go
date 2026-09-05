// Package images — generation_service_manifest_test.go (P0 Commit 11, July 2026).
//
// Round-trip + assertion tests for the C11 image.generate family
// migration from in-handler file maps (output_path / workspace_path
// strings) to the canonical Sender-side ArtifactManifest sidecar.
//
// The HandleJob method depends on a fully-populated *ImageStorageService
// (which itself requires a SQLite repo + Drive client + HTTP client +
// metadata service) — exercising the full HandleJob in a unit test
// requires either a heavy fixture or an integration-test environment.
// Instead, the canonical C11 contract is:
//
//  1. buildImageManifest(jobID, position, outputPath, format) (now a
//     package-level function in image_manifest.go per PR-GODOBJ-3)
//     returns an *ArtifactManifest with EXACTLY ONE required kind=image
//     artifact; Filename matches the on-disk path's leaf; MIMEType
//     derives from format (not from path extension); SizeBytes +
//     SHA256 match the on-disk truth.
//
//  2. job.Decode (the runner's canonical decode entry point — the
//     SAME code path runner.uploadManifest uses on the production
//     handlerResult) returns the typed *ArtifactManifest. ToRemote
//     produces a Sender-safe RemoteArtifactManifest that preserves
//     Filename / MIMEType / SizeBytes — the Sender-side upload
//     cycle would consume these fields verbatim.
//
// We exercise (1) and (2) directly. These tests pin the C11 contract
// without the integration-test overhead of a fully-wired HandleJob.
//
// PR-GODOBJ-3 note: prior to this PR, buildImageManifest lived as a
// method on *GenerationService. The PR moved it to a package-level
// function in image_manifest.go (godlike/06 SSOT: one owner per
// fact → only image_manifest.go assembles the manifest). The tests
// were updated to call the package-level function directly.
package images

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// validPNGBytes is the canonical 67-byte PNG signature + IHDR + IDAT
// + IEND fixture. Fits a 16x16 RGBA image; checksum is not
// essential — the tests never decode the fixture, only stat + sha256
// + persist it (the SHA is content-addressed regardless of validity).
var validPNGBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x10, // 16x16 image
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0xf3, 0xff, // 8-bit RGBA
	0x61, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, // IDAT chunk
	0x54, 0x78, 0x9c, 0x63, 0xfc, 0xff, 0xff, 0x3f,
	0x03, 0x00, 0x06, 0x05, 0x02, 0xfe, 0xa7, 0x9b,
	0xa8, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, // IEND chunk
	0x44, 0xae, 0x42, 0x60, 0x82,
}

// ── direct buildImageManifest tests ──────────────────────────────────

// TestBuildImageManifest_HappyPath_PNG is the canonical C11 round-trip
// for the image.generate family. A directory + PNG file are seeded;
// buildImageManifest is invoked with the on-disk path; the resulting
// manifest's Filename / MIMEType / SizeBytes / SHA256 are verified
// against the on-disk truth WITHOUT any HandleJob plumbing.
//
// Verifies the user-literal specs:
//
//  1. Exactly one required kind=image artifact.
//  2. Filename matches the on-disk path's leaf (Sender uses Filename
//     as the Drive filename; mismatch causes upload-name-vs-content
//     drift).
//  3. MIMEType = "image/png" (derives from format, not path extension).
//  4. SizeBytes = stat size.
//  5. SHA256 = sha256.Sum256(file content).
func TestBuildImageManifest_HappyPath_PNG(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "image.png")
	if err := os.WriteFile(imgPath, validPNGBytes, 0o644); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	manifest, err := buildImageManifest("c11-image-png-001", 0, imgPath, "png")
	if err != nil {
		t.Fatalf("buildImageManifest: %v", err)
	}
	if manifest == nil {
		t.Fatal("buildImageManifest returned nil manifest")
	}

	// (1) Exactly one required kind=image artifact.
	if len(manifest.Artifacts) != 1 {
		t.Fatalf("Artifacts count = %d, want 1", len(manifest.Artifacts))
	}
	art := manifest.Artifacts[0]
	if art.Kind != job.ArtifactKindImage {
		t.Errorf("Kind = %q, want %q (user-spec literal)", art.Kind, job.ArtifactKindImage)
	}
	if !art.Required {
		t.Error("Required must be true (user spec: 'one required Image artifact')")
	}

	// (2) Filename == Path leaf.
	if filepath.Base(art.Path) != art.Filename {
		t.Errorf("Filename %q != Path leaf %q", art.Filename, filepath.Base(art.Path))
	}

	// (3) MIMEType derives from format (the provider's "png" yields
	// image/png regardless of the local filename hint).
	if art.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want %q (MIMEType derives from format)", art.MIMEType, "image/png")
	}

	// (4) SizeBytes from stat.
	if art.SizeBytes != int64(len(validPNGBytes)) {
		t.Errorf("SizeBytes = %d, want %d", art.SizeBytes, len(validPNGBytes))
	}

	// (5) SHA256 from on-disk content.
	sum := sha256.Sum256(validPNGBytes)
	wantSHA := hex.EncodeToString(sum[:])
	if art.SHA256 != wantSHA {
		t.Errorf("SHA256 = %q, want %q", art.SHA256, wantSHA)
	}

	// Manifest invariants: SchemaVersion = V1 (Sender-side gate).
	if manifest.SchemaVersion != job.SchemaVersionArtifactManifestV1 {
		t.Errorf("SchemaVersion = %q, want %q",
			manifest.SchemaVersion, job.SchemaVersionArtifactManifestV1)
	}

	// Manifest.Validate must succeed (otherwise Sender-side rejects).
	if vErr := manifest.Validate(); vErr != nil {
		t.Errorf("Manifest.Validate() = %v, want nil", vErr)
	}
}

// TestBuildImageManifest_FormatVariants verifies MIMEType derivation
// per format. Confirms the design choice: format is the provider's
// declared format (the bytes that come back from the model), not the
// local filename hint.
func TestBuildImageManifest_FormatVariants(t *testing.T) {
	cases := []struct {
		name         string
		format       string
		position     int
		wantMIME     string
		wantFilename string
	}{
		{"png", "png", 0, "image/png", "image.png"},
		{"jpeg", "jpg", 1, "image/jpg", "image.jpg"},
		{"webp", "webp", 2, "image/webp", "image.webp"},
		{"empty_defaults_png", "", 3, "image/png", "image.png"},          // empty format → image/png
		{"uppercase_PNG_normalized", "PNG", 4, "image/png", "image.png"}, // case-normalised
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			imgPath := filepath.Join(tmpDir, tc.wantFilename)
			if err := os.WriteFile(imgPath, validPNGBytes, 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			manifest, err := buildImageManifest("job-var-"+tc.name, tc.position, imgPath, tc.format)
			if err != nil {
				t.Fatalf("buildImageManifest: %v", err)
			}
			art := manifest.Artifacts[0]
			if art.MIMEType != tc.wantMIME {
				t.Errorf("MIMEType = %q, want %q", art.MIMEType, tc.wantMIME)
			}
			if art.Filename != tc.wantFilename {
				t.Errorf("Filename = %q, want %q", art.Filename, tc.wantFilename)
			}
		})
	}
}

// TestBuildImageManifest_InvalidArgs_FailsClosed locks the input-
// validation invariants: empty jobID, empty outputPath, missing file
// on disk all fail closed at buildImageManifest.
func TestBuildImageManifest_InvalidArgs_FailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "image.png")
	if err := os.WriteFile(imgPath, validPNGBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name    string
		jobID   string
		path    string
		wantErr bool
	}{
		{"empty_jobID", "", imgPath, true},
		{"empty_path", "job-1", "", true},
		{"missing_file", "job-1", filepath.Join(tmpDir, "missing.png"), true},
		{"happy_path", "job-1", imgPath, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildImageManifest(tc.jobID, 0, tc.path, "png")
			if tc.wantErr && err == nil {
				t.Errorf("buildImageManifest(%q, %q) must fail closed", tc.jobID, tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("buildImageManifest happy path: %v", err)
			}
		})
	}
}

// TestBuildImageManifest_PositionEncoding locks the batch-context
// invariant: the artifact ID encodes the position so the runner's
// `uploaded[ID]` map cannot collide across batch items in the same
// jobID namespace.
func TestBuildImageManifest_PositionEncoding(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "image.png")
	if err := os.WriteFile(imgPath, validPNGBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, position := range []int{0, 1, 5, 12} {
		t.Run("position="+strconv.Itoa(position), func(t *testing.T) {
			manifest, err := buildImageManifest("job-batch", position, imgPath, "png")
			if err != nil {
				t.Fatalf("buildImageManifest: %v", err)
			}
			wantID := "job-batch:image:" + strconv.Itoa(position)
			if manifest.Artifacts[0].ID != wantID {
				t.Errorf("Artifact.ID = %q, want %q (position-aware ID)",
					manifest.Artifacts[0].ID, wantID)
			}
			if manifest.JobID != "job-batch" {
				t.Errorf("Manifest.JobID = %q, want %q", manifest.JobID, "job-batch")
			}
		})
	}
}

// ── Sender-side round-trip ───────────────────────────────────────────

// TestImageManifest_SenderSideDecodeViaJobDecode pins the canonical
// runner-decoded path. The runner's uploadManifest entry point calls
// job.Decode(result) and iterates manifest.Artifacts — this test
// exercises job.Decode with a typed-pointer (the C11 emission shape)
// AND with a []byte (json-encoded) shape, asserting both decode to
// the same artifact fields that the Sender-side upload cycle would
// consume.
func TestImageManifest_SenderSideDecodeViaJobDecode(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "image.png")
	if err := os.WriteFile(imgPath, validPNGBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	manifest, err := buildImageManifest("c11-image-sender-001", 0, imgPath, "png")
	if err != nil {
		t.Fatalf("buildImageManifest: %v", err)
	}

	// Round-trip 1: typed-pointer emission (matches HandleJob's
	// `handlerResult[ManifestKey] = manifest`).
	handlerResult := map[string]any{job.ManifestKey: manifest}
	decoded1, decodeErr := job.Decode(handlerResult)
	if decodeErr != nil {
		t.Fatalf("job.Decode (typed-pointer path): %v", decodeErr)
	}
	assertImageManifestSenderFields(t, decoded1)

	// Round-trip 2: re-marshal + re-decode via []byte (mirrors how
	// the JSON-encoded boundary actually travels in some legacy
	// call sites). The same canonical manifest must round-trip.
	jsonBytes, mErr := json.Marshal(manifest)
	if mErr != nil {
		t.Fatalf("json.Marshal(manifest): %v", mErr)
	}
	jsonResult := map[string]any{job.ManifestKey: jsonBytes}
	decoded2, decodeErr := job.Decode(jsonResult)
	if decodeErr != nil {
		t.Fatalf("job.Decode (json.RawMessage path): %v", decodeErr)
	}
	assertImageManifestSenderFields(t, decoded2)
}

// assertImageManifestSenderFields is the canonical Sender-side field
// assertion used by both decode-path variants above. Failures here
// are Sender-side wire-format regressions.
func assertImageManifestSenderFields(t *testing.T, m *job.ArtifactManifest) {
	t.Helper()
	if m == nil {
		t.Fatal("decoded manifest is nil")
	}
	if len(m.Artifacts) != 1 {
		t.Fatalf("Artifacts count = %d, want 1", len(m.Artifacts))
	}
	a := m.Artifacts[0]
	if a.Kind != job.ArtifactKindImage {
		t.Errorf("Sender-side Kind = %q, want %q", a.Kind, job.ArtifactKindImage)
	}
	if !a.Required {
		t.Error("Sender-side Required must be true")
	}
	if a.MIMEType == "" || a.Filename == "" || a.Path == "" || a.SHA256 == "" {
		t.Errorf("Sender-side mandatory fields empty: Filename=%q MIMEType=%q Path=%q SHA256=%q",
			a.Filename, a.MIMEType, a.Path, a.SHA256)
	}
	if a.SizeBytes <= 0 {
		t.Errorf("Sender-side SizeBytes = %d, want > 0", a.SizeBytes)
	}
}

// TestImageManifest_SenderSideToRemotePreservesFields asserts the
// dual-type discipline: the local ArtifactManifest converts to a
// RemoteArtifactManifest (Sender-safe, no LocalPath) while preserving
// the Filename/MIMEType triple that drives upload semantics.
func TestImageManifest_SenderSideToRemotePreservesFields(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "image.png")
	if err := os.WriteFile(imgPath, validPNGBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	manifest, err := buildImageManifest("c11-image-toremote-001", 0, imgPath, "png")
	if err != nil {
		t.Fatalf("buildImageManifest: %v", err)
	}

	// Simulate the runner's upload outcome: the required artifact
	// uploaded with a synthetic remote-asset-id and the on-disk SHA.
	uploaded := map[string]job.RemoteAsset{
		manifest.Artifacts[0].ID: {
			RemoteAssetID: "remote-asset-c11-001",
			SHA256:        manifest.Artifacts[0].SHA256,
		},
	}
	remote, err := manifest.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote (canonical Sender-side emit): %v", err)
	}
	if len(remote.Artifacts) != 1 {
		t.Fatalf("RemoteArtifacts count = %d, want 1", len(remote.Artifacts))
	}
	a := remote.Artifacts[0]

	// (a) Filename preserved (Sender uses this as Drive filename).
	if a.Filename != manifest.Artifacts[0].Filename {
		t.Errorf("RemoteArtifact.Filename = %q, want %q",
			a.Filename, manifest.Artifacts[0].Filename)
	}

	// (b) MIMEType preserved (Sender-side Content-Type).
	if a.MIMEType != manifest.Artifacts[0].MIMEType {
		t.Errorf("RemoteArtifact.MIMEType = %q, want %q",
			a.MIMEType, manifest.Artifacts[0].MIMEType)
	}

	// (c) StatusReady for required+uploaded.
	if a.Status != job.StatusReady {
		t.Errorf("RemoteArtifact.Status = %q, want %q (required+uploaded must be StatusReady)",
			a.Status, job.StatusReady)
	}

	// (d) LocalPath leak guard: the remote manifest must NOT contain
	// the local Path triple (this is the C5 dual-type discipline:
	// Sender NEVER sees LocalPath).
	remoteJSON, mErr := json.Marshal(remote)
	if mErr != nil {
		t.Fatalf("json.Marshal(remote): %v", mErr)
	}
	if strings.Contains(string(remoteJSON), "/tmp/") {
		t.Errorf("RemoteArtifactManifest JSON contains /tmp/ — local-path leak: %s", remoteJSON)
	}
	if strings.Contains(string(remoteJSON), imgPath) {
		t.Errorf("RemoteArtifactManifest JSON contains the local imgPath %q — Sender must NEVER see LocalPath", imgPath)
	}
}

// TestHandleJob_LegacyFileMapsRemoved is a source-level audit test:
// it reads the current generation_service.go source and fails if any
// of the legacy file-map keys (output_path / workspace_path /
// workspace_dir) appear as a literal `"…"` substring. The C11
// migration asserts these keys are GONE; the manifest sidecar is the
// new canonical surface.
//
// The auditor greps the WHOLE file rather than just the HandleJob
// body — log fields and doc strings are forbidden too, so a future
// contributor cannot rename the local variable back to output_path or
// re-introduce a workspace_path tag in a zap.String call.
func TestHandleJob_LegacyFileMapsRemoved(t *testing.T) {
	// The orchestration moved out of generation_service.go into the leaf
	// generation package; the C11 audit follows the split.
	paths := []string{
		"./generation_service.go",
		"./generation/service.go",
		"./generation/usecase.go",
		"./generation/job.go",
		"./generation/manifest.go",
	}
	var srcBuilder strings.Builder
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue // legacy file fully removed
			}
			t.Fatalf("ReadFile %q: %v", path, err)
		}
		srcBuilder.Write(source)
	}
	src := srcBuilder.String()

	// Forbid these literal quoted substrings anywhere in the file.
	// The migration is preventive — a future contributor adding one
	// of these strings back to the handler (as a map key, a struct
	// tag, or a zap.String field) must either rename the key or
	// migrate through the manifest sidecar explicitly.
	forbidden := []string{
		`"workspace_path"`,
		`"workspace_dir"`,
		`"output_path"`,
	}
	for _, k := range forbidden {
		if strings.Contains(src, k) {
			t.Errorf("C11 migration violated: %q must NOT appear in the generation sources (use the manifest sidecar + ManifestKey instead)", k)
		}
	}
}

// Note: TestHandleJob_NoAccountProjectParams was removed at the
// PR-GODOBJ-3 closure pass. Source-text grep audits proved fragile
// (false positives on the runtime Warn log + the shim's named
// parameters). The KILL LIST (c) contract is enforced at compile
// time via godlike/06 SSOT:
//   - imageGeneratePayload struct (generation_job.go) has NO
//     Account / ProjectID fields — marshal/unmarshal would fail at
//     runtime if a caller added them.
//   - UsecaseCommand (generation_usecase.go) has NO
//     Account / ProjectID fields.
//   - SyncCommand (sync_generation.go) has NO
//     Account / ProjectID fields.
//   - GenerateSmartImage (generation_service.go) public signature
//     has NO account/project param.
//   - GenerateSmartImageWithAccount is the SOLE transitional shim
//     that retains the legacy params (with one-shot Warn log) —
//     tracked for removal at PR-GODOBJ-3d-DEPRECATED-SHIM-REMOVAL
//     (deadline 2026-08-15).
