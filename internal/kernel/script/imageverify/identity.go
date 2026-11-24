package imageverify

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normalizeEntityMatch is the identity join for entity-image candidates:
// case-folded, accent-stripped and English-possessive-stripped.
func normalizeEntityMatch(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	// NER commonly emits an English possessive when the name is the subject
	// of a sentence ("Dwayne Johnson's journey"), while image candidates are
	// keyed by the canonical identity ("Dwayne Johnson").
	return strings.TrimSuffix(strings.TrimSuffix(b.String(), "'s"), "’s")
}

// PersonImageSourceIdentityFailure rejects only a high-confidence provenance
// conflict: an image filename that explicitly names the requested person with
// a different surname (for example Angela-Tuccia for Angela Taccia). A generic
// editorial filename (IMG_6075.jpeg) is NOT proof of a wrong identity — the
// pixel/visual verifier owns that decision. Requiring the canonical name in
// every URL made the catalog drop valid portraits before the VLM could see
// them, which is the Kaufmann zero-image failure mode.
func PersonImageSourceIdentityFailure(canonicalName, sourceURL, _ string) string {
	return personImageSourceNameMismatch(canonicalName, sourceURL)
}

// PersonImageSourceIdentityPenalty returns a soft ranking penalty for a
// filename that explicitly names more than the requested person. The hard
// different-surname gate stays fail-closed; a multi-person editorial filename
// is downgraded, not zeroed, so the pixel verifier can still recover a scarce
// but valid portrait.
func PersonImageSourceIdentityPenalty(canonicalName, sourceURL string) float64 {
	reason := personImageSourceNameMismatch(canonicalName, sourceURL)
	if strings.Contains(reason, "names multiple people") {
		return 0.25
	}
	return 0
}

// personImageSourceNameMismatch catches a high-confidence provenance conflict
// when an image filename explicitly names the queried person but spells the
// surname differently (for example Angela-Tuccia for Angela Taccia). Provider
// adapters label results with the search query, so Entity alone is not proof
// of image identity. Generic filenames remain eligible for the pixel/layout
// verifier and later source checks.
func personImageSourceNameMismatch(canonicalName, sourceURL string) string {
	name := strings.Fields(normalizeEntityMatch(canonicalName))
	if len(name) < 2 || strings.TrimSpace(sourceURL) == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return ""
	}
	basename := path.Base(parsed.Path)
	for i := 0; i < 2; i++ {
		if decoded, decodeErr := url.PathUnescape(basename); decodeErr == nil && decoded != basename {
			basename = decoded
		}
	}
	basename = strings.TrimSuffix(basename, path.Ext(basename))
	tokens := strings.FieldsFunc(strings.ToLower(basename), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(tokens) < 2 || tokens[0] != name[0] {
		return ""
	}
	second := tokens[1]
	joinedSurname := strings.NewReplacer("'", "", "’", "", "-", "").Replace(strings.Join(name[1:], ""))
	matchesFullName := len(tokens) >= len(name)
	if matchesFullName {
		for i := range name {
			if tokens[i] != name[i] {
				matchesFullName = false
				break
			}
		}
	}
	matchesJoinedSurname := second == joinedSurname
	if !matchesFullName && !matchesJoinedSurname && second != name[len(name)-1] && (len([]rune(second)) < 3 || personImageFilenameQualifier(second)) {
		return ""
	}
	// A filename may contain one canonical surname plus generic qualifiers
	// ("john-gotti-photo-2020"). Two additional name-like tokens are not a
	// qualifier: "john-gotti-sammy-gravano" names a second person and must not
	// be reusable as John Gotti's portrait.
	consumed := 1
	for consumed < len(name) && consumed < len(tokens) && tokens[consumed] == name[consumed] {
		consumed++
	}
	if consumed < len(name) && (second == name[len(name)-1] || matchesJoinedSurname) {
		consumed = 2
	}
	extraNames := 0
	for _, token := range tokens[consumed:] {
		if len([]rune(token)) < 3 || personImageFilenameQualifier(token) || isASCIIDigits(token) {
			continue
		}
		extraNames++
	}
	if extraNames >= 2 {
		return fmt.Sprintf("image source filename names multiple people, not only requested person %s", canonicalName)
	}
	if !matchesFullName && !matchesJoinedSurname && second != name[len(name)-1] {
		return fmt.Sprintf("image source filename names %s %s, not requested person %s", name[0], second, canonicalName)
	}
	return ""
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func personImageFilenameQualifier(token string) bool {
	switch token {
	case "foto", "photo", "portrait", "headshot", "image", "img", "profile", "person", "persona", "ritratto",
		"di", "della", "de", "del", "la", "il", "e", "the", "for", "case", "news", "ufficiale", "ufficiali",
		"commencement", "speech", "address", "interview", "press", "conference", "event", "stage",
		"face", "tattoo", "boxer", "boxing", "actor", "actress", "director", "investor", "president",
		"movie", "film", "martial", "arts", "legendary", "wallpaper", "cover", "hero", "banner",
		"mugshot", "arrest", "trial", "processo", "cropped", "scaled", "original", "full", "large":
		return true
	default:
		return false
	}
}
