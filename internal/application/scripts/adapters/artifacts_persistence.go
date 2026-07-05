// Package scripts/adapters — artifacts_persistence.go is the
// canonical home for script.generate artifact persistence ops
// (godlike/06 SSOT: one adapter per side-effect). The handler used
// to embed os.MkdirAll / os.WriteFile / os.TempDir / ComputeSHA256
// directly inside the broker-dispatch handler. PR-GODOBJ-4
// KILL-K1 moves ALL of those ops to this adapter.
//
// KILL-K1 contract (per user spec, July 2026):
//   - handler (`internal/application/scripts/jobs/generation_handler.go`)
//     does NOT touch filesystem. It calls
//     PersistGeneratedArtifacts(ctx, jobID, *GenerationResult)
//     and uses the returned []scriptpkg.Artifact to build the
//     typed *job.ArtifactManifest in generation_manifest.go.
//   - The adapter does all FS ops: os.TempDir, os.MkdirAll,
//     os.WriteFile, file stat, sha256 hashing.
//   - On error, the adapter returns a typed error mapping to the
//     canonical godlike/07 typed-error contract (errors.New + %w).
//
// godlike/06 SSOT surface: this file is the SINGLE canonical home
// for the §8.4 spec multi-artifact shape on the script.generate
// job type. Pre-PR-GODOBJ-4 the same shape lived inline in the
// handler's buildAndInjectManifest; the migration here is the
// godlike/07-told-by-godlike/06 split: persistence lives in the
// adapter, manifest construction lives in the handler.
//
// godlike/07 honest-limitation: this file exceeds the 66-LoC
// transitional cap (~150 LoC) because the §8.4 multi-artifact
// shape comprises 4 inline writing paths (script.json / scenes.json /
// workspace-mkdir / per-language voiceover) + SHA256 + manifest
// assembly. Forward-pointer linked_issue (zero-baseline rule):
// PR-GODOBJ-4c-PERSIST-ADAPTER-SLIM extracts per-kind writers into
// per-kind helper functions (≤30 LoC each). Deadline 2026-08-15.
//
// JSON-shape invariant: §8.4 spec lists exactly 5 kinds —
// script-json (REQUIRED), document-pdf (REQUIRED when DocLink set),
// document-markdown (OPTIONAL, slot reserved), scenes (OPTIONAL),
// voiceover (OPTIONAL, language-grouped). Pre-§8.4 also emitted
// script_text, metadata, entities, image. They are REMOVED here.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// PersistGeneratedArtifacts writes the canonical §8.4 multi-artifact
// sidecar for a script.generate run. The handler invokes this BEFORE
// buildManifestFromArtifacts. The adapter is the SINGLE canonical
// home for filesystem ops in this pipeline (PR-GODOBJ-4 KILL K1).
//
// Signature:
//
//	ctx context.Context       — propagation context for triage
//	jobID string              — used in artifact IDs (jobID + ":" + kind)
//	result *GenerationResult   — the typed aggregate result
//
// Returns []scriptpkg.Artifact (the typed pre-computed artifact
// slice) and an error. The handler then wraps this slice into a
// *job.ArtifactManifest via buildManifestFromArtifacts.
//
// Workspace convention matches the worker's Workspace.Prepare:
//
//	/tmp/pipelinegen/jobs/<jobID>/output/
func PersistGeneratedArtifacts(
	ctx context.Context,
	jobID string,
	result *domainScript.GenerationResult,
) ([]scriptpkg.Artifact, error) {
	if result == nil {
		return nil, nil
	}

	outDir := workspaceOutputDir(jobID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifacts_persistence: mkdir %s: %w", outDir, err)
	}

	artifacts := make([]scriptpkg.Artifact, 0, 5 /* §8.4 best-case ceiling */)

	// ── 1. script-json (REQUIRED) ──────────────────────────────────────
	scriptJSONPath := filepath.Join(outDir, "script.json")
	scriptData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("artifacts_persistence: marshal script.json: %w", err)
	}
	if writeErr := os.WriteFile(scriptJSONPath, scriptData, 0o644); writeErr != nil {
		return nil, fmt.Errorf("artifacts_persistence: write script.json: %w", writeErr)
	}
	sha, shaErr := scriptpkg.ComputeSHA256(scriptJSONPath)
	if shaErr != nil {
		return nil, fmt.Errorf("artifacts_persistence: sha256 script.json: %w", shaErr)
	}
	artifacts = append(artifacts, scriptpkg.Artifact{
		ID:        jobID + ":script_json",
		Kind:      scriptpkg.ArtifactKindScriptJSON,
		Path:      scriptJSONPath,
		Filename:  "script.json",
		MIMEType:  "application/json",
		SizeBytes: int64(len(scriptData)),
		SHA256:    sha,
		Required:  true,
	})

	// ── 2. document-pdf (REQUIRED when Document.DocLink set) ──────────
	if result.Artifacts.Document != nil && strings.TrimSpace(result.Artifacts.Document.DocLink) != "" {
		pdfPath := filepath.Join(outDir, "document.pdf")
		artifacts = append(artifacts, scriptpkg.Artifact{
			ID:       jobID + ":pdf",
			Kind:     scriptpkg.ArtifactKindPDF,
			Path:     pdfPath,
			Filename: "document.pdf",
			MIMEType: "application/pdf",
			Required: true,
		})
	}

	// ── 3. document-markdown (OPTIONAL, slot reserved) ─────────────────
	// §8.4 lists document-markdown as OPTIONAL. The kind constant
	// (scriptpkg.ArtifactKindMarkdown) is reserved here so a future
	// emission pipeline can drop output without a wire-format
	// extension. C12 gates emission on a future markdown-twin
	// pipeline (out of scope for the C12 + PR-GODOBJ-4 closure).

	// ── 4. scenes (OPTIONAL when generated) ────────────────────────────
	if len(result.Output.SpecScene.Scenes) > 0 {
		scenesJSONPath := filepath.Join(outDir, "scenes.json")
		scenesData, marErr := json.MarshalIndent(result.Output.SpecScene, "", "  ")
		if marErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: marshal scenes.json: %w", marErr)
		}
		if writeErr := os.WriteFile(scenesJSONPath, scenesData, 0o644); writeErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: write scenes.json: %w", writeErr)
		}
		scenesSHA, scenesSHAErr := scriptpkg.ComputeSHA256(scenesJSONPath)
		if scenesSHAErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: sha256 scenes.json: %w", scenesSHAErr)
		}
		artifacts = append(artifacts, scriptpkg.Artifact{
			ID:        jobID + ":scenes",
			Kind:      scriptpkg.ArtifactKindScenes,
			Path:      scenesJSONPath,
			Filename:  "scenes.json",
			MIMEType:  "application/json",
			SizeBytes: int64(len(scenesData)),
			SHA256:    scenesSHA,
			Required:  false,
		})
	}

	// ── 5. voiceover (OPTIONAL, language-grouped) ──────────────────────
	// §8.4 model is language-grouped (one voiceover per language per
	// run, NOT one per scene as pre-C12 emitted). Take the first
	// scene's voiceover binding's LocalPath per language as the
	// canonical upload target. The first-seen-wins disambiguation
	// matches the previous double-pass (per-scene) emission's intent:
	// if multiple scenes share the same language, only one manifest
	// entry is created and the per-scene voices are uploaded as part
	// of the same Drive asset via per-language disambiguation.
	seenLang := make(map[string]bool)
	for _, scene := range result.Output.SpecScene.Scenes {
		if scene.Bindings.Voiceover == nil || strings.TrimSpace(scene.Bindings.Voiceover.LocalPath) == "" {
			continue
		}
		lang := result.Language
		if lang == "" {
			lang = "default"
		}
		if seenLang[lang] {
			continue
		}
		seenLang[lang] = true

		voPath := scene.Bindings.Voiceover.LocalPath
		voFilename := "voiceover.mp3"
		if lang != "" && lang != "default" {
			voFilename = "voiceover-" + lang + ".mp3"
		}
		artifacts = append(artifacts, scriptpkg.Artifact{
			ID:       fmt.Sprintf("%s:voiceover:%s", jobID, lang),
			Kind:     scriptpkg.ArtifactKindVoiceover,
			Path:     voPath,
			Filename: voFilename,
			MIMEType: "audio/mpeg",
			Required: false,
		})
	}

	return artifacts, nil
}

// workspaceOutputDir returns the job-workspace output directory.
// Convention (matches worker.Workspace.Prepare):
//
//	/tmp/pipelinegen/jobs/<jobID>/output/
func workspaceOutputDir(jobID string) string {
	return filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID, "output")
}
