package script

// PipelineGen 2.0 shared foundation: canonical identity + dependency contract.
//
// The five systems (Cross-language Identity, Editorial Timeline, Visual
// Admission Resolver, Visual Diversity Planner, Revision/Invalidation Graph)
// share these identifiers, never five divergent schemes:
//   - SceneID:      "scene-03" (stable across languages and retries)
//   - CanonicalID:  "scene-03:highlight-02" (one editorial element, all languages)
//   - TimelineKey:  CanonicalID + language (local timing per language)
// Nothing here alters production paths; it is the contract the next
// implementation phase builds on.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// CanonicalID identifies one editorial element across all languages.
// Format: "<scene-id>:<kind>-<index>", e.g. "scene-03:highlight-02".
type CanonicalID string

// EditorialKinds admitted to canonical identity.
const (
	EditorialKindHighlight = "highlight"
	EditorialKindBullet    = "bullet"
	EditorialKindTitle     = "title"
	EditorialKindChart     = "chart"
)

// MakeCanonicalID builds and validates a canonical element id.
func MakeCanonicalID(sceneID, kind string, index int) (CanonicalID, error) {
	if strings.TrimSpace(sceneID) == "" {
		return "", errors.New("canonical id requires scene id")
	}
	switch kind {
	case EditorialKindHighlight, EditorialKindBullet, EditorialKindTitle, EditorialKindChart:
	default:
		return "", fmt.Errorf("unknown editorial kind %q", kind)
	}
	if index < 0 {
		return "", errors.New("canonical index must be non-negative")
	}
	return CanonicalID(fmt.Sprintf("%s:%s-%02d", sceneID, kind, index)), nil
}

// ParseCanonicalID splits a canonical id back into its parts.
func ParseCanonicalID(id CanonicalID) (sceneID, kind string, index int, err error) {
	parts := strings.SplitN(string(id), ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", "", 0, fmt.Errorf("invalid canonical id %q", id)
	}
	rest := strings.SplitN(parts[1], "-", 2)
	if len(rest) != 2 {
		return "", "", 0, fmt.Errorf("invalid canonical id %q", id)
	}
	var n int
	if _, serr := fmt.Sscanf(rest[1], "%d", &n); serr != nil || n < 0 {
		return "", "", 0, fmt.Errorf("invalid canonical id %q", id)
	}
	switch rest[0] {
	case EditorialKindHighlight, EditorialKindBullet, EditorialKindTitle, EditorialKindChart:
		return parts[0], rest[0], n, nil
	default:
		return "", "", 0, fmt.Errorf("invalid canonical id %q", id)
	}
}

// LocalizedElement is one editorial element in one language. The canonical
// id and source text are shared; text and timing are local. Word counts and
// durations may differ per language — identity must not.
type LocalizedElement struct {
	CanonicalID    CanonicalID `json:"canonical_id"`
	SourceLanguage string      `json:"source_language"`
	Language       string      `json:"language"`
	Text           string      `json:"text"`
	StartMS        *int64      `json:"start_ms,omitempty"`
	EndMS          *int64      `json:"end_ms,omitempty"`
	Anchored       bool        `json:"anchored"`
}

// DepNode is the invalidation-graph contract, defined from phase one so
// every later product records its dependencies instead of relying on a
// scene_id-only cache.
type DepNode struct {
	// ID names the product, e.g. "editorial:scene-03" or "timeline:scene-03:it".
	ID string `json:"id"`
	// Inputs is the sorted list of content fingerprints this product derives
	// from (scene text, topics, profile, embedding model, source spans...).
	Inputs []string `json:"inputs"`
	// Product is the fingerprint of the derived artifact itself.
	Product string `json:"product"`
	// Global marks products that depend on cross-scene state (chapters,
	// diversity windows spanning neighbours): any scene change invalidates them.
	Global bool `json:"global"`
}

// FingerprintBytes hashes arbitrary canonical JSON.
func FingerprintBytes(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return digest.SHA256Bytes(raw), nil
}

// InvalidationGraph records product dependencies and computes the minimal
// rebuild set when inputs change.
type InvalidationGraph struct {
	nodes map[string]*DepNode
	// rev maps an input fingerprint to the products depending on it.
	rev map[string]map[string]struct{}
}

// NewInvalidationGraph builds an empty graph.
func NewInvalidationGraph() *InvalidationGraph {
	return &InvalidationGraph{nodes: map[string]*DepNode{}, rev: map[string]map[string]struct{}{}}
}

// Upsert records or replaces a product node. Inputs are sorted canonically.
func (g *InvalidationGraph) Upsert(node DepNode) {
	inputs := append([]string(nil), node.Inputs...)
	sort.Strings(inputs)
	node.Inputs = inputs
	if g.nodes == nil {
		g.nodes = map[string]*DepNode{}
		g.rev = map[string]map[string]struct{}{}
	}
	if old, ok := g.nodes[node.ID]; ok {
		for _, in := range old.Inputs {
			if s := g.rev[in]; s != nil {
				delete(s, node.ID)
			}
		}
	}
	cp := node
	g.nodes[node.ID] = &cp
	for _, in := range inputs {
		if g.rev[in] == nil {
			g.rev[in] = map[string]struct{}{}
		}
		g.rev[in][node.ID] = struct{}{}
	}
} // Invalidate returns the IDs of products depending on any changed input,
// plus every global product when at least one KNOWN input changed. Unknown
// inputs (depended on by nothing) invalidate nothing, and globals never
// invalidate on an empty or unrelated change: they react to scene changes,
// not to every call.
// Products whose inputs are all unchanged are never returned.
func (g *InvalidationGraph) Invalidate(changedInputs []string) []string {
	known := map[string]struct{}{}
	for _, node := range g.nodes {
		for _, in := range node.Inputs {
			known[in] = struct{}{}
		}
	}
	changed := map[string]struct{}{}
	for _, in := range changedInputs {
		if _, ok := known[in]; ok {
			changed[in] = struct{}{}
		}
	}
	if len(changed) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for id, node := range g.nodes {
		if node.Global {
			out[id] = struct{}{}
			continue
		}
		for _, in := range node.Inputs {
			if _, ok := changed[in]; ok {
				out[id] = struct{}{}
				break
			}
		}
	}
	ids := make([]string, 0, len(out))
	for id := range out {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
