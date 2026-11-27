package clips

import (
	"context"
	"sync"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// ClipAudioLatch is the one-producer/two-consumer join for physical clip
// audio. The master audio phase produces the immutable snapshot; the overlay
// plan branch consumes it and applies the same facts to its detached document.
// No second resolution or probe is issued, and the timing seen by the two
// branches is identical by construction.
//
// The producer is the phase goroutine, called immediately after the branch is
// launched. If a caller other than the producer ever consumed first, it would
// block on ready until the producer published (or failed).
type ClipAudioLatch struct {
	once     sync.Once
	ready    chan struct{}
	snapshot *ClipAudioSnapshot
	ms       int64
	err      error
}

func NewClipAudioLatch() *ClipAudioLatch {
	return &ClipAudioLatch{ready: make(chan struct{})}
}

// produce resolves the clip audio once and projects the facts onto result.
func (l *ClipAudioLatch) Produce(ctx context.Context, result *contract.GenerateResult, source contract.ClipAudioAssetSource, policy capabilityaudio.AudioMixPolicy) (int64, error) {
	l.once.Do(func() {
		defer close(l.ready)
		l.snapshot, l.ms, l.err = PrepareClipAudioSnapshot(ctx, result, source, policy)
	})
	if l.err != nil {
		return l.ms, l.err
	}
	if err := l.snapshot.ApplyTo(result); err != nil {
		return 0, err
	}
	return l.ms, nil
}

// applyTo waits for the producer and projects the cached facts onto result.
// It performs no materialization.
func (l *ClipAudioLatch) ApplyTo(result *contract.GenerateResult) error {
	<-l.ready
	if l.err != nil {
		return l.err
	}
	return l.snapshot.ApplyTo(result)
}
