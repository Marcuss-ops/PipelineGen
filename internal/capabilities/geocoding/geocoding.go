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
}

// Context is the grounded geographic frame of one lookup. Empty fields mean
// "no hint" and never constrain the result.
type Context struct {
	// Country names the country already grounded elsewhere in the scene
	// (entity name or ISO code).
	Country string `json:"country,omitempty"`
	// Region names the administrative region already grounded in the scene.
	Region string `json:"region,omitempty"`
}

// IsZero reports whether the context carries no hint at all.
func (c Context) IsZero() bool {
	return strings.TrimSpace(c.Country) == "" && strings.TrimSpace(c.Region) == ""
}

// Key is the canonical cache identity of the context. Two lookups share a
// cache entry only when their contexts match, so a name resolved inside one
// country can never be silently reused for a namesake elsewhere.
func (c Context) Key() string {
	return strings.ToLower(strings.TrimSpace(c.Country)) + "\x1f" + strings.ToLower(strings.TrimSpace(c.Region))
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
	return req, nil
}
