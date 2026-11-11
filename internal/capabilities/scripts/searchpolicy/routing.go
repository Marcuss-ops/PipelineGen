// Package searchpolicy classifies semantic media queries into provider lanes.
// It is intentionally independent of the scripts workflow orchestration.
package searchpolicy

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
)

// Class is the provider-routing class of one search query.
type Class string

const (
	Person       Class = "person"
	Organization Class = "organization"
	Location     Class = "location"
	Object       Class = "object"
	Event        Class = "event"
	Generic      Class = "generic"
)

// ForIdentityKind maps the semantic engine's identity taxonomy onto routing
// classes. Unknown kinds remain generic so they cannot inherit an unsupported
// provider lane.
func ForIdentityKind(kind string) Class {
	switch entitytype.Fold(kind) {
	case entitytype.Person:
		return Person
	case entitytype.Org:
		return Organization
	case entitytype.Place:
		return Location
	case entitytype.Product, entitytype.WorkOfArt:
		return Object
	case entitytype.Event:
		return Event
	}
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "OBJECT", "ARTIFACT", "VEHICLE", "DEVICE":
		return Object
	}
	return Generic
}

// ProviderLane is the budget lane a class consumes.
func (c Class) ProviderLane() string {
	switch c {
	case Person:
		return "duckduckgo"
	case Organization, Location, Object:
		return "wikimedia_commons"
	default:
		return "generic"
	}
}

// RoutedToEditorialSource reports whether the class should consult an
// encyclopedic, license-bearing source before the broad web provider.
func (c Class) RoutedToEditorialSource() bool {
	switch c {
	case Organization, Location, Object:
		return true
	default:
		return false
	}
}

// FromQuery is the conservative fallback for queries without a canonical
// identity attached.
func FromQuery(query string) Class {
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(query), " ")) + " "
	for _, marker := range []string{
		" corte ", " procura ", " tribunale ", " cassazione ", " ministero ", " comune di ",
		" regione ", " provincia ", " università ", " universita ", " istituto ", " fondazione ",
		" ansa ", " polizia ", " carabinieri ", " guardia di finanza ",
	} {
		if strings.Contains(normalized, marker) {
			return Organization
		}
	}
	return Generic
}
