package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

func readInput(path string) ([]byte, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, fmt.Errorf("input exceeds %d bytes", maxInputBytes)
	}
	return data, nil
}

func splitIntoScenes(text string, maxWords int) []string {
	// Keep sentence boundaries where possible so the worker sees coherent
	// events. Oversized individual sentences are split at whitespace below.
	sentences := splitSentences(text)
	var scenes []string
	var current strings.Builder
	currentWords := 0
	flush := func() {
		if value := strings.TrimSpace(current.String()); value != "" {
			scenes = append(scenes, value)
		}
		current.Reset()
		currentWords = 0
	}
	for _, sentence := range sentences {
		words := strings.Fields(sentence)
		if len(words) == 0 {
			continue
		}
		if len(words) > maxWords {
			flush()
			for start := 0; start < len(words); start += maxWords {
				end := start + maxWords
				if end > len(words) {
					end = len(words)
				}
				scenes = append(scenes, strings.Join(words[start:end], " "))
			}
			continue
		}
		if currentWords > 0 && currentWords+len(words) > maxWords {
			flush()
		}
		if currentWords > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(strings.TrimSpace(sentence))
		currentWords += len(words)
	}
	flush()
	return scenes
}

func splitSentences(text string) []string {
	var out []string
	start := 0
	runes := []rune(text)
	byteOffsets := make([]int, len(runes)+1)
	for i := range runes {
		byteOffsets[i+1] = byteOffsets[i] + len(string(runes[i]))
	}
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' && r != '\n' {
			continue
		}
		if r != '\n' && i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue
		}
		end := byteOffsets[i+1]
		if piece := strings.TrimSpace(text[start:end]); piece != "" {
			out = append(out, piece)
		}
		start = end
	}
	if piece := strings.TrimSpace(text[start:]); piece != "" {
		out = append(out, piece)
	}
	return out
}

func defaultEnginePath() string {
	if value := strings.TrimSpace(os.Getenv("VELOX_RUST_SEMANTIC_ENGINE_PATH")); value != "" {
		return value
	}
	return "bin/semantic-engine"
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "semantic-extract: "+format+"\n", args...)
	os.Exit(2)
}
