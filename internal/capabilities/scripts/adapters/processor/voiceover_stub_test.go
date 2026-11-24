package processor

import (
	"context"
	"sync/atomic"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
)

// stubItemExecutor is the canonical VoiceoverItemExecutor test double used by
// the root-package translation/voiceover integration tests. The voiceover
// processor tests now live in the processor/voiceover subpackage and carry
// their own copy.
type stubItemExecutor struct {
	fn    func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error)
	calls atomic.Int32
}

func (s *stubItemExecutor) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	s.calls.Add(1)
	if item == nil {
		return &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "nil GenerateVoiceoverItemCommand",
		}, nil
	}
	if s.fn != nil {
		return s.fn(item.Text, string(item.Language), item.Filename)
	}
	return &voiceover.VoiceoverItemResult{
		Status:    voiceover.StatusCompleted,
		Language:  item.Language,
		Filename:  item.Filename,
		LocalPath: "/tmp/" + item.Filename,
		DriveLink: "https://drive.example.test/" + item.Filename,
	}, nil
}

var _ voiceover.VoiceoverItemExecutor = (*stubItemExecutor)(nil)
