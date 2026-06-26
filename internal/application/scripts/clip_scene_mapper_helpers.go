package scripts

import (
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func resolveMarkerClipID(marker string, clipIDs, clipNames []string) string {
	marker = strings.TrimSpace(marker)
	for _, id := range clipIDs {
		if strings.EqualFold(strings.TrimSpace(id), marker) {
			return id
		}
	}
	for i, name := range clipNames {
		if strings.EqualFold(strings.TrimSpace(name), marker) && i < len(clipIDs) {
			return clipIDs[i]
		}
	}
	return marker
}

func clipIdentityFromPack(pack map[string]any) ([]string, []string) {
	if pack == nil {
		return nil, nil
	}
	clipIDs, _ := pack["clip_ids"].([]string)
	clipNames, _ := pack["clip_names"].([]string)
	return cleanStrings(clipIDs), cleanStrings(clipNames)
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func splitScriptAcrossClips(script string, count int) []string {
	if count <= 0 {
		return nil
	}
	paragraphs := nonEmptyBlocks(strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n\n"))
	if len(paragraphs) == count {
		return paragraphs
	}
	sentences := textutil.SplitScriptSentences(script)
	if len(sentences) == 0 {
		sentences = []string{script}
	}
	out := make([]string, count)
	for i, sentence := range sentences {
		bucket := i * count / len(sentences)
		if bucket >= count {
			bucket = count - 1
		}
		if out[bucket] != "" {
			out[bucket] += " "
		}
		out[bucket] += strings.TrimSpace(sentence)
	}
	return out
}

func nonEmptyBlocks(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
