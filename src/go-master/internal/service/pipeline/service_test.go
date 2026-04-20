package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Tests: NewVideoCreationService
// ---------------------------------------------------------------------------

func TestNewVideoCreationService(t *testing.T) {
	mockGen := &MockScriptGenerator{}
	mockEntity := &MockEntityService{}
	mockTTS := &MockTTSGenerator{}
	mockVideo := &MockVideoProcessor{}

	svc := NewVideoCreationService(mockGen, mockEntity, mockTTS, mockVideo)

	require.NotNil(t, svc)
	assert.Equal(t, mockGen, svc.scriptGen)
	assert.Equal(t, mockEntity, svc.entityService)
	assert.Equal(t, mockTTS, svc.ttsGenerator)
	assert.Equal(t, mockVideo, svc.videoProc)
}
