package adapters

import (
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestResolveDocumentVoiceoverLink_UsesRequestedLanguage(t *testing.T) {
	vo := &scriptpkg.VoiceoverBinding{
		Links: map[string]string{
			"it": "IT-LINK",
			"en": "EN-LINK",
		},
	}
	require.Equal(t, "IT-LINK", resolveDocumentVoiceoverLink(vo, "it", ""))
}

func TestResolveDocumentVoiceoverLink_English(t *testing.T) {
	vo := &scriptpkg.VoiceoverBinding{
		Links: map[string]string{
			"it": "IT-LINK",
			"en": "EN-LINK",
		},
	}
	require.Equal(t, "EN-LINK", resolveDocumentVoiceoverLink(vo, "en", ""))
}

func TestResolveDocumentVoiceoverLink_DefaultLanguageFallsBackToLink(t *testing.T) {
	vo := &scriptpkg.VoiceoverBinding{
		Link: "LEGACY-LINK",
	}
	require.Equal(t, "LEGACY-LINK", resolveDocumentVoiceoverLink(vo, "it", "it"))
}

func TestResolveDocumentVoiceoverLink_DoesNotUseWrongLanguage(t *testing.T) {
	vo := &scriptpkg.VoiceoverBinding{
		Links: map[string]string{
			"it": "IT-LINK",
		},
	}
	require.Equal(t, "", resolveDocumentVoiceoverLink(vo, "en", "it"))
}

func TestResolveDocumentVoiceoverLink_NilBinding(t *testing.T) {
	require.Equal(t, "", resolveDocumentVoiceoverLink(nil, "it", "it"))
}

func TestResolveDocumentVoiceoverLink_EmptyBinding(t *testing.T) {
	require.Equal(t, "", resolveDocumentVoiceoverLink(&scriptpkg.VoiceoverBinding{}, "it", "it"))
}
