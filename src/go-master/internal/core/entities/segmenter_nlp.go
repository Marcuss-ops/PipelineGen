// Package entities provides script segmentation implementation.
package entities

import (
	"strings"
)

// NLPSegmenter implements entities.Segmenter
type NLPSegmenter struct{}

// NewNLPSegmenter creates a new NLP-based segmenter
func NewNLPSegmenter() *NLPSegmenter {
	return &NLPSegmenter{}
}

// Split divides text into segments based on configuration
func (s *NLPSegmenter) Split(text string, config SegmentConfig) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{}
	}

	// Apply defaults
	if config.TargetWordsPerSegment <= 0 {
		config.TargetWordsPerSegment = 800
	}
	if config.MinSegments <= 0 {
		config.MinSegments = 1
	}

	// Calculate number of segments
	numSegments := len(words) / config.TargetWordsPerSegment
	if numSegments < config.MinSegments {
		numSegments = config.MinSegments
	}

	// Apply max segments limit
	if config.MaxSegments > 0 && numSegments > config.MaxSegments {
		numSegments = config.MaxSegments
	}

	// Handle edge case: script shorter than one segment
	if numSegments == 0 {
		numSegments = 1
	}

	// Words per segment
	wordsPerSegment := len(words) / numSegments
	if wordsPerSegment == 0 {
		wordsPerSegment = 1
	}

	var segments []string
	for i := 0; i < numSegments; i++ {
		start := i * wordsPerSegment
		end := start + wordsPerSegment

		// Last segment takes all remaining words
		if i == numSegments-1 {
			end = len(words)
		}

		// Add overlap if configured
		if config.OverlapWords > 0 && i > 0 {
			overlapStart := start - config.OverlapWords
			if overlapStart < 0 {
				overlapStart = 0
			}
			start = overlapStart
		}

		if start >= end {
			start = 0
		}

		segmentWords := words[start:end]
		segments = append(segments, strings.Join(segmentWords, " "))
	}

	return segments
}

// CountWords returns the word count of text
func (s *NLPSegmenter) CountWords(text string) int {
	return len(strings.Fields(text))
}

// EstimateSegments estimates how many segments will be produced
func (s *NLPSegmenter) EstimateSegments(text string, wordsPerSegment int) int {
	wordCount := s.CountWords(text)
	if wordCount == 0 {
		return 0
	}
	if wordsPerSegment <= 0 {
		wordsPerSegment = 800
	}
	count := wordCount / wordsPerSegment
	if count == 0 {
		count = 1
	}
	return count
}
