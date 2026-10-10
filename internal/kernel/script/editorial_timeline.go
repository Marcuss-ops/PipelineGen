package script

// Editorial Timeline (P0): certified display windows from TTS word timing.
//
// The timeline NEVER invents timestamps from reading-speed estimates. It
// consumes certified per-language word timing and produces anchored
// intervals. Without a reliable anchor, the element stays unanchored and
// must not be synchronized.

import (
	"errors"
	"sort"
)

// WordMark is one certified spoken token in a language.
type WordMark struct {
	Text    string `json:"text"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// TimelineWindow is a certified display interval for one element.
type TimelineWindow struct {
	CanonicalID CanonicalID `json:"canonical_id"`
	Language    string      `json:"language"`
	StartMS     int64       `json:"start_ms"`
	EndMS       int64       `json:"end_ms"`
	// MinDurationEnforced is true when the spoken span was shorter than the
	// legibility floor and the window was extended symmetrically within the
	// scene bounds instead of inventing new timing.
	MinDurationEnforced bool `json:"min_duration_enforced"`
}

// AnchorElements maps each element text to its spoken span in the word
// marks (case-insensitive contiguous match) and returns certified windows.
// Elements with no reliable anchor are returned unanchored (ok=false) and
// no window is fabricated for them.
func AnchorElements(ids []CanonicalID, texts map[CanonicalID]string, language string, words []WordMark, minDurationMS, sceneStartMS, sceneEndMS int64) (anchored []TimelineWindow, unanchored []CanonicalID) {
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = foldToken(w.Text)
	}
	for _, id := range ids {
		text := texts[id]
		tokens := foldTokens(text)
		if len(tokens) == 0 {
			unanchored = append(unanchored, id)
			continue
		}
		start, end, spanErr := findTokenSpan(lower, tokens)
		if spanErr != nil {
			unanchored = append(unanchored, id)
			continue
		}
		s, e := words[start].StartMS, words[end-1].EndMS
		if s < 0 || e <= s {
			unanchored = append(unanchored, id)
			continue
		}
		enforced := false
		if e-s < minDurationMS {
			// Extend symmetrically, clamped to the scene bounds. If the
			// scene itself cannot host the floor, leave unanchored.
			need := minDurationMS - (e - s)
			s -= need / 2
			e += need - need/2
			if s < sceneStartMS {
				e += sceneStartMS - s
				s = sceneStartMS
			}
			if e > sceneEndMS {
				s -= e - sceneEndMS
				e = sceneEndMS
			}
			if s < sceneStartMS || e > sceneEndMS || e-s < minDurationMS {
				unanchored = append(unanchored, id)
				continue
			}
			enforced = true
		}
		anchored = append(anchored, TimelineWindow{
			CanonicalID: id, Language: language,
			StartMS: s, EndMS: e, MinDurationEnforced: enforced,
		})
	}
	sort.Slice(anchored, func(i, j int) bool {
		if anchored[i].StartMS != anchored[j].StartMS {
			return anchored[i].StartMS < anchored[j].StartMS
		}
		return anchored[i].CanonicalID < anchored[j].CanonicalID
	})
	return anchored, unanchored
}

// Overlaps reports whether two windows overlap in time.
func Overlaps(a, b TimelineWindow) bool {
	return a.StartMS < b.EndMS && b.StartMS < a.EndMS
}

func foldToken(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c >= 0x80 {
			out = append(out, c)
		}
	}
	return string(out)
}

func foldTokens(text string) []string {
	var toks []string
	for _, f := range splitFields(text) {
		if t := foldToken(f); t != "" {
			toks = append(toks, t)
		}
	}
	return toks
}

func splitFields(s string) []string {
	var out []string
	start := -1
	for i := 0; i <= len(s); i++ {
		sep := i == len(s) || s[i] == ' ' || s[i] == '\t' || s[i] == '\n'
		if !sep && start < 0 {
			start = i
		}
		if sep && start >= 0 {
			out = append(out, s[start:i])
			start = -1
		}
	}
	return out
}

func findTokenSpan(haystack, needle []string) (int, int, error) {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return 0, 0, errors.New("no span")
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i, i + len(needle), nil
		}
	}
	return 0, 0, errors.New("no span")
}
