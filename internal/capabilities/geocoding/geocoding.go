// Package geocoding defines the provider-neutral geocoding boundary used by
// PipelineGen. It intentionally contains no HTTP, cache, or provider policy:
// callers must explicitly opt into lookups, and platform adapters own the
// external service and its operating constraints.
package geocoding

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Request is one grounded place-name lookup. Query must be a location entity
// from script annotations, never arbitrary narration or personal data.
type Request struct {
	Query    string
	Language string
}

// Result is the WGS84 point returned by a geocoder. Coordinates use degrees.
type Result struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	DisplayName string  `json:"display_name,omitempty"`
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
