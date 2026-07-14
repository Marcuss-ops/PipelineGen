// Package stockpipeline — orchestrator_manifest.go (split July 2026).
//
// This file owns the C12 5-artifact manifest builder. Extracted from
// orchestrator.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: buildStockManifest is the single canonical owner of
// the C12 5-artifact envelope shape.
package stockpipeline

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// buildStockManifest returns the C12 5-artifact envelope for stock.
//
// Why a hard-coded 5? The user spec for Stock Cutover Commit 2 says:
//
//	"the JobStatusResponse exposes __artifact_manifest with the C12
//	 5-artifact shape"
//
// The 5 fixed entries are the per-kind envelope the downstream
// runner (internal/application/jobs/worker/runner.go::uploadManifest)
// routes on:
//
//	(a) metadata   — pipeline metadata.json uploaded at the end
//	(b) thumbnail  — cover png for the run (rendered once per run)
//	(c) bindings   — source-clip bindings report (one per run)
//	(d) report     — runtime summary JSON (one per run)
//	(e) summary    — narrative text summary (one per run)
//
// All entries have Required:false today because Commit 2 cannot
// populate their on-disk Paths (chunk rendering, Drive upload,
// and the binder run all land in Commit 4-7). Required is flipped
// to true in Commit 4-7 once the entry has a real local path —
// Validate() requires Required:true ⇒ non-empty Path; setting
// Required:false today passes Validate() cleanly.
//
// Validate() invariants upheld:
//   - SchemaVersion non-empty (pipelinegen.artifacts.v1)
//   - len(Artifacts) > 0
//   - no Required⇒empty Path
//   - no non-empty Path⇒empty Filename (Commit 4-7 hydrates both)
//
// (NIT-1 — kind overloading rationale): ArtifactKindScriptJSON +
// ArtifactKindScriptText are repurposed for stock here because the
// C12 envelope (domain/job/artifact_manifest.go) does not yet
// declare a "stock_run_report" or "stock_narrative" kind. The
// underlying wire-string is still valid JSON / still valid text —
// downstream consumers dispatch by Kind string only when a
// sender-side router maps a Kind to a transport (the stock
// pipeline does NOT route ScriptJSON-named entries to the scripts
// gateway; the sender-side routing is bidirectional via filename
// + manifest per-kind ID convention, not kind value alone). A
// follow-up PR may introduce ArtifactKindRunReport +
// ArtifactKindStockSummary; until then, the kind labels carry a
// stock-pipeline semantic load that the operator dashboards must
// understand via the manifest's stable IDs (stock:report /
// stock:summary) rather than the kind value. This rationale is
// mirrored in the CHANGELOG entry for Commit 2.
func buildStockManifest(workflowID, jobID string) *job.ArtifactManifest {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    workflowID,
		JobID:         jobID,
		Artifacts: []job.Artifact{
			{
				ID:       StockArtifactIdMetadata,
				Kind:     job.ArtifactKindMetadata,
				Filename: "metadata.json",
				MIMEType: "application/json",
				Required: false, // Commit 4-7 flips to true once Path is hydrated
			},
			{
				ID:       StockArtifactIdThumbnail,
				Kind:     job.ArtifactKindImage,
				Filename: "thumbnail.png",
				MIMEType: "image/png",
				Required: false,
			},
			{
				ID:       StockArtifactIdBindings,
				Kind:     job.ArtifactKindClipBindings,
				Filename: "bindings.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdReport,
				Kind:     job.ArtifactKindScriptJSON,
				Filename: "report.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdSummary,
				Kind:     job.ArtifactKindScriptText,
				Filename: "summary.txt",
				MIMEType: "text/plain",
				Required: false,
			},
		},
	}
	// Compile-time invariant pin: the C12 5-artifact shape must
	// stay arity-5 unless a follow-up explicitly changes the
	// shape (and bumps these constants). Future maintainers who
	// want a different arity must update stockArtifactCount AND
	// the constant list above AND the CHANGELOG entry referencing
	// this commit.
	if len(manifest.Artifacts) != stockArtifactCount {
		panic("buildStockManifest: artifact arity drifted from canonical 5 (Stock Cutover Commit 2 invariant violated)")
	}
	return manifest
}
