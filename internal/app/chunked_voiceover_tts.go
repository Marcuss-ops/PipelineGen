package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

const voiceoverChunkMaxWords = 400

// chunkedTTSProvider keeps each provider request below the speech service's
// request limit, then joins the resulting audio in source order. The outer
// voiceover use case still performs silence removal and Drive publication once
// on the merged track.
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
	if p.concurrency < 1 {
		p.concurrency = 1
	}
	if p.concurrency > len(chunks) {
		p.concurrency = len(chunks)
	}

	type result struct {
		index int
		out   voiceover.TTSOutput
		err   error
	}
	results := make(chan result, len(chunks))
	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup
	for i, text := range chunks {
		wg.Add(1)
		go func(index int, chunk string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- result{index: index, err: ctx.Err()}
				return
			}
			chunkInput := in
			chunkInput.Text = chunk
			chunkInput.RemoveSilence = false
			chunkInput.Filename = chunkFilename(in.Filename, index)
			out, err := p.inner.Synthesize(ctx, chunkInput)
			results <- result{index: index, out: out, err: err}
		}(i, text)
	}
	wg.Wait()
	close(results)

	outputs := make([]voiceover.TTSOutput, len(chunks))
	for r := range results {
		if r.err != nil {
			return voiceover.TTSOutput{}, fmt.Errorf("voiceover chunk %d: %w", r.index, r.err)
		}
		outputs[r.index] = r.out
	}
	paths := make([]string, len(outputs))
	var duration int64
	for i, out := range outputs {
		if out.LocalPath == "" {
			return voiceover.TTSOutput{}, fmt.Errorf("voiceover chunk %d returned empty local path", i)
		}
		paths[i] = out.LocalPath
		duration += out.Duration.Nanoseconds()
	}
	defer func() {
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}()

	merged := filepath.Join(in.OutputDir, in.Filename)
	if err := p.merger.MergeInputs(ctx, paths, merged); err != nil {
		return voiceover.TTSOutput{}, fmt.Errorf("merge voiceover chunks: %w", err)
	}
	hash, err := fileSHA256(merged)
	if err != nil {
		return voiceover.TTSOutput{}, fmt.Errorf("hash merged voiceover: %w", err)
	}
	// The merged track inherits the provider identity + boundary mode from
	// the first chunk. Per-chunk WordBoundaries are NOT merged here: chunk
	// offsets restart per synthesis, so merging requires a global offset
	// remap — deferred to the timing-artifact step (same text can still be
	// synthesized in one pass by providers that do not enforce a chunk
	// ceiling).
	return voiceover.TTSOutput{
		LocalPath:    merged,
		Voice:        outputs[0].Voice,
		FileHash:     hash,
		Duration:     timeDuration(duration),
		Provider:     outputs[0].Provider,
		BoundaryMode: outputs[0].BoundaryMode,
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
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// timeDuration avoids exposing a mutable accumulator as time.Duration in the
// concurrent synthesis loop.
func timeDuration(nanoseconds int64) time.Duration { return time.Duration(nanoseconds) }
