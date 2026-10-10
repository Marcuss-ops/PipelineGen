package script

// Cross-language Identity (P1, implementata per prima): stable logical ids,
// distinct from translations. Timings differ per language; identity never does.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// IdentityRegistry assigns canonical ids to editorial elements of one job
// and stores their per-language localizations.
type IdentityRegistry struct {
	sourceLanguage string
	counters       map[string]int
	elements       map[CanonicalID]*LocalizedElement
	order          []CanonicalID
}

// NewIdentityRegistry creates a registry rooted at the source language.
func NewIdentityRegistry(sourceLanguage string) *IdentityRegistry {
	if strings.TrimSpace(sourceLanguage) == "" {
		sourceLanguage = "en"
	}
	return &IdentityRegistry{
		sourceLanguage: sourceLanguage,
		counters:       map[string]int{},
		elements:       map[CanonicalID]*LocalizedElement{},
	}
}

// Register mints the next canonical id for (scene, kind) and stores the
// source-language element. Deterministic: same registration order yields
// the same ids across retries.
func (r *IdentityRegistry) Register(sceneID, kind, sourceText string) (CanonicalID, error) {
	key := sceneID + ":" + kind
	n := r.counters[key]
	id, err := MakeCanonicalID(sceneID, kind, n)
	if err != nil {
		return "", err
	}
	if _, dup := r.elements[id]; dup {
		return "", fmt.Errorf("canonical id collision %q", id)
	}
	r.counters[key] = n + 1
	r.elements[id] = &LocalizedElement{
		CanonicalID: id, SourceLanguage: r.sourceLanguage,
		Language: r.sourceLanguage, Text: sourceText,
	}
	r.order = append(r.order, id)
	return id, nil
}

// Localize attaches a translated text (and later, local timing) to an
// existing canonical id. Unknown ids fail closed: translations never mint
// identity.
func (r *IdentityRegistry) Localize(id CanonicalID, language, text string) error {
	el, ok := r.elements[id]
	if !ok {
		return fmt.Errorf("localize unknown canonical id %q", id)
	}
	if strings.TrimSpace(language) == "" || language == el.SourceLanguage {
		return errors.New("localize requires a non-source language")
	}
	cp := *el
	cp.Language = language
	cp.Text = text
	cp.StartMS, cp.EndMS, cp.Anchored = nil, nil, false
	r.elements[localizedKey(id, language)] = &cp
	return nil
}

func localizedKey(id CanonicalID, language string) CanonicalID {
	return CanonicalID(string(id) + "@" + language)
}

// Get returns the source element for a canonical id.
func (r *IdentityRegistry) Get(id CanonicalID) (*LocalizedElement, bool) {
	el, ok := r.elements[id]
	if !ok {
		return nil, false
	}
	cp := *el
	return &cp, true
}

// GetLocalized returns the element text for (id, language).
func (r *IdentityRegistry) GetLocalized(id CanonicalID, language string) (*LocalizedElement, bool) {
	if language == r.sourceLanguage {
		return r.Get(id)
	}
	el, ok := r.elements[localizedKey(id, language)]
	if !ok {
		return nil, false
	}
	cp := *el
	return &cp, true
}

// AnchorTiming records certified local timing for (id, language). Timing
// for the source language is recorded directly on the source element.
func (r *IdentityRegistry) AnchorTiming(id CanonicalID, language string, startMS, endMS int64) error {
	if startMS < 0 || endMS <= startMS {
		return errors.New("invalid anchor window")
	}
	if language == r.sourceLanguage {
		el, ok := r.elements[id]
		if !ok {
			return fmt.Errorf("anchor unknown canonical id %q", id)
		}
		el.StartMS, el.EndMS, el.Anchored = &startMS, &endMS, true
		return nil
	}
	key := localizedKey(id, language)
	el, ok := r.elements[key]
	if !ok {
		return fmt.Errorf("anchor before localize for %q", id)
	}
	el.StartMS, el.EndMS, el.Anchored = &startMS, &endMS, true
	return nil
}

// OrderedIDs returns canonical source ids in registration order.
func (r *IdentityRegistry) OrderedIDs() []CanonicalID {
	return append([]CanonicalID(nil), r.order...)
}

// SceneIDs returns the distinct scene ids in first-appearance order.
func (r *IdentityRegistry) SceneIDs() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, id := range r.order {
		scene, _, _, err := ParseCanonicalID(id)
		if err != nil {
			continue
		}
		if _, ok := seen[scene]; !ok {
			seen[scene] = struct{}{}
			out = append(out, scene)
		}
	}
	sort.Strings(out)
	return out
}
