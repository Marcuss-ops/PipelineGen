package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"

	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestResolveArtifactRoutingContext_MapsCanonicalFacts(t *testing.T) {
	req := GenerateRequest{
		Project:        "  my-project  ",
		SourceLanguage: "en",
		Docs:           DocumentsConfig{FolderID: "docs-folder"},
		DriveFolderID:  "legacy-drive-folder",
	}
	routing, err := req.resolveArtifactRoutingContext("")
	require.NoError(t, err)
	require.Equal(t, "my-project", routing.Project)
	require.Equal(t, "en", routing.Language)
	require.Equal(t, "", routing.VoiceoverFolderID)
	// Docs.FolderID is canonical and wins over the legacy DriveFolderID.
	require.Equal(t, "docs-folder", routing.DocsFolderID)
}

func TestResolveArtifactRoutingContext_ForwardsVoiceoverFolder(t *testing.T) {
	req := GenerateRequest{
		Project:           "p",
		SourceLanguage:    "it",
		VoiceoverFolderID: "explicit-vo-folder",
		Docs:              DocumentsConfig{FolderID: "docs-folder"},
	}
	routing, err := req.resolveArtifactRoutingContext("")
	require.NoError(t, err)
	require.Equal(t, "explicit-vo-folder", routing.VoiceoverFolderID,
		"caller-explicit voiceover folder must survive the routing context")
}

func TestResolveArtifactRoutingContext_DriveFolderFallback(t *testing.T) {
	req := GenerateRequest{
		Project:        "p",
		SourceLanguage: "es",
		DriveFolderID:  "legacy-drive-folder",
	}
	routing, err := req.resolveArtifactRoutingContext("")
	require.NoError(t, err)
	require.Equal(t, "legacy-drive-folder", routing.DocsFolderID)
}

func TestResolveArtifactRoutingContext_ConfiguredDefault(t *testing.T) {
	req := GenerateRequest{
		Project:        "p",
		SourceLanguage: "it",
		Docs:           DocumentsConfig{Enabled: true, Languages: []Language{"it"}},
	}
	routing, err := req.resolveArtifactRoutingContext("PIPELINEGEN_DEFAULT_FOLDER")
	require.NoError(t, err)
	require.Equal(t, "PIPELINEGEN_DEFAULT_FOLDER", routing.DocsFolderID)
}

func TestResolveArtifactRoutingContext_FailsClosedWithoutFolder(t *testing.T) {
	req := GenerateRequest{
		Project:        "p",
		SourceLanguage: "it",
		Docs:           DocumentsConfig{Enabled: true, Languages: []Language{"it"}},
	}
	_, err := req.resolveArtifactRoutingContext("")
	require.ErrorIs(t, err, kernelscript.ErrScriptDocsFolderRequired)
}

func TestKernelResolveArtifactRoutingContext_Trims(t *testing.T) {
	routing := kernelscript.ResolveArtifactRoutingContext("  p  ", " en ", " vo ", " docs ")
	require.Equal(t, "p", routing.Project)
	require.Equal(t, "en", routing.Language)
	require.Equal(t, "vo", routing.VoiceoverFolderID)
	require.Equal(t, "docs", routing.DocsFolderID)
}
