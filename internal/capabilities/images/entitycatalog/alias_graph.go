package entitycatalog

import (
	"errors"
	"sort"
	"strings"
	"sync"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

// ErrAmbiguousPersonName is returned when a PERSON surface is registered under
// more than one canonical identity and the caller supplied no disambiguator.
// The caller must fail closed: an ambiguous name is not selectable.
var ErrAmbiguousPersonName = errors.New("entity image catalog: ambiguous PERSON name")

// PersonAliasGraph is the alias dimension of the PERSON identity catalog. It
// maps every known surface (canonical name or nickname) to the ONE canonical
// entity id that owns it. A name may be registered under more than one id only
// when the identities are genuinely distinct; resolution then fails closed
// until the caller supplies the disambiguated id.
//
// The graph is intentionally separate from the image catalog rows: aliases are
// identity metadata, not candidate evidence. It is safe for concurrent use.
type PersonAliasGraph struct {
	mu      sync.RWMutex
	byAlias map[string]map[string]struct{}
	byID    map[string]map[string]struct{}
}

// NewPersonAliasGraph returns an empty graph.
func NewPersonAliasGraph() *PersonAliasGraph {
	return &PersonAliasGraph{
		byAlias: make(map[string]map[string]struct{}),
		byID:    make(map[string]map[string]struct{}),
	}
}

func normalizePersonAlias(value string) string {
	value = strings.TrimSpace(value)
	for _, suffix := range []string{"'s", "’s"} {
		if len(value) > len(suffix) && strings.EqualFold(value[len(value)-len(suffix):], suffix) {
			value = strings.TrimSpace(value[:len(value)-len(suffix)])
			break
		}
	}
	return capabilityentities.NormalizeName(value)
}

// Register attaches one or more alias surfaces to a canonical PERSON id.
// Registering the same alias under the same id is idempotent. Registering it
// under a different id is allowed and makes the surface ambiguous; it never
// silently overwrites the existing owner.
func (g *PersonAliasGraph) Register(canonicalID string, aliases ...string) error {
	if g == nil {
		return errors.New("entity image catalog: nil alias graph")
	}
	id := NormalizePersonEntityID(canonicalID)
	if id == "" {
		return errors.New("entity image catalog: alias graph requires a canonical PERSON entity id")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.byAlias == nil {
		g.byAlias = make(map[string]map[string]struct{})
	}
	if g.byID == nil {
		g.byID = make(map[string]map[string]struct{})
	}
	for _, alias := range aliases {
		key := normalizePersonAlias(alias)
		if key == "" {
			continue
		}
		if g.byAlias[key] == nil {
			g.byAlias[key] = make(map[string]struct{})
		}
		g.byAlias[key][id] = struct{}{}
		if g.byID[id] == nil {
			g.byID[id] = make(map[string]struct{})
		}
		g.byID[id][key] = struct{}{}
	}
	return nil
}

// Candidates returns the canonical ids that own one surface, sorted. Zero
// means the surface is unknown; one means a resolved identity; more than one
// means the caller must disambiguate or treat the name as unresolved.
func (g *PersonAliasGraph) Candidates(name string) []string {
	if g == nil {
		return nil
	}
	key := normalizePersonAlias(name)
	if key == "" {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	owners := g.byAlias[key]
	if len(owners) == 0 {
		return nil
	}
	out := make([]string, 0, len(owners))
	for id := range owners {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Aliases returns every surface registered for one canonical id, sorted.
func (g *PersonAliasGraph) Aliases(canonicalID string) []string {
	if g == nil {
		return nil
	}
	id := NormalizePersonEntityID(canonicalID)
	if id == "" {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	aliases := g.byID[id]
	if len(aliases) == 0 {
		return nil
	}
	out := make([]string, 0, len(aliases))
	for alias := range aliases {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out
}

// CanonicalizePersonNameWithAliases resolves a surface through the graph when
// one is supplied. An alias owned by one id returns that id; an alias owned by
// several ids returns ErrAmbiguousPersonName and no canonical id. Without a
// graph (or without a registered alias) the historical surface-based slug is
// derived unchanged.
func CanonicalizePersonNameWithAliases(name string, graph *PersonAliasGraph) (PersonIdentity, error) {
	canonicalName := strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	if canonicalName == "" {
		return PersonIdentity{}, errors.New("entity image catalog: PERSON name cannot produce a canonical entity id")
	}
	if graph != nil {
		candidates := graph.Candidates(canonicalName)
		if len(candidates) > 1 {
			return PersonIdentity{CanonicalName: canonicalName, Unresolved: true, Candidates: candidates}, ErrAmbiguousPersonName
		}
		if len(candidates) == 1 {
			return PersonIdentity{CanonicalEntityID: candidates[0], CanonicalName: canonicalName, Candidates: candidates}, nil
		}
	}
	canonicalID := capabilityentities.CanonicalEntityID(EntityTypePerson, canonicalName)
	if canonicalID == "" {
		return PersonIdentity{}, errors.New("entity image catalog: PERSON name cannot produce a canonical entity id")
	}
	return PersonIdentity{CanonicalEntityID: canonicalID, CanonicalName: canonicalName}, nil
}

// CanonicalizePersonIdentityWithAliases is CanonicalizePersonIdentity with an
// alias graph. A supplied id must be one of the graph's owners for the surface;
// this is how a caller explicitly selects one of several homonyms. Without a
// graph, a supplied id equal to the canonical slug or to its disambiguated
// "--suffix" form is accepted.
func CanonicalizePersonIdentityWithAliases(name, suppliedID string, graph *PersonAliasGraph) (PersonIdentity, error) {
	canonicalName := strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	if canonicalName == "" {
		return PersonIdentity{}, errors.New("entity image catalog: PERSON name cannot produce a canonical entity id")
	}
	supplied := NormalizePersonEntityID(suppliedID)
	if strings.TrimSpace(suppliedID) != "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(suppliedID)), "local:") && supplied == "" {
		return PersonIdentity{}, errors.New("entity image catalog: supplied PERSON entity id is invalid")
	}
	if supplied != "" && graph != nil {
		candidates := graph.Candidates(canonicalName)
		for _, candidate := range candidates {
			if candidate == supplied {
				return PersonIdentity{CanonicalEntityID: supplied, CanonicalName: canonicalName, Candidates: candidates}, nil
			}
		}
		if len(candidates) > 0 {
			return PersonIdentity{}, errors.New("entity image catalog: supplied PERSON entity id does not match any alias owner")
		}
	}
	identity, err := CanonicalizePersonNameWithAliases(canonicalName, graph)
	if err != nil {
		return PersonIdentity{}, err
	}
	if supplied != "" && supplied != identity.CanonicalEntityID {
		base := capabilityentities.CanonicalEntityID(EntityTypePerson, identity.CanonicalName)
		if strings.HasPrefix(supplied, base+"--") {
			return PersonIdentity{
				CanonicalEntityID: supplied,
				CanonicalName:     identity.CanonicalName,
				Disambiguator:     strings.TrimPrefix(supplied, base+"--"),
			}, nil
		}
		return PersonIdentity{}, errors.New("entity image catalog: supplied PERSON entity id does not match canonical name")
	}
	identity.Candidates = nil
	return identity, nil
}

// CanonicalPersonIDWithDisambiguator mints an identity that includes an
// explicit disambiguator. Two people who share a surface ("John Smith") get
// distinct stable ids ("person:john-smith--actor" vs
// "person:john-smith--director"); the base surface alone is never selectable
// for either when both are known.
func CanonicalPersonIDWithDisambiguator(name, disambiguator string) (PersonIdentity, error) {
	canonicalName := strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	base := capabilityentities.CanonicalEntityID(EntityTypePerson, canonicalName)
	if base == "" {
		return PersonIdentity{}, errors.New("entity image catalog: PERSON name cannot produce a canonical entity id")
	}
	disambiguator = strings.TrimSpace(disambiguator)
	if disambiguator == "" {
		return CanonicalizePersonNameWithAliases(canonicalName, nil)
	}
	suffix := capabilityentities.SafeEntityID(disambiguator)
	if suffix == "" {
		return PersonIdentity{}, errors.New("entity image catalog: PERSON disambiguator cannot produce a stable suffix")
	}
	return PersonIdentity{
		CanonicalEntityID: base + "--" + suffix,
		CanonicalName:     canonicalName,
		Disambiguator:     disambiguator,
	}, nil
}
