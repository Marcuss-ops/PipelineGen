package linguistics

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// loadWordSet reads one word per line from the file at path into dest.
// Blank lines and lines starting with '#' are skipped. All words are
// lowercased before insertion.
//
// Phase 8 split: file-loading helper. Used by NewLexiconRegistry
// (lexicon_registry.go) for stopwords.txt / function_words.txt /
// entity_blocklist.txt / negative_particles.txt / visual_verbs.txt.
//
// Missing file is NOT an error — the destination map stays empty.
// This matches the pre-Phase-8 behaviour: any optional lexicon file
// may be absent, and the registry proceeds with empty sets that the
// fallback profile can supplement.
func loadWordSet(path string, dest map[string]struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lexicon registry: open %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		dest[strings.ToLower(word)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lexicon registry: scan %q: %w", path, err)
	}
	return nil
}

// loadStringList reads one string per line from the file at path.
// Like loadWordSet but preserves order (used for verb_morphology.txt
// suffix lists where ordering matters for the suffix-stripping loop).
//
// Phase 8 split: file-loading helper. Used by NewLexiconRegistry
// (lexicon_registry.go) for verb_morphology.txt.
func loadStringList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lexicon registry: open %q: %w", path, err)
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.ToLower(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("lexicon registry: scan %q: %w", path, err)
	}
	return out, nil
}

// loadPhrasePolicy reads a simple key=value file (one pair per line)
// and returns the resulting PhraseExtractionPolicy + an "ok" flag
// indicating whether the file was present. Recognised keys:
// min_words, max_words, max_results, reject_verbs_when_all.
//
// Phase 8 split: file-loading helper. Used by NewLexiconRegistry
// (lexicon_registry.go) for phrase_policy.txt. Falls back to
// DefaultPhraseExtractionPolicy() when the file is absent or when
// the ok flag is false.
func loadPhrasePolicy(path string) (PhraseExtractionPolicy, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PhraseExtractionPolicy{}, false, nil
		}
		return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: open %q: %w", path, err)
	}
	defer f.Close()

	p := DefaultPhraseExtractionPolicy()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: malformed phrase policy line %q in %q", line, path)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "min_words":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse min_words in %q: %w", path, parseErr)
			}
			p.MinWords = n
		case "max_words":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse max_words in %q: %w", path, parseErr)
			}
			p.MaxWords = n
		case "max_results":
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse max_results in %q: %w", path, parseErr)
			}
			p.MaxResults = n
		case "reject_verbs_when_all":
			b, parseErr := strconv.ParseBool(val)
			if parseErr != nil {
				return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: parse reject_verbs_when_all in %q: %w", path, parseErr)
			}
			p.RejectVerbsWhenAll = b
		default:
			return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: unknown phrase policy key %q in %q", key, path)
		}
	}
	if err := scanner.Err(); err != nil {
		return PhraseExtractionPolicy{}, false, fmt.Errorf("lexicon registry: scan %q: %w", path, err)
	}
	return p, true, nil
}
