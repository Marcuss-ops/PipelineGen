package contract

import (
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// resolveArtifactRoutingContext resolves the canonical artifact routing
// context from the generation input. It is the single derivation point for
// Project / Language / folder routing; downstream phases consume the resolved
// value and never read req.Project or invent a namespace.
//
// Google Docs was retired: the routing context carries only the project,
// source language and the caller-explicit voiceover folder. There is no
// documents destination to resolve any more.
func (req GenerateRequest) ResolveArtifactRoutingContext() (kernelscript.ArtifactRoutingContext, error) {
	return kernelscript.ResolveArtifactRoutingContext(req.Project, string(req.SourceLanguage), req.VoiceoverFolderID), nil
}
