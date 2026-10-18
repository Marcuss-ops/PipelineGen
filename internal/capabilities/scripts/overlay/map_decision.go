// Package overlay — map_decision.go owns the Map Decision Report: the ONE
// per-map record of why a map came out the way it did. Places recognized,
// places actually geocoded, exclusions with reasons, the selected composition,
// the seed, the backend, any declared fallback and the phase timings all live
// here, so a missing or unexpected map is explainable without chasing logs
// across three repositories.
package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MapDecisionReportSchema identifies the report document version.
const MapDecisionReportSchema = "pipelinegen.map-decision.v1"

// MapDecisionPlace is one grounded place as the report sees it. State is the
// lifecycle: eligible (passed admission) -> scheduled (has spoken timing
// inside the map window) -> rendered (renderer confirmed frames). Presented is
// retained for compatibility and is true only when State==rendered; never set
// it before the renderer confirms, so diagnostics cannot claim a pin was
// shown that still had to pass the renderer.
type MapDecisionPlace struct {
	EntityID   string  `json:"entity_id"`
	Label      string  `json:"label"`
	Scope      string  `json:"scope,omitempty"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Geocoded   bool    `json:"geocoded"`
	StartUS    int64   `json:"start_us,omitempty"`
	DurationUS int64   `json:"duration_us,omitempty"`
	State      string  `json:"state,omitempty"`
	Presented  bool    `json:"presented"`
}

// MapDecisionExclusion records one recognized place (or candidate) that did
// NOT become a pin, with the machine-readable reason and human detail.
type MapDecisionExclusion struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// MapDecisionReport is the complete decision trail of ONE map item.
type MapDecisionReport struct {
	Schema            string                 `json:"schema"`
	PlanID            string                 `json:"plan_id,omitempty"`
	ItemID            string                 `json:"item_id,omitempty"`
	Seed              string                 `json:"seed,omitempty"`
	Backend           string                 `json:"backend,omitempty"`
	BackendReason     string                 `json:"backend_reason,omitempty"`
	Requested         string                 `json:"requested,omitempty"`
	Composition       string                 `json:"composition,omitempty"`
	CompositionFamily string                 `json:"composition_family,omitempty"`
	Fallback          string                 `json:"fallback,omitempty"`
	CameraAnimation   string                 `json:"camera_animation,omitempty"`
	LabelAnimation    string                 `json:"label_animation,omitempty"`
	Basemap           string                 `json:"basemap,omitempty"`
	MapGroup          map[string]any         `json:"map_group,omitempty"`
	Strategy          string                 `json:"strategy,omitempty"`
	Places            []MapDecisionPlace     `json:"places,omitempty"`
	Exclusions        []MapDecisionExclusion `json:"exclusions,omitempty"`
	PhaseSeconds      map[string]float64     `json:"phase_seconds,omitempty"`
}

// NewMapDecisionReport starts the report for one map item.
func NewMapDecisionReport(planID, itemID, seed string) *MapDecisionReport {
	return &MapDecisionReport{
		Schema: MapDecisionReportSchema,
		PlanID: planID,
		ItemID: itemID,
		Seed:   seed,
	}
}

// AddExclusion records why a recognized place did not become a pin.
func (r *MapDecisionReport) AddExclusion(reason, detail string) {
	if r == nil {
		return
	}
	r.Exclusions = append(r.Exclusions, MapDecisionExclusion{Reason: reason, Detail: detail})
}

// RecordPhase stores one measured phase duration in seconds.
func (r *MapDecisionReport) RecordPhase(name string, seconds float64) {
	if r == nil {
		return
	}
	if r.PhaseSeconds == nil {
		r.PhaseSeconds = map[string]float64{}
	}
	r.PhaseSeconds[name] = seconds
}

// JSON encodes the report in its canonical, sorted-key form.
func (r *MapDecisionReport) JSON() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("map decision report is nil")
	}
	if strings.TrimSpace(r.Schema) == "" {
		r.Schema = MapDecisionReportSchema
	}
	return json.Marshal(r)
}

// WriteMapDecisionReport persists the report as <dir>/<item>.map-decision.json.
// The caller owns the directory lifecycle; the write is atomic.
func WriteMapDecisionReport(dir string, r *MapDecisionReport) (string, error) {
	if r == nil {
		return "", fmt.Errorf("map decision report is nil")
	}
	name := strings.TrimSpace(r.ItemID)
	if name == "" {
		name = "map"
	}
	encoded, err := r.JSON()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create map decision report directory: %w", err)
	}
	path := filepath.Join(dir, name+".map-decision.json")
	tmp, err := os.CreateTemp(dir, ".map-decision-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create map decision report temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(encoded, '\n')); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write map decision report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close map decision report: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("promote map decision report: %w", err)
	}
	return path, nil
}
