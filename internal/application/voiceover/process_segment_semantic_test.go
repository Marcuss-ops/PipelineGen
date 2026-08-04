package voiceover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestProcessSegmentUseCase_SemanticTaggingIsPersistedOnce(t *testing.T) {
	db := openProcessTestDB(t)
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-semantic"}}
	calls := 0

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/semantic.mp3", Voice: "en-US-Test"}},
		Publisher:           &stubProcessPublisher{fileID: "drive-semantic"},
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		SemanticTagger: func(_ context.Context, prompt, style, mediaType, generator string) (*SemanticTaggerResult, error) {
			calls++
			require.Equal(t, "A narrated boxing story", prompt)
			require.Equal(t, "", style)
			require.Equal(t, "voiceover", mediaType)
			require.Equal(t, "voiceover", generator)
			return &SemanticTaggerResult{
				SearchText: "narrated boxing story",
				Tags:       []string{"boxing", "narration"},
				Subjects:   []string{"sport"},
				Mood:       []string{"dramatic"},
			}, nil
		},
		Logger: zap.NewNop(),
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-semantic",
		Text:     "A narrated boxing story",
		Language: "en",
		Filename: "semantic.mp3",
		Dest:     &ResolvedDestination{FolderID: "folder-semantic", FolderPath: "/tmp"},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, out.Status)
	require.Equal(t, 1, calls, "the shared pipeline must invoke semantic tagging exactly once")
	require.Equal(t, "narrated boxing story", out.SearchText)
	require.Len(t, finalizer.calls, 1)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(finalizer.calls[0].MetaJSON, &metadata))
	require.Equal(t, "narrated boxing story", metadata["search_text"])
	require.Equal(t, []any{"boxing", "narration"}, metadata["semantic_tags"])
	require.Equal(t, []any{"sport"}, metadata["semantic_subjects"])
	require.Equal(t, []any{"dramatic"}, metadata["semantic_mood"])
}

func TestProcessSegmentUseCase_SemanticTaggingFailureDoesNotFakeFailure(t *testing.T) {
	db := openProcessTestDB(t)
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-semantic-degraded"}}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/semantic-degraded.mp3"}},
		Publisher:           &stubProcessPublisher{fileID: "drive-semantic-degraded"},
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		SemanticTagger: func(context.Context, string, string, string, string) (*SemanticTaggerResult, error) {
			return nil, errors.New("semantic service unavailable")
		},
		Logger: zap.NewNop(),
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-semantic-degraded",
		Text:     "A story",
		Language: "en",
		Filename: "semantic-degraded.mp3",
		Dest:     &ResolvedDestination{FolderID: "folder-semantic", FolderPath: "/tmp"},
	})
	require.NoError(t, err, "semantic enrichment remains best-effort as in the legacy batch path")
	require.Equal(t, StatusCompleted, out.Status)
	require.Empty(t, out.SearchText)
	require.Len(t, finalizer.calls, 1, "audio must still reach finalization when tagging fails")

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(finalizer.calls[0].MetaJSON, &metadata))
	_, present := metadata["semantic_tags"]
	require.False(t, present)
}

func TestBuildVoiceoverCommitRequest_PreservesSemanticMetadata(t *testing.T) {
	cmd := &FinalizeCommand{
		ID:        "vo-semantic-projection",
		Language:  "en",
		RequestID: "req-semantic-projection",
		FileHash:  "hash-semantic-projection",
		MetaJSON:  []byte(`{"search_text":"semantic search","semantic_tags":["boxing"],"semantic_subjects":["sport"],"semantic_mood":["dramatic"]}`),
	}

	req := buildVoiceoverCommitRequest(cmd, "plain preview")
	require.Equal(t, "semantic search", req.SearchText)
	require.Equal(t, []string{"boxing"}, req.Metadata.Tags)
	require.Equal(t, []string{"sport"}, req.Metadata.Extra["semantic_subjects"])
	require.Equal(t, []string{"dramatic"}, req.Metadata.Extra["semantic_mood"])
}

func TestProcessSegmentUseCase_SemanticTaggingIsOptional(t *testing.T) {
	db := openProcessTestDB(t)
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-no-semantic"}}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/no-semantic.mp3"}},
		Publisher:           &stubProcessPublisher{fileID: "drive-no-semantic"},
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-no-semantic",
		Text:     "A story",
		Language: "en",
		Filename: "no-semantic.mp3",
		Dest:     &ResolvedDestination{FolderID: "folder-semantic", FolderPath: "/tmp"},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, out.Status)
	require.Empty(t, out.SearchText)
	require.Len(t, finalizer.calls, 1)
}
