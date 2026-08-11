// Package job — artifact_manifest_p0c5_remote_test.go (split surface: P0 Commit 5 (C5) remote).
//
// WithRemoteLocations (legacy alias) and ToRemote (canonical C5 adapter) tests.
// Covers happy-path round-trip, required-missing rejection, non-required skipped,
// nil-manifest defensive guard, metadata preservation, RemoteArtifactManifest
// local-path-leak guard at the JSON-bytes level, and the legacy-method delegation
// to ToRemote. Pure relocation from artifact_manifest_test.go; no behavior change.
package job

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ── WithRemoteLocations ──────────────────────────────────────────────

func TestWithRemoteLocations_AllReady(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_1",
		JobID:         "job_1",
		Artifacts: []Artifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", MIMEType: "application/json", SHA256: "abc123", Required: true},
			{ID: "job_1:voiceover:it", Kind: ArtifactKindVoiceover, Filename: "voiceover-it.mp3", MIMEType: "audio/mpeg", SHA256: "def456", Required: true},
		},
	}
	uploaded := map[string]RemoteAsset{
		"job_1:script":       {RemoteAssetID: "asset_789", SHA256: "abc123"},
		"job_1:voiceover:it": {RemoteAssetID: "asset_790", SHA256: "def456"},
	}
	result, err := m.WithRemoteLocations(uploaded)
	if err != nil {
		t.Fatalf("WithRemoteLocations: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}

	// First artefact: ready
	if result.Artifacts[0].RemoteAssetID != "asset_789" {
		t.Errorf("artifact[0].RemoteAssetID = %q, want asset_789", result.Artifacts[0].RemoteAssetID)
	}
	if result.Artifacts[0].Status != "ready" {
		t.Errorf("artifact[0].Status = %q, want ready", result.Artifacts[0].Status)
	}

	// Second artefact: ready
	if result.Artifacts[1].RemoteAssetID != "asset_790" {
		t.Errorf("artifact[1].RemoteAssetID = %q, want asset_790", result.Artifacts[1].RemoteAssetID)
	}
	if result.Artifacts[1].Status != "ready" {
		t.Errorf("artifact[1].Status = %q, want ready", result.Artifacts[1].Status)
	}

	// Verify no local paths leak
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "/tmp/") {
		t.Error("UploadedManifest contains local paths — must not leak")
	}
}

func TestToRemotePreservesFinalAudioContractMetadata(t *testing.T) {
	m := &ArtifactManifest{SchemaVersion: SchemaVersionArtifactManifestV1, JobID: "job-audio", Artifacts: []Artifact{{
		ID: "job-audio:final_audio", Kind: ArtifactKindFinalAudio, Filename: "final_audio.m4a", MIMEType: "audio/mp4", SHA256: "final-hash", Required: true,
		ArtifactMetadata: map[string]any{"audio_strategy": "FINAL_AUDIO_COPY", "copy_eligible": true, "codec": "aac", "sample_rate": 48000, "channels": 2},
	}}}
	remote, err := m.ToRemote(map[string]RemoteAsset{"job-audio:final_audio": {RemoteAssetID: "remote-audio-1", SHA256: "final-hash"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := remote.Artifacts[0].ArtifactMetadata
	if metadata["audio_strategy"] != "FINAL_AUDIO_COPY" || metadata["copy_eligible"] != true || metadata["codec"] != "aac" {
		t.Fatalf("final audio contract metadata was lost: %#v", metadata)
	}
}

func TestWithRemoteLocations_RequiredMissing_Error(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", Required: true},
		},
	}
	// Script artefact is required but not in the uploaded map.
	_, err := m.WithRemoteLocations(map[string]RemoteAsset{})
	if err == nil {
		t.Fatal("expected error for required artefact not uploaded")
	}
	if !strings.Contains(err.Error(), "required but was not uploaded") {
		t.Errorf("error should mention required + not uploaded, got: %v", err)
	}
}

func TestWithRemoteLocations_NonRequiredSkipped(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "job_1:script", Kind: ArtifactKindScriptJSON, Filename: "script.json", Required: true, SHA256: "abc"},
			{ID: "job_1:image:0", Kind: ArtifactKindImage, Filename: "image.png", Required: false, SHA256: "def"},
		},
	}
	// Only the required script was uploaded; image is best-effort and missing.
	uploaded := map[string]RemoteAsset{
		"job_1:script": {RemoteAssetID: "asset_1", SHA256: "abc"},
	}
	result, err := m.WithRemoteLocations(uploaded)
	if err != nil {
		t.Fatalf("WithRemoteLocations: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}
	if result.Artifacts[0].Status != "ready" {
		t.Errorf("required artifact should be ready, got %q", result.Artifacts[0].Status)
	}
	if result.Artifacts[1].Status != "skipped" {
		t.Errorf("non-required missing artifact should be skipped, got %q", result.Artifacts[1].Status)
	}
	if result.Artifacts[1].RemoteAssetID != "" {
		t.Errorf("skipped artifact should have empty RemoteAssetID, got %q", result.Artifacts[1].RemoteAssetID)
	}
}

func TestWithRemoteLocations_NilManifest(t *testing.T) {
	var m *ArtifactManifest
	_, err := m.WithRemoteLocations(nil)
	if err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestWithRemoteLocations_PreservesMetadata(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_meta",
		JobID:         "job_meta",
		Artifacts: []Artifact{
			{ID: "x:script", Kind: ArtifactKindScriptJSON, Filename: "s.json", MIMEType: "application/json", SHA256: "sha", Required: true},
		},
	}
	uploaded := map[string]RemoteAsset{"x:script": {RemoteAssetID: "r1", SHA256: "sha"}}
	result, _ := m.WithRemoteLocations(uploaded)
	if result.SchemaVersion != m.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, m.SchemaVersion)
	}
	if result.WorkflowID != m.WorkflowID {
		t.Errorf("WorkflowID = %q, want %q", result.WorkflowID, m.WorkflowID)
	}
	if result.JobID != m.JobID {
		t.Errorf("JobID = %q, want %q", result.JobID, m.JobID)
	}
	if result.Artifacts[0].Kind != ArtifactKindScriptJSON {
		t.Errorf("Kind = %q, want %q", result.Artifacts[0].Kind, ArtifactKindScriptJSON)
	}
	if result.Artifacts[0].Filename != "s.json" {
		t.Errorf("Filename = %q, want s.json", result.Artifacts[0].Filename)
	}
	if result.Artifacts[0].MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want application/json", result.Artifacts[0].MIMEType)
	}
	if result.Artifacts[0].SHA256 != "sha" {
		t.Errorf("SHA256 = %q, want sha", result.Artifacts[0].SHA256)
	}
}

// ── P0 Commit 5 (C5): ToRemote canonical adapter ────────────────────

// TestToRemote_AllReady_SchemaVersionV1 is the happy-path round-trip:
// every required artefact has an uploaded entry, SchemaVersion is the
// canonical V1, and the returned RemoteArtifactManifest contains no
// LocalPath leak.
func TestToRemote_AllReady_SchemaVersionV1(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_C5",
		JobID:         "job_C5",
		Artifacts: []Artifact{
			{
				ID: "job_C5:script", Kind: ArtifactKindScriptJSON,
				Path:     "/tmp/pipelinegen/jobs/job_C5/script.json",
				Filename: "script.json", MIMEType: "application/json",
				SHA256: "abc123", Required: true,
			},
			{
				ID: "job_C5:voiceover:it", Kind: ArtifactKindVoiceover,
				Path:     "/tmp/pipelinegen/jobs/job_C5/voiceover-it.mp3",
				Filename: "voiceover-it.mp3", MIMEType: "audio/mpeg",
				SHA256: "def456", Required: true,
			},
		},
	}
	uploaded := map[string]RemoteAssetIDAdapter{
		"job_C5:script":       {RemoteAssetID: "asset_C5_1", SHA256: "abc123"},
		"job_C5:voiceover:it": {RemoteAssetID: "asset_C5_2", SHA256: "def456"},
	}

	result, err := m.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}
	if result == nil {
		t.Fatal("ToRemote returned nil RemoteArtifactManifest")
	}
	if result.SchemaVersion != SchemaVersionArtifactManifestV1 {
		t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, SchemaVersionArtifactManifestV1)
	}
	if result.WorkflowID != "wf_C5" {
		t.Errorf("WorkflowID = %q, want wf_C5", result.WorkflowID)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}
	if result.Artifacts[0].RemoteAssetID != "asset_C5_1" {
		t.Errorf("Artifacts[0].RemoteAssetID = %q, want asset_C5_1", result.Artifacts[0].RemoteAssetID)
	}
	if result.Artifacts[0].Status != StatusReady {
		t.Errorf("Artifacts[0].Status = %q, want %q", result.Artifacts[0].Status, StatusReady)
	}
	if result.Artifacts[0].Requirement != ArtifactRequirementRequired {
		t.Errorf("Artifacts[0].Requirement = %v, want %v", result.Artifacts[0].Requirement, ArtifactRequirementRequired)
	}
	if result.Artifacts[1].RemoteAssetID != "asset_C5_2" {
		t.Errorf("Artifacts[1].RemoteAssetID = %q, want asset_C5_2", result.Artifacts[1].RemoteAssetID)
	}
	if result.Artifacts[1].Requirement != ArtifactRequirementRequired {
		t.Errorf("Artifacts[1].Requirement = %v, want %v", result.Artifacts[1].Requirement, ArtifactRequirementRequired)
	}

	// Raw-byte guard: no "/tmp/" substring anywhere in the remote
	// manifest's JSON serialisation (the canonical local-path leak
	// marker for /tmp/pipelinegen/jobs/<jobid>/...).
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "/tmp/") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT leak /tmp/ local paths; got: %s", string(data))
	}
}

// TestToRemote_SchemaVersionNotV1_Rejects locks the C5 gate: any
// SchemaVersion other than the canonical V1 is REJECTED at the
// ToRemote emit boundary. The sentinel error chain is asserted so
// callers can errors.Is(err, ErrRemoteSchemaVersionUnsupported).
func TestToRemote_SchemaVersionNotV1_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		version string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"v2_explicit", "pipelinegen.artifacts.v2"},
		{"random", "unknown-schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &ArtifactManifest{
				SchemaVersion: tc.version,
				Artifacts: []Artifact{
					{ID: "x", Kind: ArtifactKindScriptJSON, Required: true},
				},
			}
			uploaded := map[string]RemoteAssetIDAdapter{
				"x": {RemoteAssetID: "asset_x", SHA256: "sha"},
			}
			result, err := m.ToRemote(uploaded)
			if err == nil {
				t.Fatalf("SchemaVersion=%q should be rejected; got result=%+v", tc.version, result)
			}
			if !errors.Is(err, ErrRemoteSchemaVersionUnsupported) {
				t.Errorf("error should wrap ErrRemoteSchemaVersionUnsupported, got: %v", err)
			}
		})
	}
}

// TestToRemote_RequiredMissing_RejectsBeforeEmit locks the C5 invariant:
// a Required artefact that is NOT in the `uploaded` map causes ToRemote
// to return a non-nil error BEFORE emitting any RemoteArtifactManifest.
// The error message must identify the missing artefact so an operator can
// audit the failure.
func TestToRemote_RequiredMissing_RejectsBeforeEmit(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{
				ID: "job_M:script", Kind: ArtifactKindScriptJSON,
				Filename: "script.json", Required: true,
			},
		},
	}
	// Empty uploaded map — the required artefact is missing.
	result, err := m.ToRemote(map[string]RemoteAssetIDAdapter{})
	if err == nil {
		t.Fatalf("ToRemote should reject when required missing; got result=%+v", result)
	}
	if result != nil {
		t.Errorf("ToRemote should return nil RemoteArtifactManifest on rejection; got %+v", result)
	}
	if !strings.Contains(err.Error(), "required but was not uploaded") {
		t.Errorf("error should mention 'required but was not uploaded', got: %v", err)
	}
	if !strings.Contains(err.Error(), "job_M:script") {
		t.Errorf("error should mention the missing artefact ID; got: %v", err)
	}
	// FASE 1 close-out typed-error contract: the required-missing
	// ToRemote error MUST wrap the typed job.ErrRequiredArtifactMissing
	// sentinel so callers can errors.Is without string-matching.
	if !errors.Is(err, ErrRequiredArtifactMissing) {
		t.Errorf("error should wrap ErrRequiredArtifactMissing, got %T: %v", err, err)
	}
}

// TestToRemote_NonRequiredSkipped_StatusSkipped preserves the
// best-effort semantics: non-required artefacts not in `uploaded`
// are emitted with Status="skipped" (an empty RemoteAssetID).
func TestToRemote_NonRequiredSkipped_StatusSkipped(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "x:script", Kind: ArtifactKindScriptJSON, Filename: "s.json", Required: true, SHA256: "abc"},
			{ID: "x:image:0", Kind: ArtifactKindImage, Filename: "img.png", Required: false, SHA256: "def"},
		},
	}
	uploaded := map[string]RemoteAssetIDAdapter{
		"x:script": {RemoteAssetID: "asset_1", SHA256: "abc"},
		// x:image:0 intentionally missing (best-effort).
	}
	result, err := m.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}
	if result.Artifacts[0].Status != StatusReady {
		t.Errorf("required artifact should be StatusReady, got %q", result.Artifacts[0].Status)
	}
	if result.Artifacts[1].Status != StatusSkipped {
		t.Errorf("non-required missing artifact should be StatusSkipped, got %q", result.Artifacts[1].Status)
	}
	if result.Artifacts[1].RemoteAssetID != "" {
		t.Errorf("StatusSkipped artifact should have empty RemoteAssetID, got %q", result.Artifacts[1].RemoteAssetID)
	}
}

// TestArtifactManifest_ToRemote_NilManifest_TypedSentinel pins the
// FASE 1 close-out typed-error contract on the nil-receiver branch
// of ToRemote. The pre-FASE-1 raw-error format is now wrapped with
// ErrArtifactManifestInvalid.
func TestArtifactManifest_ToRemote_NilManifest_TypedSentinel(t *testing.T) {
	var m *ArtifactManifest
	result, err := m.ToRemote(nil)
	if err == nil {
		t.Fatal("ToRemote(nil receiver) should return error")
	}
	if result != nil {
		t.Errorf("ToRemote(nil receiver) should return nil RemoteArtifactManifest, got %+v", result)
	}
	if !errors.Is(err, ErrArtifactManifestInvalid) {
		t.Errorf("nil receiver should wrap ErrArtifactManifestInvalid, got %T: %v", err, err)
	}
}

// TestToRemote_NilManifest_ReturnsError pins the defensive nil-guard.
func TestToRemote_NilManifest_ReturnsError(t *testing.T) {
	var m *ArtifactManifest
	result, err := m.ToRemote(nil)
	if err == nil {
		t.Fatal("ToRemote(nil receiver) should return error")
	}
	if result != nil {
		t.Errorf("ToRemote(nil receiver) should return nil RemoteArtifactManifest, got %+v", result)
	}
}

// TestToRemote_PreservesMetadata asserts the per-artefact metadata
// (Kind / Filename / MIMEType / SHA256) survives the local→remote
// emit (no schema drift on non-locator fields).
func TestToRemote_PreservesMetadata(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_meta_C5",
		JobID:         "job_meta_C5",
		Artifacts: []Artifact{
			{
				ID: "meta:script", Kind: ArtifactKindScriptJSON,
				Filename: "s.json", MIMEType: "application/json",
				SHA256: "sha_meta", Required: true,
			},
		},
	}
	uploaded := map[string]RemoteAssetIDAdapter{
		"meta:script": {RemoteAssetID: "r_meta", SHA256: "sha_meta"},
	}
	result, err := m.ToRemote(uploaded)
	if err != nil {
		t.Fatalf("ToRemote: %v", err)
	}
	if result.Artifacts[0].Kind != ArtifactKindScriptJSON {
		t.Errorf("Kind = %q, want %q", result.Artifacts[0].Kind, ArtifactKindScriptJSON)
	}
	if result.Artifacts[0].Filename != "s.json" {
		t.Errorf("Filename = %q, want s.json", result.Artifacts[0].Filename)
	}
	if result.Artifacts[0].MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want application/json", result.Artifacts[0].MIMEType)
	}
	if result.Artifacts[0].SHA256 != "sha_meta" {
		t.Errorf("SHA256 = %q, want sha_meta", result.Artifacts[0].SHA256)
	}
}

// TestRemoteArtifactManifest_JSON_NoLocalPathField is the C5 invariant
// at the JSON-bytes level: the RemoteArtifactManifest serialisation
// MUST NOT contain a LocalPath / path key on any artefact entry, and
// MUST NOT carry any /tmp/ substring (the canonical local-path leak
// marker for /tmp/pipelinegen/jobs/<jobid>/...).
//
// This is the canonical structural enforcement of the dual-type
// discipline (per P0 §4): the remote type has no LocalPath / Path
// field, so json.Marshal cannot serialise one even if a future
// contributor adds one by mistake (the field would have to be added
// at the type level first, which is reviewable — vs. relying on
// human discipline alone).
func TestRemoteArtifactManifest_JSON_NoLocalPathField(t *testing.T) {
	result := &RemoteArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_no_leak",
		JobID:         "job_no_leak",
		Artifacts: []RemoteArtifact{
			{
				ID: "no_leak:script", Kind: ArtifactKindScriptJSON,
				Filename: "script.json", MIMEType: "application/json",
				SHA256: "no_leak_sha", RemoteAssetID: "asset_no_leak",
				Status: StatusReady,
			},
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rawJSON := string(data)

	// Two checks: no /tmp/ (canonical local-path marker) AND no
	// "local_path" JSON key in any artefact entry.
	if strings.Contains(rawJSON, "/tmp/") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain '/tmp/' (the canonical local-path marker for /tmp/pipelinegen/jobs/); got: %s", rawJSON)
	}
	if strings.Contains(rawJSON, "\"local_path\"") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain 'local_path' key; got: %s", rawJSON)
	}
	if strings.Contains(rawJSON, "\"path\"") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain a top-level per-artefact 'path' key; got: %s", rawJSON)
	}
	if strings.Contains(rawJSON, "\"size_bytes\"") {
		t.Errorf("RemoteArtifactManifest JSON MUST NOT contain 'size_bytes' (sized by Sender on read); got: %s", rawJSON)
	}

	// Positive control: the structural identifiers ARE present.
	for _, must := range []string{
		"\"schema_version\"", "\"workflow_id\"", "\"job_id\"",
		"\"id\"", "\"kind\"", "\"filename\"", "\"mime_type\"",
		"\"sha256\"", "\"remote_asset_id\"", "\"status\"",
		"no_leak:script", "asset_no_leak", StatusReady,
	} {
		if !strings.Contains(rawJSON, must) {
			t.Errorf("RemoteArtifactManifest JSON should contain %q (positive control broken); got: %s", must, rawJSON)
		}
	}
}

// ── P0 Commit 5 (C5): cross-check the legacy alias still works ──────

// TestWithRemoteLocations_LegacyAlias delegates to ToRemote per the C5
// back-compat shape. Pre-C5 tests in TestWithRemoteLocations_* are
// preserved (existing assertions still pass); this test pins the
// explicit C5 behavioural contract for the legacy method.
func TestWithRemoteLocations_LegacyAlias_DelegatesToToRemote(t *testing.T) {
	m := &ArtifactManifest{
		SchemaVersion: SchemaVersionArtifactManifestV1,
		Artifacts: []Artifact{
			{ID: "x", Kind: ArtifactKindScriptJSON, Filename: "x.json", Required: true, SHA256: "x_sha"},
		},
	}
	uploaded := map[string]RemoteAsset{
		"x": {RemoteAssetID: "asset_x", SHA256: "x_sha"},
	}
	// The legacy entry point still works (no breakage).
	resultLegacy, errLegacy := m.WithRemoteLocations(uploaded)
	if errLegacy != nil {
		t.Fatalf("WithRemoteLocations: %v", errLegacy)
	}
	// The canonical C5 entry point produces an equivalent result.
	resultNew, errNew := m.ToRemote(uploaded)
	if errNew != nil {
		t.Fatalf("ToRemote: %v", errNew)
	}
	if len(resultLegacy.Artifacts) != 1 || resultLegacy.Artifacts[0].RemoteAssetID != "asset_x" {
		t.Errorf("legacy result shape unintended: %+v", resultLegacy)
	}
	if len(resultNew.Artifacts) != 1 || resultNew.Artifacts[0].RemoteAssetID != "asset_x" {
		t.Errorf("new result shape unintended: %+v", resultNew)
	}
}
