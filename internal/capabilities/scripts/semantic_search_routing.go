// Package scriptgeneration — semantic_search_routing.go owns the query-class
// projection of a semantic media composition.
//
// The retrieval problem is not "which provider is fastest" but "which provider
// can answer this query at all". The Garlasco live probe showed the shape: on
// Wikimedia Commons, institutions, places and generic objects return licensed
// assets, while proper names of people return nothing. Spending the Commons
// budget on a person is therefore not a miss, it is a wasted permit on a lane
// that cannot answer the question.
//
// The class is derived from the canonical identity kind the plan already
// carries, never re-guessed from the query text when an identity exists. A
// multi-identity composition is generic: one query covering two names cannot be
// attributed to a single class.
package scriptgeneration

import "strings"

// SemanticSearchClass is the provider-routing class of one search query.
type SemanticSearchClass string

const (
	// SearchClassPerson routes to people-oriented retrieval (DuckDuckGo in
	// the production cascade).
	SearchClassPerson SemanticSearchClass = "person"
	// SearchClassOrganization routes to encyclopedic/editorial retrieval
	// (Wikimedia Commons first).
	SearchClassOrganization SemanticSearchClass = "organization"
	// SearchClassLocation routes to encyclopedic/editorial retrieval.
	SearchClassLocation SemanticSearchClass = "location"
	// SearchClassObject routes to encyclopedic/editorial retrieval.
	SearchClassObject SemanticSearchClass = "object"
	// SearchClassEvent routes through the generic production cascade.
	SearchClassEvent SemanticSearchClass = "event"
	// SearchClassGeneric is the fallback: the query names no single typed
	// identity, so the canonical provider cascade owns the decision.
	SearchClassGeneric SemanticSearchClass = "generic"
)

// SearchClassForIdentityKind maps the semantic engine's identity taxonomy onto
// the routing classes. Unknown kinds are generic by design: a new kind must not
// silently inherit a lane whose provider cannot answer it.
func SearchClassForIdentityKind(kind string) SemanticSearchClass {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "PERSON":
		return SearchClassPerson
	case "ORG", "ORGANIZATION", "COMPANY", "INSTITUTION", "GOVERNMENT", "COURT", "AGENCY":
		return SearchClassOrganization
	case "LOCATION", "GPE", "PLACE", "CITY", "COUNTRY", "REGION", "ADDRESS", "FACILITY":
		return SearchClassLocation
	case "OBJECT", "PRODUCT", "WORK", "ARTIFACT", "VEHICLE", "DEVICE":
		return SearchClassObject
	case "EVENT":
		return SearchClassEvent
	default:
		return SearchClassGeneric
	}
}

// ProviderLane is the budget lane a class consumes. It is a stable string so a
// report can prove which budget a query charged.
func (c SemanticSearchClass) ProviderLane() string {
	switch c {
	case SearchClassPerson:
		return "duckduckgo"
	case SearchClassOrganization, SearchClassLocation, SearchClassObject:
		return "wikimedia_commons"
	default:
		return "generic"
	}
}

// RoutedToEditorialSource reports whether the class should consult an
// encyclopedic, license-bearing source before the broad web provider.
func (c SemanticSearchClass) RoutedToEditorialSource() bool {
	switch c {
	case SearchClassOrganization, SearchClassLocation, SearchClassObject:
		return true
	default:
		return false
	}
}

// SearchClassFromQuery is the fallback classifier for a query that has no
// canonical identity attached (for example a proposal with free-text visual
// suggestions). It is deliberately conservative: only clear institutional and
// place markers re-route, everything else stays generic so the canonical
// cascade keeps ownership.
func SearchClassFromQuery(query string) SemanticSearchClass {
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(query), " ")) + " "
	for _, marker := range []string{
		" corte ", " procura ", " tribunale ", " cassazione ", " ministero ", " comune di ",
		" regione ", " provincia ", " università ", " universita ", " istituto ", " fondazione ",
		" ansa ", " polizia ", " carabinieri ", " guardia di finanza ",
	} {
		if strings.Contains(normalized, marker) {
			return SearchClassOrganization
		}
	}
	return SearchClassGeneric
}
