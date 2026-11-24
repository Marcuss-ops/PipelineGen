package vidrush

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func contextualImageLaneRequested(perSceneImages, automaticImageSearch, manualImageExclusive bool) bool {
	return automaticImageSearch && !manualImageExclusive && !perSceneImages
}

func validateVidRushImageHead(status int, _ string, contentLength int64, policy VidRushImagePolicy) error {
	policy = normalizeVidRushImagePolicy(policy)
	if status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented || status == http.StatusForbidden {
		// Some image CDNs deny HEAD while serving the same URL to a normal GET.
		// Treat the preflight as advisory; the guarded GET validates status, MIME,
		// byte limits, and decoded image content before accepting anything.
		return nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("vidrush image: upstream HEAD status %d", status)
	}
	// HEAD metadata is advisory: several image hosts return text/plain or a
	// placeholder length here even though GET serves the actual image. GET
	// validates the response MIME against sniffed bytes and decodes the image.
	if contentLength > policy.MaxBytes {
		return ErrVidRushImageTooLarge
	}
	return nil
}

func sourcePathHasCanonicalPersonName(canonicalName, source string) bool {
	if strings.TrimSpace(canonicalName) == "" || strings.TrimSpace(source) == "" {
		return false
	}
	parsed, err := neturl.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	decoded := parsed.Path
	for i := 0; i < 2; i++ {
		if next, decodeErr := neturl.PathUnescape(decoded); decodeErr == nil && next != decoded {
			decoded = next
		}
	}
	tokens := strings.FieldsFunc(strings.ToLower(decoded), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	want := strings.FieldsFunc(normalizeEntityMatch(canonicalName), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(want) < 2 || len(tokens) < len(want) {
		return false
	}
	for start := 0; start <= len(tokens)-len(want); start++ {
		match := true
		for i := range want {
			if tokens[start+i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func capSemanticVerificationCandidates(in []scriptpkg.SegmentAssetCandidate, limit int) []scriptpkg.SegmentAssetCandidate {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}
