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
//     and uses the returned []job.Artifact to build the
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
// JSON-shape invariant: §8.4 spec post-Sprint-1.0 lists exactly
// 3 kinds — script-json (REQUIRED), scenes (OPTIONAL when generated),
// voiceover (OPTIONAL, language-grouped). Document artefacts
// (document-pdf, document-markdown) were RETIRED in Sprint 1.0;
// the canonical downstream document.generate job owns Google-Doc
// creation. Pre-§8.4 kinds (script_text, metadata, entities,
// image) are REMOVED here.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
// Returns []job.Artifact (the typed pre-computed artifact
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
	log *zap.Logger,
) ([]job.Artifact, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if result == nil {
		return nil, nil
	}

	outDir := workspaceOutputDir(jobID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifacts_persistence: mkdir %s: %w", outDir, err)
	}

	artifacts := make([]job.Artifact, 0, 3 /* §8.4 best-case ceiling (Sprint 1.0: document retired) */)

	// ── 1. script-json (REQUIRED) ──────────────────────────────────────
	scriptJSONPath := filepath.Join(outDir, "script.json")
	scriptData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("artifacts_persistence: marshal script.json: %w", err)
	}
	if writeErr := os.WriteFile(scriptJSONPath, scriptData, 0o644); writeErr != nil {
		return nil, fmt.Errorf("artifacts_persistence: write script.json: %w", writeErr)
	}
	sha, shaErr := job.ComputeSHA256(scriptJSONPath)
	if shaErr != nil {
		return nil, fmt.Errorf("artifacts_persistence: sha256 script.json: %w", shaErr)
	}
	artifacts = append(artifacts, job.Artifact{
		ID:        jobID + ":script_json",
		Kind:      job.ArtifactKindScriptJSON,
		Path:      scriptJSONPath,
		Filename:  "script.json",
		MIMEType:  "application/json",
		SizeBytes: int64(len(scriptData)),
		SHA256:    sha,
		Required:  true,
	})

	// ── 2. scenes (OPTIONAL when generated) ────────────────────────────
	if len(result.Output.SpecScene.Scenes) > 0 {
		scenesJSONPath := filepath.Join(outDir, "scenes.json")
		scenesData, marErr := json.MarshalIndent(result.Output.SpecScene, "", "  ")
		if marErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: marshal scenes.json: %w", marErr)
		}
		if writeErr := os.WriteFile(scenesJSONPath, scenesData, 0o644); writeErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: write scenes.json: %w", writeErr)
		}
		scenesSHA, scenesSHAErr := job.ComputeSHA256(scenesJSONPath)
		if scenesSHAErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: sha256 scenes.json: %w", scenesSHAErr)
		}
		artifacts = append(artifacts, job.Artifact{
			ID:        jobID + ":scenes",
			Kind:      job.ArtifactKindScenes,
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
	//
	// PR-OUTBOX-SOURCE-VERSION: compute SHA256 + SizeBytes for
	// voiceover artifacts. Without this, FinalizeAsset emits
	// outbox events with empty source_version, causing dead_letter
	// via the IndexingHandler supersede gate. Skip files that
	// don't exist on disk (the voiceover pipeline may not have
	// generated them yet).
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
		// Only include voiceover artifact if the file exists and
		// we can compute its SHA256. Skip missing files gracefully.
		if voInfo, voStatErr := os.Stat(voPath); voStatErr == nil {
			voSHA, voSHAErr := job.ComputeSHA256(voPath)
			if voSHAErr != nil {
				return nil, fmt.Errorf("artifacts_persistence: sha256 voiceover %s: %w", voPath, voSHAErr)
			}
			artifacts = append(artifacts, job.Artifact{
				ID:        fmt.Sprintf("%s:voiceover:%s", jobID, lang),
				Kind:      job.ArtifactKindVoiceover,
				Path:      voPath,
				Filename:  voFilename,
				MIMEType:  "audio/mpeg",
				SizeBytes: voInfo.Size(),
				SHA256:    voSHA,
				Required:  false,
			})
		} else {
			log.Debug("artifacts_persistence: voiceover file not on disk — skipping artifact",
				zap.String("job_id", jobID),
				zap.String("lang", lang),
				zap.String("vo_path", voPath),
				zap.Error(voStatErr))
		}
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
