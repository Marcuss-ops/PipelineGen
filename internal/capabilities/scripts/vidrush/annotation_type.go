package vidrush

import "strings"

// normalizeAnnotationType folds annotation spellings onto the canonical
// uppercase entity kind.
func normalizeAnnotationType(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// MaxSemanticVerificationCandidates bounds how many provider candidates one
// semantic query sends to the pixel/VLM verifier.
const MaxSemanticVerificationCandidates = 8
