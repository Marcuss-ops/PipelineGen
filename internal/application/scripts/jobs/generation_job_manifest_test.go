// Package jobs — generation_job_manifest_test.go (P0 Commit 12, July 2026).
//
// Round-trip + assertion tests for the C12 script.generate §8.4
// multi-artifact emission contract.
//
// C12 spec (literal user request):
//   - script-json REQUIRED
//   - document-pdf REQUIRED (when generated)
//   - document-markdown OPTIONAL (reserved slot)
//   - scenes OPTIONAL (when SpecScene has entries)
//   - voiceover OPTIONAL (language-grouped)
//
// Pre-C12 also emitted script_text + metadata + entities + image —
// these are NOT in the §8.4 spec envelope and the C12 audit asserts
// they are GONE from the manifest emission (the typed
// GenerationResult still carries them as Data fields, just not as
// manifest file-sidecar entries).
//
// The §8.4 envelope is built INTERNALLY as a typed
//
//	scriptpkg.ExecutionResult[script.GenerationResult]{Data,Artifacts}
//
// (C10 dual-shape discipline); the function then marshals the envelope
// to bytes + round-trips to map[string]any, AND sets
// handlerResult[__artifact_manifest] = manifest so the runner's
// job.Decode path still works at the wire-protocol boundary.
package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	script "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// validScriptResult builds a minimal typed GenerationResult that
// satisfies the §8.4 contract: scrip text + 2 scenes + 1 voiceover
// (en) + 1 document (with DocLink). Tests below compose variations
// of this fixture to exercise each emission branch.
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
		Artifacts: script.ArtifactResult{
			Document: &script.DocumentArtifact{
				DocLink: "https://docs.google.com/document/d/test-doc-link/edit",
				DocID:   "test-doc-id",
				Status:  "completed",
			},
		},
	}
}

// validScriptResult_NoDocument returns the same shape minus the
// Document artifact; the §8.4 PDF emission slot should NOT appear in
// the manifest when no document was generated.
func validScriptResult_NoDocument() *script.GenerationResult {
	r := validScriptResult("en")
	r.Artifacts.Document = nil
	return r
}

// validScriptResult_NoScenes returns the same shape with empty scenes
// so the §8.4 scenes emission slot should NOT appear.
func validScriptResult_NoScenes() *script.GenerationResult {
	r := validScriptResult("en")
	r.Output.SpecScene.Scenes = nil
	r.Output.SpecScene.Scenes = []script.SpecScene{} // explicit empty
	return r
}

// validScriptResult_VoiceoverMultiLanguage exercises the §8.4
// language-grouped emission: 3 scenes across 2 languages, expect ONE
// manifest entry per language (en + it).
func validScriptResult_VoiceoverMultiLanguage() *script.GenerationResult {
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

// TestBuildAndInjectManifest_HappyPath_FiveArtifacts is the canonical
// C12 round-trip e2e. With a fully-populated GenerationResult (doc +
// scenes + voiceover), the manifest must contain EXACTLY the §8.4
// 5-artifact shape:
//
//  1. script-json    REQUIRED
//  2. document-pdf   REQUIRED (Document.DocLink set in fixture)
//  3. document-markdown — RESERVED SLOT, not emitted (no
//     ArtifactKindMarkdown emission yet)
//  4. scenes         OPTIONAL (emitted because fixture has 2 scenes)
//  5. voiceover      OPTIONAL (emitted because fixture has 2
//     en-scene bindings → manifest dedup'd to
//     ONE entry per language)
//
// Total: 4 manifest entries (NOT 5 — markdown slot is reserved
// without emission).
func TestBuildAndInjectManifest_HappyPath_FiveArtifacts(t *testing.T) {
	h := &GenerateJobHandler{log: zap.NewNop()}
	handlerResult := map[string]any{}
	res := validScriptResult("en")

	h.buildAndInjectManifest("test-job-c12-happy", res, handlerResult)

	// Decode the manifest via the canonical runner-side lookup.
	manifest, decodeErr := scriptpkg.Decode(handlerResult)
	if decodeErr != nil {
		t.Fatalf("job.Decode(handlerResult): %v", decodeErr)
	}
	if manifest == nil {
		t.Fatal("manifest is nil — handler did not inject under __artifact_manifest")
	}

	// Build a kind→count map for clarity.
	kindCount := map[string]int{}
	requiredKinds := []string{}
	for _, a := range manifest.Artifacts {
		kindCount[a.Kind]++
		if a.Required {
			requiredKinds = append(requiredKinds, a.Kind)
		}
	}

	want := map[string]int{
		scriptpkg.ArtifactKindScriptJSON: 1, // (a)
		scriptpkg.ArtifactKindPDF:        1, // (b)
		scriptpkg.ArtifactKindMarkdown:   0, // (c) reserved, NOT emitted
		scriptpkg.ArtifactKindScenes:     1, // (d)
		scriptpkg.ArtifactKindVoiceover:  1, // (e), language-grouped (en)
	}
	for k, wantv := range want {
		if got := kindCount[k]; got != wantv {
			t.Errorf("kind %q: got %d, want %d (manifest kinds do NOT match §8.4 spec)", k, got, wantv)
		}
	}

	// Also assert that pre-C12 kinds (now-removed) are absent.
	removedKinds := []string{
		scriptpkg.ArtifactKindScriptText,
		scriptpkg.ArtifactKindMetadata,
		scriptpkg.ArtifactKindEntities,
		scriptpkg.ArtifactKindImage,
	}
	for _, k := range removedKinds {
		if got := kindCount[k]; got != 0 {
			t.Errorf("removed pre-C12 kind %q present in manifest (got %d)", k, got)
		}
	}

	// Required-set must be exactly: script-json + document-pdf (§8.4 spec).
	wantRequired := map[string]bool{
		scriptpkg.ArtifactKindScriptJSON: true,
		scriptpkg.ArtifactKindPDF:        true,
		scriptpkg.ArtifactKindMarkdown:   false,
		scriptpkg.ArtifactKindScenes:     false,
		scriptpkg.ArtifactKindVoiceover:  false,
	}
	for _, k := range []string{
		scriptpkg.ArtifactKindScriptJSON,
		scriptpkg.ArtifactKindPDF,
		scriptpkg.ArtifactKindMarkdown,
		scriptpkg.ArtifactKindScenes,
		scriptpkg.ArtifactKindVoiceover,
	} {
		gotRequired := false
		for _, a := range manifest.Artifacts {
			if a.Kind == k {
				gotRequired = a.Required
			}
		}
		if gotRequired != wantRequired[k] {
			t.Errorf("required flag for %q: got %v, want %v (§8.4 Required/Optional map)", k, gotRequired, wantRequired[k])
		}
	}

	// Manifest.Validate must succeed.
	if vErr := manifest.Validate(); vErr != nil {
		t.Errorf("manifest.Validate(): %v (manifest should be well-formed)", vErr)
	}

	// SchemaVersion must be V1.
	if manifest.SchemaVersion != scriptpkg.SchemaVersionArtifactManifestV1 {
		t.Errorf("manifest schema_version = %q, want %q", manifest.SchemaVersion, scriptpkg.SchemaVersionArtifactManifestV1)
	}
}

// TestBuildAndInjectManifest_NoDocument_OmitsPDF locks the §8.4
// "REQUIRED when Document.DocLink set" semantics — if the document
// pipeline did NOT produce a document (GenerateDocument=false or
// pipeline skipped), the pdf slot must NOT appear in the manifest.
// A required artefact with empty Path would fail Validate; the
// §8.4 spec's "Required when generated" conditional emission
// prevents that.
func TestBuildAndInjectManifest_NoDocument_OmitsPDF(t *testing.T) {
	h := &GenerateJobHandler{log: zap.NewNop()}
	handlerResult := map[string]any{}
	res := validScriptResult_NoDocument()

	h.buildAndInjectManifest("test-job-c12-no-doc", res, handlerResult)

	manifest, decodeErr := scriptpkg.Decode(handlerResult)
	if decodeErr != nil {
		t.Fatalf("decode: %v", decodeErr)
	}
	for _, a := range manifest.Artifacts {
		if a.Kind == scriptpkg.ArtifactKindPDF {
			t.Errorf("PDF slot present in manifest when Document.DocLink empty — §8.4 spec says emit only when generated")
		}
	}
}

// TestBuildAndInjectManifest_NoScenes_OmitsScenes locks the §8.4
// "scenes OPTIONAL" semantics — empty SpecScene.Scenes should NOT
// produce a scenes.json entry. Validate would fail if the entry had
// empty Path with Required=true (pre-C12 behaviour); the C12 fix
// marks scenes as OPTIONAL, so even an empty entry would be allowed,
// but emitting nothing is cleaner.
func TestBuildAndInjectManifest_NoScenes_OmitsScenes(t *testing.T) {
	h := &GenerateJobHandler{log: zap.NewNop()}
	handlerResult := map[string]any{}
	res := validScriptResult_NoScenes()

	h.buildAndInjectManifest("test-job-c12-no-scenes", res, handlerResult)

	manifest, _ := scriptpkg.Decode(handlerResult)
	for _, a := range manifest.Artifacts {
		if a.Kind == scriptpkg.ArtifactKindScenes {
			t.Errorf("scenes slot present in manifest when SpecScene empty — §8.4 spec says emit only when generated")
		}
	}
}

// TestBuildAndInjectManifest_VoiceoverMultilang_OnePerLanguage
// locks the §8.4 voiceover language-grouped emission. 3 scenes
// across 2 languages (en + it) must produce ONE voiceover entry
// per language, NOT 3 entries (pre-C12 behaviour was per-scene
// which emitted 3 en entries + 1 it entry).
func TestBuildAndInjectManifest_VoiceoverMultilang_OnePerLanguage(t *testing.T) {
	h := &GenerateJobHandler{log: zap.NewNop()}
	handlerResult := map[string]any{}
	res := validScriptResult_VoiceoverMultiLanguage()

	h.buildAndInjectManifest("test-job-c12-multilang", res, handlerResult)

	manifest, _ := scriptpkg.Decode(handlerResult)
	voiceoverLangs := []string{}
	for _, a := range manifest.Artifacts {
		if a.Kind == scriptpkg.ArtifactKindVoiceover {
			// ID format: "<jobID>:voiceover:<lang>"
			voiceoverLangs = append(voiceoverLangs, filepath.Base(a.ID))
		}
	}
	if len(voiceoverLangs) != 2 {
		t.Errorf("voiceover manifest entries = %d, want 2 (one per language: en + it)", len(voiceoverLangs))
	}
}

// TestBuildAndInjectManifest_TypedEnvelopeRouted wraps the handler's
// output and decodes it AS the typed ExecutionResult[GenerationResult]
// envelope (NOT just as a sidecar lookup). The dual-shape discipline
// asserts BOTH Data AND Artifacts are present and typed-correctly
// after marshal-to-map round-trip.
//
// This is the user's literal "emit as part of the ExecutionResult
// envelope rather than embedding file paths inside Items" assertion:
// the typed envelope wraps Data + Artifacts so a handler that
// forgets one half is caught at the Decode step.
func TestBuildAndInjectManifest_TypedEnvelopeRouted(t *testing.T) {
	h := &GenerateJobHandler{log: zap.NewNop()}
	handlerResult := map[string]any{}
	res := validScriptResult("en")

	h.buildAndInjectManifest("test-job-c12-typed-env", res, handlerResult)

	// Round-trip the entire handlerResult through the typed envelope.
	envelopeBytes, mErr := json.Marshal(handlerResult)
	if mErr != nil {
		t.Fatalf("marshal handlerResult: %v", mErr)
	}
	var envelope scriptpkg.ExecutionResult[script.GenerationResult]
	if uErr := json.Unmarshal(envelopeBytes, &envelope); uErr != nil {
		t.Fatalf("unmarshal into typed ExecutionResult[GenerationResult]: %v", uErr)
	}

	// Data half: typed GenerationResult with the canonical fields.
	if envelope.Data.ItemID != res.ItemID {
		t.Errorf("envelope.Data.ItemID = %q, want %q", envelope.Data.ItemID, res.ItemID)
	}
	if envelope.Data.Title != res.Title {
		t.Errorf("envelope.Data.Title = %q, want %q", envelope.Data.Title, res.Title)
	}
	if envelope.Data.Language != res.Language {
		t.Errorf("envelope.Data.Language = %q, want %q (typed envelope fields round-tripped)", envelope.Data.Language, res.Language)
	}

	// Artifacts half: manifest with the §8.4 spec shape (verified
	// by the happy-path test, but re-asserted here).
	if envelope.Artifacts == nil {
		t.Fatal("envelope.Artifacts is nil — typed dual-shape discipline violated")
	}
	if envelope.Artifacts.SchemaVersion != scriptpkg.SchemaVersionArtifactManifestV1 {
		t.Errorf("envelope.Artifacts_schema_version = %q, want %q", envelope.Artifacts.SchemaVersion, scriptpkg.SchemaVersionArtifactManifestV1)
	}
	if envelope.Artifacts.JobID != "test-job-c12-typed-env" {
		t.Errorf("envelope.Artifacts.JobID = %q, want %q", envelope.Artifacts.JobID, "test-job-c12-typed-env")
	}
	// And the sidecar existence — handleResult[__artifact_manifest] must
	// also be set so the runner's job.Decode lookup works in addition
	// to the typed envelope channel.
	if _, hasSidecar := handlerResult[scriptpkg.ManifestKey]; !hasSidecar {
		t.Errorf("handlerResult missing %q sidecar — runner's job.Decode lookup will fail", scriptpkg.ManifestKey)
	}
}

// TestBuildAndInjectManifest_ScriptJSONOnDisk pins the §8.4 spec
// invariant that script.json is materialized on disk (the worker
// process has to read it for the upload cycle to compute SHA-256
// and stream to Drive). Validates that buildAndInjectManifest
// actually writes the file, not just declares it in the manifest.
func TestBuildAndInjectManifest_ScriptJSONOnDisk(t *testing.T) {
	h := &GenerateJobHandler{log: zap.NewNop()}
	handlerResult := map[string]any{}
	res := validScriptResult("en")

	h.buildAndInjectManifest("c12-disk-pin-test", res, handlerResult)

	manifest, _ := scriptpkg.Decode(handlerResult)
	for _, a := range manifest.Artifacts {
		if a.Kind != scriptpkg.ArtifactKindScriptJSON {
			continue
		}
		if _, statErr := os.Stat(a.Path); statErr != nil {
			t.Errorf("script-json path %q not on disk: %v (validator requires required artefact on disk before upload)", a.Path, statErr)
		}
		if a.SizeBytes <= 0 {
			t.Errorf("script-json SizeBytes = %d, want > 0 (handler must populate)", a.SizeBytes)
		}
		if a.SHA256 == "" {
			t.Errorf("script-json SHA256 = \"\", want non-empty")
		}
	}
}
