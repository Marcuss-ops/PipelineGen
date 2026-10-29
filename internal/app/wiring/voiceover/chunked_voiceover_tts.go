package voiceover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"time"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// voiceoverChunkMaxWords is the LATENCY granularity: each provider request
// carries at most one sentence-sized chunk (~1 sentence, 24 words cap so a
// long sentence still splits). This is a 2026-10-29 paradigm change: with the
// former 400-word cap the split stayed inactive for every real scene
// (20–40 words) and synthesis remained a single long streaming request
// (measured p50 8s/call, wall p50 102s/run). Sentence-sized chunks fan out
// concurrently and the critical wait becomes max(sentence) + merge instead
// of sum(whole scene). The hard service request limit (400 words) is far
// above this value, so the original protection is unaffected.
const voiceoverChunkMaxWords = 24

// chunkedTTSProvider keeps each provider request below the speech service's
// request limit AND at sentence-level granularity (see
// voiceoverChunkMaxWords: latency paradigm 2026-10-29), then joins the
// resulting audio in source order. The outer voiceover use case still
// performs silence removal and Drive publication once on the merged track.
// A failed chunk fails OPEN to the whole-scene synthesis (single requesting
// path with the full repair machinery), so the streaming-per-scene behaviour
// always remains available as the safety net.
type chunkedTTSProvider struct {
	inner       voiceover.TTSProvider
	merger      audioChunkMerger
	concurrency int
}

type audioChunkMerger interface {
	MergeInputs(context.Context, []string, string) error
}

func (p *chunkedTTSProvider) Synthesize(ctx context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	chunks := splitVoiceoverWords(in.Text, voiceoverChunkMaxWords)
	if len(chunks) <= 1 {
		return p.inner.Synthesize(ctx, in)
	}
	// Keep the configured limit immutable. This provider is shared by the
	// voiceover service, so mutating p.concurrency to fit one request would
	// introduce a data race and could change the limit for another request.
	effectiveConcurrency := p.concurrency
	if effectiveConcurrency < 1 {
		effectiveConcurrency = 1
	}
	if effectiveConcurrency > len(chunks) {
		effectiveConcurrency = len(chunks)
	}

	type result struct {
		index int
		out   voiceover.TTSOutput
		err   error
	}
	results := make(chan result, len(chunks))
	sem := make(chan struct{}, effectiveConcurrency)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i, text := range chunks {
		wg.Add(1)
		go func(index int, chunk string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-workCtx.Done():
				results <- result{index: index, err: workCtx.Err()}
				return
			}
			chunkInput := in
			chunkInput.Text = chunk
			chunkInput.RemoveSilence = false
			chunkInput.Filename = chunkFilename(in.Filename, index)
			out, err := p.inner.Synthesize(workCtx, chunkInput)
			if err != nil {
				// Stop sibling chunks promptly, while still waiting for all
				// goroutines below so their temporary files can be cleaned up.
				cancel()
			}
			results <- result{index: index, out: out, err: err}
		}(i, text)
	}
	wg.Wait()
	close(results)

	outputs := make([]voiceover.TTSOutput, len(chunks))
	anyChunkFailed := false
	for r := range results {
		if r.err != nil {
			anyChunkFailed = true
			continue
		}
		outputs[r.index] = r.out
	}
	cleanupChunks := func() {
		for _, out := range outputs {
			if out.LocalPath != "" {
				_ = os.Remove(out.LocalPath)
			}
		}
	}
	if anyChunkFailed {
		// Fail-open to the whole-scene path: a single failed sentence must
		// not fail a scene when the original one-request synthesis is
		// available. Chunks and plan state above are discarded; the inner
		// provider owns retries, the loudness gate and empty-audio repair.
		// Successful siblings are temporary chunk artifacts; discard them
		// before retrying the whole scene so a failed fan-out cannot leak files
		// across retries or fill the job's temporary output directory.
		cleanupChunks()
		return p.inner.Synthesize(ctx, in)
	}
	paths := make([]string, len(outputs))
	var duration int64
	for i, out := range outputs {
		if out.LocalPath == "" {
			cleanupChunks()
			return voiceover.TTSOutput{}, fmt.Errorf("voiceover chunk %d returned empty local path", i)
		}
		paths[i] = out.LocalPath
		duration += out.Duration.Nanoseconds()
	}
	defer cleanupChunks()

	merged := filepath.Join(in.OutputDir, in.Filename)
	if err := p.merger.MergeInputs(ctx, paths, merged); err != nil {
		return voiceover.TTSOutput{}, fmt.Errorf("merge voiceover chunks: %w", err)
	}
	hash, err := fileSHA256(merged)
	if err != nil {
		return voiceover.TTSOutput{}, fmt.Errorf("hash merged voiceover: %w", err)
	}
	// The merged track inherits the provider identity + boundary mode from
	// the first chunk. Per-chunk WordBoundaries are merged here with a
	// global offset remap: chunk offsets restart at 0 per synthesis and the
	// merged track is the source-order concatenation (chunks synthesize with
	// RemoveSilence=false, so no silence timeline is involved), so each
	// chunk's boundaries shift by the cumulative duration of the preceding
	// chunks. Dropping them made timing.mode=required fail closed
	// (VOICEOVER_TIMING_UNAVAILABLE) on every multi-chunk scene.
	mergedBoundaries := make([]voiceover.RawSpeechBoundary, 0, len(outputs))
	var offsetUS int64
	for _, out := range outputs {
		for _, b := range out.WordBoundaries {
			mergedBoundaries = append(mergedBoundaries, voiceover.RawSpeechBoundary{
				Text:    b.Text,
				StartUS: b.StartUS + offsetUS,
				EndUS:   b.EndUS + offsetUS,
			})
		}
		offsetUS += out.Duration.Microseconds()
	}

	return voiceover.TTSOutput{
		LocalPath:      merged,
		Voice:          outputs[0].Voice,
		ContentSHA256:  hash,
		Duration:       timeDuration(duration),
		Provider:       outputs[0].Provider,
		BoundaryMode:   outputs[0].BoundaryMode,
		WordBoundaries: mergedBoundaries,
	}, nil
}

func splitVoiceoverWords(text string, maxWords int) []string {
	if maxWords < 1 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	// Build sentence-sized units first. This keeps normal prose boundaries
	// intact; oversized sentences are split by words in the packing step.
	units := make([][]string, 0)
	unit := make([]string, 0)
	for _, word := range words {
		unit = append(unit, word)
		if strings.HasSuffix(word, ".") || strings.HasSuffix(word, "!") || strings.HasSuffix(word, "?") || strings.HasSuffix(word, ":") {
			units = append(units, unit)
			unit = nil
		}
	}
	if len(unit) > 0 {
		units = append(units, unit)
	}

	var chunks []string
	current := make([]string, 0, maxWords)
	for _, wordsUnit := range units {
		for len(wordsUnit) > 0 {
			space := maxWords - len(current)
			if space == 0 {
				chunks = append(chunks, strings.Join(current, " "))
				current = make([]string, 0, maxWords)
				space = maxWords
			}
			n := len(wordsUnit)
			if n > space {
				n = space
			}
			current = append(current, wordsUnit[:n]...)
			wordsUnit = wordsUnit[n:]
			if len(current) == maxWords {
				chunks = append(chunks, strings.Join(current, " "))
				current = make([]string, 0, maxWords)
			}
		}
	}
	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, " "))
	}
	return chunks
}

func chunkFilename(filename string, index int) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filepath.Base(filename), ext)
	return fmt.Sprintf("%s.chunk-%03d%s", base, index+1, ext)
}

func fileSHA256(path string) (string, error) {
	hash, _, err := digest.SHA256File(path)
	return hash, err
}

// timeDuration avoids exposing a mutable accumulator as time.Duration in the
// concurrent synthesis loop.
func timeDuration(nanoseconds int64) time.Duration { return time.Duration(nanoseconds) }
