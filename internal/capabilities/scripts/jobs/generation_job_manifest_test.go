// Package jobs — generation_job_manifest_test.go (P0 Commit 12,
// July 2026) updated for PR-GODOBJ-4 KILL-K1.
//
// Per KILL-K1, the §8.4 multi-artifact emission is owned by
// adapters.PersistGeneratedArtifacts (filesystem ops), the typed
// manifest assembly is owned by buildManifestFromArtifacts
// (PURE constructor), and the typed ExecutionResult dual-shape
// merge is owned by MergeTypedExecutionEnvelope (PURE marshal/unmarshal).
//
// Round-trip + assertion tests (Sprint 1.0: document generation
// moved to the downstream document.generate job; the script
// postprocessor chain no longer emits a document artifact):
//   - script-json REQUIRED
//   - scenes     OPTIONAL (when generated)
//   - per-scene voiceovers INTENTIONALLY NOT emitted (Aug 2026 P0:
//     the TTS pipeline already publishes them to Drive during
//     generation — finalize publishes only script.json, scenes.json
//     and the certified final_audio.m4a, O(1) uploads)
package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// validScriptResult builds a minimal typed GenerationResult that
// satisfies the §8.4 contract.
func validScriptResult(language string) *script.GenerationResult {
	if language == "" {
		language = "en"
	}
	return &script.GenerationResult{
		ItemID:   "test-item-c12",
		Title:    "C12 Title",
		Language: language,
		Output: script.ScriptOutput{
			Text:      "Scene one says hi. Scene two says bye.",
			WordCount: 8,
			SpecScene: script.SpecSceneOutput{
				Scenes: []script.SpecScene{
					{
						Index: 0,
						Text:  "Scene one says hi.",
						Kind:  "narration",
						Bindings: script.SceneBindings{
							Voiceover: &script.VoiceoverBinding{
								LocalPath: "/tmp/pipelinegen/jobs/test-job/voiceover-en-scene-0.mp3",
							},
						},
					},
					{
						Index: 1,
						Text:  "Scene two says bye.",
						Kind:  "narration",
						Bindings: script.SceneBindings{
							Voiceover: &script.VoiceoverBinding{
								LocalPath: "/tmp/pipelinegen/jobs/test-job/voiceover-en-scene-1.mp3",
							},
						},
					},
				},
			},
		},
		Artifacts: script.ArtifactResult{},
	}
}

func validScriptResult_NoScenes() *script.GenerationResult {
	r := validScriptResult("en")
	r.Output.SpecScene.Scenes = nil
	r.Output.SpecScene.Scenes = []script.SpecScene{}
	return r
}

func validScriptResult_VoiceoverMultiLanguage() *script.GenerationResult {
	// Fixture with 3 scenes carrying voiceover bindings (mixed
	// languages). Used to pin the P0 contract that NO per-scene
	// voiceover manifest artifact is emitted even when every scene
	// has a generated voiceover.
	return &script.GenerationResult{
		ItemID:   "test-item-multilang",
		Title:    "C12 Multilang Title",
		Language: "en",
		Output: script.ScriptOutput{
			Text:      "Multi-language scenes.",
			WordCount: 3,
			SpecScene: script.SpecSceneOutput{
				Scenes: []script.SpecScene{
					{Index: 0, Text: "EN: hi", Kind: "narration",
						Bindings: script.SceneBindings{
							Voiceover: &script.VoiceoverBinding{LocalPath: "/tmp/vo-en-0.mp3"},
						}},
					{Index: 1, Text: "IT: ciao", Kind: "narration",
						Bindings: script.SceneBindings{
							Voiceover: &script.VoiceoverBinding{LocalPath: "/tmp/vo-it-1.mp3"},
						}},
					{Index: 2, Text: "EN: bye", Kind: "narration",
						Bindings: script.SceneBindings{
							Voiceover: &script.VoiceoverBinding{LocalPath: "/tmp/vo-en-2.mp3"},
						}},
				},
			},
		},
	}
}

// canonicalEmit runs the canonical PR-GODOBJ-4 KILL-K1 surface for
// test fixtures: PersistGeneratedArtifacts → buildManifestFromArtifacts
// → MergeTypedExecutionEnvelope. Mirrors the production handler's
// handleSingle/handleBatch flow without going through the broker
// dispatch so tests assert the unit-level surface directly.
//
// PR-OUTBOX-SOURCE-VERSION: every emitted artifact carries SHA256 +
// SizeBytes (the §8.4 contract requires non-empty source_version on
// the FinalizeAsset outbox event). Per-scene voiceover files are no
// longer emitted (Aug 2026 P0) so they do not need fixture files.
// Document artifacts are now produced by the downstream
// document.generate job and are out of scope here.
func canonicalEmit(t *testing.T, jobID string, res *script.GenerationResult) (map[string]any, *job.ArtifactManifest) {
	t.Helper()
	ensureFixtureFiles(t, jobID, res)
	ctx := context.Background()
	artifacts, err := adapters.PersistGeneratedArtifacts(ctx, jobID, res, nil)
	if err != nil {
		t.Fatalf("canonicalEmit: PersistGeneratedArtifacts(%q): %v", jobID, err)
	}
	manifest := buildManifestFromArtifacts(jobID, artifacts)
	if vErr := manifest.Validate(); vErr != nil {
		t.Logf("canonicalEmit: manifest.Validate() returned %v (non-fatal — migration tests assert envelope merge path)", vErr)
	}
	handlerResult := map[string]any{}
	if mErr := MergeTypedExecutionEnvelope(handlerResult, res, manifest); mErr != nil {
		t.Fatalf("canonicalEmit: MergeTypedExecutionEnvelope: %v", mErr)
	}
	return handlerResult, manifest
}

// ensureFixtureFiles creates the job workspace output dir so
// PersistGeneratedArtifacts can write script.json / scenes.json and
// compute their SHA256. Per-scene voiceover files are no longer
// needed on disk — PersistGeneratedArtifacts does not emit them
// (Aug 2026 P0: the TTS pipeline publishes voiceovers during
// generation).
func ensureFixtureFiles(t *testing.T, jobID string, res *script.GenerationResult) {
	t.Helper()
	outDir := filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID, "output")
	if mkErr := os.MkdirAll(outDir, 0o755); mkErr != nil {
		t.Fatalf("ensureFixtureFiles: mkdir %s: %v", outDir, mkErr)
	}
	// Clean up fixture files after the test to avoid /tmp pollution.
	t.Cleanup(func() {
		os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID))
	})
}

// TestPersistGeneratedArtifacts_HappyPath_TwoArtifacts is the
// canonical C12 round-trip e2e (Sprint 1.0: document generation
// retired from the script path; Aug 2026 P0: per-scene voiceovers
// removed from the manifest). With a fully-populated GenerationResult
// (scenes + voiceover bindings; no Document), the manifest must
// contain EXACTLY the 2-artifact shape:
//
//  1. script-json   REQUIRED
//  2. scenes        OPTIONAL (emitted because fixture has 2 scenes)
//
// Per-scene voiceovers must NOT appear: the TTS pipeline already
// publishes them to Drive during generation and no downstream
// consumer reads a finalize re-publication. Total: 2 manifest entries.
func TestPersistGeneratedArtifacts_HappyPath_TwoArtifacts(t *testing.T) {
	res := validScriptResult("en")
	handlerResult, _ := canonicalEmit(t, "test-job-c12-happy", res)

	manifest, decodeErr := job.Decode(handlerResult)
	if decodeErr != nil {
		t.Fatalf("job.Decode(handlerResult): %v", decodeErr)
	}
	if manifest == nil {
		t.Fatal("manifest is nil — MergeTypedExecutionEnvelope did not inject under __artifact_manifest")
	}

	kindCount := map[string]int{}
	for _, a := range manifest.Artifacts {
		kindCount[a.Kind]++
	}

	want := map[string]int{
		job.ArtifactKindScriptJSON: 1,
		job.ArtifactKindScenes:     1,
	}
	for k, wantv := range want {
		if got := kindCount[k]; got != wantv {
			t.Errorf("kind %q: got %d, want %d (manifest kinds do NOT match the 2-artifact spec)", k, got, wantv)
		}
	}

	removedKinds := []string{
		job.ArtifactKindScriptText,
		job.ArtifactKindMetadata,
		job.ArtifactKindEntities,
		job.ArtifactKindImage,
		job.ArtifactKindVoiceover,
	}
	for _, k := range removedKinds {
		if got := kindCount[k]; got != 0 {
			t.Errorf("removed kind %q present in manifest (got %d)", k, got)
		}
	}

	wantRequired := map[string]bool{
		job.ArtifactKindScriptJSON: true,
		job.ArtifactKindScenes:     false,
	}
	for _, k := range []string{
		job.ArtifactKindScriptJSON,
		job.ArtifactKindScenes,
	} {
		gotRequired := false
		for _, a := range manifest.Artifacts {
			if a.Kind == k {
				gotRequired = a.Required
			}
		}
		if gotRequired != wantRequired[k] {
			t.Errorf("required flag for %q: got %v, want %v (Required/Optional map)", k, gotRequired, wantRequired[k])
		}
	}

	if vErr := manifest.Validate(); vErr != nil {
		t.Errorf("manifest.Validate(): %v (manifest should be well-formed)", vErr)
	}

	if manifest.SchemaVersion != job.SchemaVersionArtifactManifestV1 {
		t.Errorf("manifest schema_version = %q, want %q", manifest.SchemaVersion, job.SchemaVersionArtifactManifestV1)
	}
}

func TestPersistGeneratedArtifacts_NoScenes_OmitsScenes(t *testing.T) {
	res := validScriptResult_NoScenes()
	handlerResult, _ := canonicalEmit(t, "test-job-c12-no-scenes", res)

	manifest, _ := job.Decode(handlerResult)
	for _, a := range manifest.Artifacts {
		if a.Kind == job.ArtifactKindScenes {
			t.Errorf("scenes slot present in manifest when SpecScene empty — §8.4 spec says emit only when generated")
		}
	}
}

// TestPersistGeneratedArtifacts_NoVoiceoverArtifacts pins the Aug 2026
// P0 contract: even with 3 scenes that all carry a generated voiceover
// binding, the manifest MUST NOT contain per-scene voiceover artifacts.
// The TTS pipeline already publishes each scene file to Drive during
// generation (links live in the scene bindings consumed by the
// Google-Doc and scenes.json/script.json); finalize stays O(1) uploads.
func TestPersistGeneratedArtifacts_NoVoiceoverArtifacts(t *testing.T) {
	res := validScriptResult_VoiceoverMultiLanguage()
	handlerResult, _ := canonicalEmit(t, "test-job-c12-multilang", res)

	manifest, _ := job.Decode(handlerResult)
	for _, a := range manifest.Artifacts {
		if a.Kind == job.ArtifactKindVoiceover {
			t.Errorf("per-scene voiceover artifact %q present in manifest — finalize must stay O(1) uploads (P0 Aug 2026)", a.ID)
		}
	}
	// The manifest still carries the script-json + scenes pair.
	if len(manifest.Artifacts) != 2 {
		t.Errorf("manifest artifacts = %d, want 2 (script-json + scenes)", len(manifest.Artifacts))
	}
}

// TestPersistGeneratedArtifacts_TypedEnvelopeRouted wraps the handler's
// output and decodes it AS the typed ExecutionResult[GenerationResult]
// envelope (NOT just as a sidecar lookup). Validates that the C10
// dual-shape discipline routes through correctly: Data half round-trips
// via JSON marshal/unmarshal into the typed ExecutionResult.
func TestPersistGeneratedArtifacts_TypedEnvelopeRouted(t *testing.T) {
	res := validScriptResult("en")
	handlerResult, _ := canonicalEmit(t, "test-job-c12-typed-env", res)

	envelopeBytes, mErr := json.Marshal(handlerResult)
	if mErr != nil {
		t.Fatalf("marshal handlerResult: %v", mErr)
	}
	var envelope job.ExecutionResult[script.GenerationResult]
	if uErr := json.Unmarshal(envelopeBytes, &envelope); uErr != nil {
		t.Fatalf("unmarshal into typed ExecutionResult[GenerationResult]: %v", uErr)
	}

	if envelope.Data.ItemID != res.ItemID {
		t.Errorf("envelope.Data.ItemID = %q, want %q", envelope.Data.ItemID, res.ItemID)
	}
	if envelope.Data.Title != res.Title {
		t.Errorf("envelope.Data.Title = %q, want %q", envelope.Data.Title, res.Title)
	}
	if envelope.Data.Language != res.Language {
		t.Errorf("envelope.Data.Language = %q, want %q (typed envelope fields round-tripped)", envelope.Data.Language, res.Language)
	}

	if envelope.Artifacts == nil {
		t.Fatal("envelope.Artifacts is nil — typed dual-shape discipline violated")
	}
	if envelope.Artifacts.SchemaVersion != job.SchemaVersionArtifactManifestV1 {
		t.Errorf("envelope.Artifacts_schema_version = %q, want %q", envelope.Artifacts.SchemaVersion, job.SchemaVersionArtifactManifestV1)
	}
	if envelope.Artifacts.JobID != "test-job-c12-typed-env" {
		t.Errorf("envelope.Artifacts.JobID = %q, want %q", envelope.Artifacts.JobID, "test-job-c12-typed-env")
	}
	if _, hasSidecar := handlerResult[job.ManifestKey]; !hasSidecar {
		t.Errorf("handlerResult missing %q sidecar — runner's job.Decode lookup will fail", job.ManifestKey)
	}
}

// TestPersistGeneratedArtifacts_ScriptJSONOnDisk pins the §8.4 spec
// invariant that script.json is materialized on disk (the worker
// process has to read it for the upload cycle to compute SHA-256
// and stream to Drive). Validates that PersistGeneratedArtifacts
// actually writes the file, not just declares it in the manifest.
func TestPersistGeneratedArtifacts_ScriptJSONOnDisk(t *testing.T) {
	res := validScriptResult("en")
	handlerResult, _ := canonicalEmit(t, "c12-disk-pin-test", res)

	manifest, _ := job.Decode(handlerResult)
	for _, a := range manifest.Artifacts {
		if a.Kind != job.ArtifactKindScriptJSON {
			continue
		}
		if _, statErr := os.Stat(a.Path); statErr != nil {
			t.Errorf("script-json path %q not on disk: %v (validator requires required artefact on disk before upload)", a.Path, statErr)
		}
		if a.SizeBytes <= 0 {
			t.Errorf("script-json SizeBytes = %d, want > 0 (handler must populate)", a.SizeBytes)
		}
		if a.SHA256 == "" {
			t.Errorf(`script-json SHA256 = "", want non-empty`)
		}
	}
}

// TestPersistGeneratedArtifacts_AllArtifactsHaveSHA256 pins the
// PR-OUTBOX-SOURCE-VERSION contract: every artifact in the manifest
// MUST have a non-empty SHA256 and non-zero SizeBytes. Without this,
// FinalizeAsset emits outbox events with empty source_version, which
// the IndexingHandler's parseAndValidateRequest classifies as terminal
// (dead_letter).
func TestPersistGeneratedArtifacts_AllArtifactsHaveSHA256(t *testing.T) {
	res := validScriptResult("en")
	handlerResult, _ := canonicalEmit(t, "c12-sha256-pin", res)

	manifest, decodeErr := job.Decode(handlerResult)
	if decodeErr != nil {
		t.Fatalf("decode: %v", decodeErr)
	}
	if manifest == nil {
		t.Fatal("manifest is nil")
	}
	if len(manifest.Artifacts) == 0 {
		t.Fatal("no artifacts in manifest")
	}
	for _, a := range manifest.Artifacts {
		t.Run("kind="+a.Kind, func(t *testing.T) {
			if a.SHA256 == "" {
				t.Errorf("artifact %q (kind=%s) SHA256 is empty — source_version will be empty in outbox event, causing dead_letter", a.ID, a.Kind)
			}
			if a.SizeBytes <= 0 {
				t.Errorf("artifact %q (kind=%s) SizeBytes = %d, want > 0", a.ID, a.Kind, a.SizeBytes)
			}
		})
	}
}
