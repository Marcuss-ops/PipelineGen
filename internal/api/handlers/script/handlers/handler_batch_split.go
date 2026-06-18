package handlers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

const (
	batchSourceSplitMinWords = 2500
	batchSourceSplitMaxWords = 4000
)

type batchWorkItem struct {
	topic              string
	sourceText         string
	sourceOrigin       string
	sourceSplitParent  string
	sourceSplitIndex   int
	sourceSplitTotal   int
	sourceSplitReason  string
	sourcePreprocessMS int64
	sourceTextChars    int
	sourceTextWords    int
	searchStart        time.Time
	searchEnd          time.Time
	webContext         string
}

func batchSourceSplitLimit(targetWords int) int {
	if targetWords <= 0 {
		targetWords = 1800
	}
	limit := targetWords * 2
	if limit < batchSourceSplitMinWords {
		limit = batchSourceSplitMinWords
	}
	if limit > batchSourceSplitMaxWords {
		limit = batchSourceSplitMaxWords
	}
	return limit
}

func buildBatchWorkItems(topic, sourceText, sourceOrigin, webContext string, searchStart, searchEnd time.Time, targetWords int) []batchWorkItem {
	topic = strings.TrimSpace(topic)
	sourceText = strings.TrimSpace(sourceText)
	sourceOrigin = strings.TrimSpace(sourceOrigin)
	if sourceOrigin == "" {
		sourceOrigin = "inline_text"
	}

	preprocessStartedAt := time.Now()
	parts := splitBatchSourceText(sourceText, batchSourceSplitLimit(targetWords))
	preprocessMS := time.Since(preprocessStartedAt).Milliseconds()
	if len(parts) == 0 {
		parts = []string{sourceText}
	}

	items := make([]batchWorkItem, 0, len(parts))
	total := len(parts)
	for idx, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		itemTopic := topic
		if total > 1 {
			itemTopic = fmt.Sprintf("%s (Part %d/%d)", topic, idx+1, total)
		}
		splitParent := ""
		splitIndex := 0
		splitReason := ""
		if total > 1 {
			splitParent = topic
			splitIndex = idx + 1
			splitReason = splitReasonForSource(total)
		}
		items = append(items, batchWorkItem{
			topic:              itemTopic,
			sourceText:         part,
			sourceOrigin:       sourceOrigin,
			sourceSplitParent:  splitParent,
			sourceSplitIndex:   splitIndex,
			sourceSplitTotal:   total,
			sourceSplitReason:  splitReason,
			sourcePreprocessMS: preprocessMS,
			sourceTextChars:    len(part),
			sourceTextWords:    textutil.CountWords(part),
			searchStart:        searchStart,
			searchEnd:          searchEnd,
			webContext:         webContext,
		})
	}
	if len(items) == 0 {
		items = append(items, batchWorkItem{
			topic:           topic,
			sourceText:      sourceText,
			sourceOrigin:    sourceOrigin,
			sourceTextChars: len(sourceText),
			sourceTextWords: textutil.CountWords(sourceText),
			searchStart:     searchStart,
			searchEnd:       searchEnd,
			webContext:      webContext,
		})
	}
	return items
}

func splitReasonForSource(total int) string {
	if total > 1 {
		return "long_source_text"
	}
	return ""
}

func splitBatchSourceText(text string, maxWords int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxWords <= 0 {
		maxWords = batchSourceSplitLimit(1800)
	}
	if textutil.CountWords(text) <= maxWords {
		return []string{text}
	}

	paragraphs := splitBatchParagraphs(text)
	if len(paragraphs) == 0 {
		paragraphs = []string{text}
	}

	var chunks []string
	var current []string
	currentWords := 0

	flushCurrent := func() {
		if len(current) == 0 {
			return
		}
		chunk := strings.TrimSpace(strings.Join(current, "\n\n"))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		current = nil
		currentWords = 0
	}

	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		paragraphWords := textutil.CountWords(paragraph)
		if paragraphWords > maxWords {
			flushCurrent()
			chunks = append(chunks, splitBatchParagraphIntoChunks(paragraph, maxWords)...)
			continue
		}
		if currentWords > 0 && currentWords+paragraphWords > maxWords {
			flushCurrent()
		}
		current = append(current, paragraph)
		currentWords += paragraphWords
	}
	flushCurrent()

	return compactBatchChunks(chunks)
}

func splitBatchParagraphs(text string) []string {
	blocks := strings.Split(text, "\n\n")
	paragraphs := make([]string, 0, len(blocks))
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block != "" {
			paragraphs = append(paragraphs, block)
		}
	}
	return paragraphs
}

func splitBatchParagraphIntoChunks(paragraph string, maxWords int) []string {
	sentences := splitBatchSentences(paragraph)
	if len(sentences) == 0 {
		return splitBatchWordsIntoChunks(paragraph, maxWords)
	}

	var chunks []string
	var current []string
	currentWords := 0

	flushCurrent := func() {
		if len(current) == 0 {
			return
		}
		chunk := strings.TrimSpace(strings.Join(current, " "))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		current = nil
		currentWords = 0
	}

	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		sentenceWords := textutil.CountWords(sentence)
		if sentenceWords > maxWords {
			flushCurrent()
			chunks = append(chunks, splitBatchWordsIntoChunks(sentence, maxWords)...)
			continue
		}
		if currentWords > 0 && currentWords+sentenceWords > maxWords {
			flushCurrent()
		}
		current = append(current, sentence)
		currentWords += sentenceWords
	}
	flushCurrent()

	return compactBatchChunks(chunks)
}

func splitBatchSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	matches := regexp.MustCompile(`[^.!?]+[.!?]*`).FindAllString(text, -1)
	if len(matches) == 0 {
		return []string{text}
	}

	sentences := make([]string, 0, len(matches))
	for _, match := range matches {
		match = strings.TrimSpace(match)
		if match != "" {
			sentences = append(sentences, match)
		}
	}
	if len(sentences) == 0 {
		return []string{text}
	}
	return sentences
}

func splitBatchWordsIntoChunks(text string, maxWords int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxWords <= 0 {
		maxWords = batchSourceSplitLimit(1800)
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= maxWords {
		return []string{text}
	}

	chunks := make([]string, 0, (len(words)/maxWords)+1)
	for start := 0; start < len(words); start += maxWords {
		end := start + maxWords
		if end > len(words) {
			end = len(words)
		}
		chunk := strings.TrimSpace(strings.Join(words[start:end], " "))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}
	return compactBatchChunks(chunks)
}

func compactBatchChunks(chunks []string) []string {
	if len(chunks) == 0 {
		return nil
	}
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk != "" {
			out = append(out, chunk)
		}
	}
	return out
}
