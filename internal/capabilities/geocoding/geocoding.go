// Package geocoding defines the provider-neutral geocoding boundary used by
// PipelineGen. It intentionally contains no HTTP, cache, or provider policy:
// callers must explicitly opt into lookups, and platform adapters own the
// external service and its operating constraints.
package geocoding

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrNoResult means a grounded name is not specific enough for the configured
// provider to locate. Callers may safely leave that entity unlocated.
var ErrNoResult = errors.New("geocoding: no result")

// Cartographic levels. They name the scale a caller needs, so the lookup can
// refuse an answer the map could never use: a country-level map does not want
// a city coordinate. Empty means "no level": the historical level-agnostic
// lookup.
const (
	LevelContinent    = "continent"
	LevelCountry      = "country"
	LevelRegion       = "region"
	LevelMunicipality = "municipality"
)

// NormalizeLevel folds the payload and provider spellings onto the canonical
// levels. Empty, "auto" and "none" mean no constraint; an unknown level is an
// error rather than a silent fallback to an unfiltered lookup.
func NormalizeLevel(level string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "auto", "none":
		return "", nil
	case LevelContinent:
		return LevelContinent, nil
	case LevelCountry, "nation", "sovereign_state":
		return LevelCountry, nil
	case LevelRegion, "state", "province", "admin1":
		return LevelRegion, nil
	case LevelMunicipality, "city", "town", "comune", "settlement":
		return LevelMunicipality, nil
	default:
		return "", fmt.Errorf("geocoding: unsupported level %q", level)
	}
}

// CanonicalScope folds the provider vocabulary onto the four scopes a result
// can carry. Geocoders use city for municipalities and may use town/village/
// locality for the same level; keeping those spellings distinct made a valid
// city disappear from a municipality map.
func CanonicalScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "city", "town", "village", "hamlet", "locality", "municipality", "comune", "settlement":
		return "city"
	case "country", "nation", "sovereign_state":
		return "country"
	case "region", "state", "province", "admin1", "administrative":
		return "region"
	case "continent":
		return "continent"
	default:
		return strings.ToLower(strings.TrimSpace(scope))
	}
}

// scopesForLevel states which result scopes a cartographic level may pin. A
// region frame is drawn around its cities: the region grounds the camera, the
// cities name what is inside it. A level with no entry (empty, unknown) keeps
// every result.
func scopesForLevel(level string) map[string]bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case LevelContinent:
		return map[string]bool{"continent": true}
	case LevelCountry:
		return map[string]bool{"country": true}
	case LevelRegion:
		return map[string]bool{"region": true, "city": true}
	case LevelMunicipality:
		return map[string]bool{"city": true}
	default:
		return nil
	}
}

// LevelConstrains reports whether a level restricts which scopes are usable.
// An empty or unknown level does not.
func LevelConstrains(level string) bool {
	return scopesForLevel(level) != nil
}

// ConstraintLevel returns the level a provider lookup may enforce, or "" when
// the level must not constrain the search.
//
// Only a coarse frame excludes finer places: a country-level map cannot render
// a city, so the provider is asked for countries. A fine frame needs the
// coarser places as disambiguation context — a municipality map resolves
// "Parigi" inside the country the scene grounded — so erasing countries at the
// provider would delete the very frames the cities need. On those levels the
// planner's candidate filter stays the authority, as before.
func ConstraintLevel(level string) (string, error) {
	normalized, err := NormalizeLevel(level)
	if err != nil {
		return "", err
	}
	switch normalized {
	case LevelContinent, LevelCountry:
		return normalized, nil
	default:
		return "", nil
	}
}

// ScopeFitsLevel reports whether a result scope may be pinned on a map of the
// requested level. An empty level accepts everything. This is the single
// authority for the scope↔level rule: the provider applies it to every answer
// and the overlay planner applies it to every candidate, so a country can
// never land on a city plate in one path and not the other.
func ScopeFitsLevel(scope, level string) bool {
	allowed := scopesForLevel(level)
	if allowed == nil {
		return true
	}
	return allowed[CanonicalScope(scope)]
}

// Request is one grounded place-name lookup. Query must be a location entity
// from script annotations, never arbitrary narration or personal data.
type Request struct {
	Query    string
	Language string
	// Context carries the already-grounded geographic hints of the scene the
	// place was spoken in (never invented: only coordinates/names the run has
	// already certified). Providers use it to disambiguate homonyms; the same
	// name under two different contexts is two different lookups, not one
	// cached answer.
	Context Context
	// Level is the cartographic level the requesting map needs. A provider must
	// narrow the search to that level and answer ErrNoResult when the name is
	// only known at another scale, so a country-level map never pays for a city
	// lookup it could not render. Empty keeps the level-agnostic lookup.
	Level string
}

// Context is the grounded geographic frame of one lookup. Empty fields mean
// "no hint" and never constrain the result.
type Context struct {
	// Country names the country already grounded elsewhere in the scene
	// (entity name or ISO code).
	Country string `json:"country,omitempty"`
	// CountryCode is the ISO 3166-1 alpha-2 code (lowercase) of a place
	// already grounded in the scene. It disambiguates namesakes even when the
	// provider returns the country in a different language.
	CountryCode string `json:"country_code,omitempty"`
	// Region names the administrative region already grounded in the scene.
	Region string `json:"region,omitempty"`
}

// IsZero reports whether the context carries no hint at all.
func (c Context) IsZero() bool {
	return strings.TrimSpace(c.Country) == "" &&
		strings.TrimSpace(c.CountryCode) == "" &&
		strings.TrimSpace(c.Region) == ""
}

// Key is the canonical cache identity of the context. Two lookups share a
// cache entry only when their contexts match, so a name resolved inside one
// country can never be silently reused for a namesake elsewhere.
func (c Context) Key() string {
	key := strings.ToLower(strings.TrimSpace(c.Country)) + "\x1f" + strings.ToLower(strings.TrimSpace(c.Region))
	// Keep the pre-country-code key byte-identical for contexts that do not
	// carry a code, so existing durable caches stay addressable.
	if code := strings.ToLower(strings.TrimSpace(c.CountryCode)); code != "" {
		key += "\x1f" + code
	}
	return key
}

// Result is the WGS84 point returned by a geocoder. Coordinates use degrees.
// The selection identity fields (Provider, PlaceID, Score, Rationale) record
// WHY this answer was accepted, so a cached hit can be audited without
// re-asking the provider.
type Result struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	DisplayName string  `json:"display_name,omitempty"`
	Scope       string  `json:"scope,omitempty"`
	// Country and CountryCode are the provider's addressed country for this
	// hit. They let the scene adopt grounded country context from any resolved
	// place (a city included) and let a cached hit remain auditable.
	Country     string `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	// AdminRegion is the provider's first-level administrative region (for
	// example a US state). It lets later lookups disambiguate city namesakes
	// within the same country.
	AdminRegion string `json:"admin_region,omitempty"`
	// Provider identifies the adapter (and endpoint) that produced the hit.
	Provider string `json:"provider,omitempty"`
	// PlaceID is the provider's own stable identity for the place.
	PlaceID string `json:"place_id,omitempty"`
	// Score is the disambiguation confidence in [0,1]; below the adapter's
	// acceptance threshold the lookup is refused, not guessed.
	Score float64 `json:"score,omitempty"`
	// Rationale is the human-readable reason this candidate won (match kind,
	// context agreement, margin over the runner-up).
	Rationale string `json:"rationale,omitempty"`
}

// Validate rejects coordinates that cannot describe a WGS84 point. This is
// fail-closed because downstream Web Mercator placement is meaningless for
// invalid values.
func (r Result) Validate() error {
	if math.IsNaN(r.Latitude) || math.IsInf(r.Latitude, 0) || r.Latitude < -90 || r.Latitude > 90 {
		return fmt.Errorf("geocoding: latitude %v outside [-90,90]", r.Latitude)
	}
	if math.IsNaN(r.Longitude) || math.IsInf(r.Longitude, 0) || r.Longitude < -180 || r.Longitude > 180 {
		return fmt.Errorf("geocoding: longitude %v outside [-180,180]", r.Longitude)
	}
	return nil
}

// Geocoder resolves one grounded place query to WGS84. Implementations must
// honor provider rate limits and must not perform work when the caller has not
// explicitly enabled geocoding.
type Geocoder interface {
	Geocode(context.Context, Request) (Result, error)
}

// NormalizeRequest canonicalizes a lookup for caching and rejects empty
// query/language values. Language is optional and normalized to "en".
func NormalizeRequest(req Request) (Request, error) {
	req.Query = strings.Join(strings.Fields(strings.TrimSpace(req.Query)), " ")
	req.Language = strings.ToLower(strings.TrimSpace(req.Language))
	if req.Query == "" {
		return Request{}, fmt.Errorf("geocoding: query is required")
	}
	if req.Language == "" {
		req.Language = "en"
	}
	level, err := NormalizeLevel(req.Level)
	if err != nil {
		return Request{}, err
	}
	req.Level = level
	return req, nil
}
