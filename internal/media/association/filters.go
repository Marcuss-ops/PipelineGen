package association

import (
	"path/filepath"
	"strings"
	"velox/go-master/internal/pkg/sliceutil"
	"velox/go-master/internal/pkg/textutil"
)

// FilterStockMatchesBySubject filters matches based on their relevance to the segment subject.
func FilterStockMatchesBySubject(matches []ScoredMatch, subject string) []ScoredMatch {
	if len(matches) == 0 {
		return nil
	}
	subjectKeys := AssociationSubjectKeys(subject)
	if len(subjectKeys) == 0 {
		return matches
	}

	exact := make([]ScoredMatch, 0, len(matches))
	loose := make([]ScoredMatch, 0, len(matches))
	for _, match := range matches {
		titleKey := textutil.Normalize(match.Title)
		pathKey := textutil.Normalize(match.Path)
		leafKey := textutil.Normalize(filepath.Base(strings.TrimSpace(match.Path)))

		if NormalizedKeyMatchesAny(titleKey, subjectKeys) || NormalizedKeyMatchesAny(pathKey, subjectKeys) || NormalizedKeyMatchesAny(leafKey, subjectKeys) {
			exact = append(exact, match)
			continue
		}
		for _, subjectKey := range subjectKeys {
			if strings.HasSuffix(pathKey, "/"+subjectKey) || strings.HasSuffix(titleKey, subjectKey) {
				loose = append(loose, match)
				break
			}
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return loose
}

// FilterArtlistMatchesBySubject is similar to FilterStockMatchesBySubject but for Artlist sources.
func FilterArtlistMatchesBySubject(matches []ScoredMatch, subject string) []ScoredMatch {
	return FilterStockMatchesBySubject(matches, subject)
}

// AssociationSubjectKeys generates normalized keys for a subject (including stripped prefixes).
func AssociationSubjectKeys(subject string) []string {
	subjectKey := textutil.Normalize(subject)
	if subjectKey == "" {
		return nil
	}
	keys := []string{subjectKey}
	for _, prefix := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(subjectKey, prefix) {
			if stripped := strings.TrimSpace(strings.TrimPrefix(subjectKey, prefix)); stripped != "" {
				keys = append(keys, stripped)
			}
			break
		}
	}
	return sliceutil.UniqueStrings(keys)
}

// NormalizedKeyMatchesAny checks if a key matches any of the subject keys.
func NormalizedKeyMatchesAny(key string, subjectKeys []string) bool {
	key = textutil.Normalize(key)
	if key == "" {
		return false
	}
	for _, subjectKey := range subjectKeys {
		if key == subjectKey {
			return true
		}
	}
	return false
}

// PreferredPathsFromMatches extracts the best paths from a set of matches.
func PreferredPathsFromMatches(matches []ScoredMatch) []string {
	if len(matches) == 0 {
		return nil
	}
	best := matches[0]
	for _, match := range matches[1:] {
		if match.Score > best.Score {
			best = match
		}
	}
	preferred := []string{
		strings.TrimSpace(best.Path),
		strings.TrimSpace(best.Link),
	}
	return sliceutil.UniqueStrings(sliceutil.TrimStrings(preferred))
}

// HasUsefulStockMatch returns true if at least one match has a non-empty, non-broad path.
func HasUsefulStockMatch(matches []ScoredMatch) bool {
	for _, match := range matches {
		if strings.TrimSpace(match.Path) == "" {
			continue
		}
		if !LooksBroadStockContainer(match.Path) {
			return true
		}
	}
	return false
}

// LooksBroadStockContainer returns true if the path suggests a broad category rather than a specific asset.
func LooksBroadStockContainer(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	return strings.Contains(path, ",") || strings.Contains(path, " and ")
}
