package script

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// SceneMetadata carries technical scene data that should not be
// read as narration. It is separate from Text by contract.
type SceneMetadata struct {
	SourceURL string            `json:"source_url,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Keywords  []string          `json:"keywords,omitempty"`
	Raw       string            `json:"raw,omitempty"`
	Sources   []SourceReference `json:"sources,omitempty"`
}

// SourceReference is editorial provenance. It is deliberately kept outside
// SpecScene.Text because Text is the exact speakable surface sent to TTS.
type SourceReference struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Type  string `json:"type,omitempty"`
}

var ErrNarrationNotSpeakable = errors.New("NARRATION_NOT_SPEAKABLE")

var (
	markdownSourceRE = regexp.MustCompile(`(?i)\[(?:fonte|source)\s*:\s*([^\]]+?)\]\((https?://[^)\s]+)\)`)
	markdownLinkRE   = regexp.MustCompile(`\[([^\]]+?)\]\((https?://[^)\s]+)\)`)
	bracketSourceRE  = regexp.MustCompile(`(?i)\[(?:fonte|source)\s*:\s*([^\]]+?)\]`)
	bareURLRE        = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s)\]}>]+`)
	bareMarkerRE     = regexp.MustCompile(`(?i)https?://|www\.`)
	sourceLineRE     = regexp.MustCompile(`(?im)^\s*(?:fonti|sources?)\s*:\s*.*$`)
)

// SanitizeNarration extracts editorial source markers and returns only text
// that is safe to send to a speech engine. It is idempotent.
func SanitizeNarration(text string) (string, []SourceReference, error) {
	clean := strings.TrimSpace(text)
	var sources []SourceReference
	add := func(title, rawURL string) {
		rawURL = strings.TrimRight(strings.TrimSpace(rawURL), ".,;:!?")
		title = strings.TrimSpace(title)
		if title == "" {
			title = rawURL
		}
		ref := SourceReference{Title: title, URL: rawURL, Type: sourceType(rawURL)}
		for _, existing := range sources {
			if strings.EqualFold(existing.URL, ref.URL) && existing.Title == ref.Title {
				return
			}
		}
		sources = append(sources, ref)
	}
	clean = markdownSourceRE.ReplaceAllStringFunc(clean, func(match string) string {
		parts := markdownSourceRE.FindStringSubmatch(match)
		add(parts[1], parts[2])
		return ""
	})
	clean = markdownLinkRE.ReplaceAllStringFunc(clean, func(match string) string {
		parts := markdownLinkRE.FindStringSubmatch(match)
		add(parts[1], parts[2])
		return ""
	})
	clean = bracketSourceRE.ReplaceAllStringFunc(clean, func(match string) string {
		parts := bracketSourceRE.FindStringSubmatch(match)
		add(parts[1], "")
		return ""
	})
	clean = sourceLineRE.ReplaceAllString(clean, "")
	clean = bareURLRE.ReplaceAllStringFunc(clean, func(match string) string {
		add("", match)
		return ""
	})
	// Models occasionally emit a truncated source marker such as a bare
	// "https://" with no hostname. It is not matched by bareURLRE, but it
	// must still never reach TTS.
	clean = bareMarkerRE.ReplaceAllString(clean, "")
	clean = strings.Join(strings.Fields(clean), " ")
	clean = strings.TrimSpace(strings.Trim(clean, "-–—:; "))
	clean = strings.ReplaceAll(clean, " ,", ",")
	clean = strings.ReplaceAll(clean, " .", ".")
	if err := ValidateSpeakableText(clean); err != nil {
		return "", sources, err
	}
	return clean, sources, nil
}

// ValidateSpeakableText is the final fail-closed guard before TTS.
func ValidateSpeakableText(text string) error {
	for _, token := range []string{"http://", "https://", "www.", "[Fonte:", "[Source:", "]("} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(token)) {
			return fmt.Errorf("%w: forbidden source marker %q", ErrNarrationNotSpeakable, token)
		}
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: empty narration", ErrNarrationNotSpeakable)
	}
	return nil
}

func sourceType(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && strings.Contains(strings.ToLower(u.Host), "youtube") {
		return "youtube"
	}
	if rawURL != "" {
		return "article"
	}
	return "editorial"
}
