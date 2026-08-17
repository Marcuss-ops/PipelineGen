// Package scriptgeneration — routing_context.go maps a GenerateRequest onto
// the canonical artifact routing context resolved ONCE at generation start.
//
// Project, Language, and folder routing are routing facts decided at the
// input boundary; no downstream phase may re-derive them. The runner resolves
// this context a single time and propagates it verbatim to the voiceover and
// document phases (godlike/06 SSOT — one owner per fact).
package scriptgeneration

import (
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// resolveArtifactRoutingContext resolves the canonical artifact routing
// context from the generation input. It is the single derivation point for
// Project / Language / folder routing; downstream phases consume the resolved
// value and never read req.Project or invent a namespace.
//
// The script documents destination is resolved through the single canonical
// resolver: explicit docs.folder_id > configured default (the runner's
// PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID) > fail closed when docs.enabled=true.
// A docs-enabled request with no resolvable folder returns an error so the
// runner fails the run BEFORE any Google Docs write.
func (req GenerateRequest) resolveArtifactRoutingContext(defaultDocsFolderID string) (kernelscript.ArtifactRoutingContext, error) {
	callerFolderID := req.Docs.FolderID
	if callerFolderID == "" {
		callerFolderID = req.DriveFolderID
	}
	enabled, _, _ := req.ResolveDocsConfig()
	docsFolderID, err := kernelscript.ResolveScriptDocsFolderID(enabled, callerFolderID, defaultDocsFolderID)
	if err != nil {
		return kernelscript.ArtifactRoutingContext{}, err
	}
	return kernelscript.ResolveArtifactRoutingContext(req.Project, string(req.SourceLanguage), req.VoiceoverFolderID, docsFolderID), nil
}
