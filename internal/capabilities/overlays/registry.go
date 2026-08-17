// Package overlays — registry.go is the canonical visual-capability registry
// for Chronon overlays. It is the SINGLE owner of the kind→renderer/template
// mapping: planners, the OverlayPlan compiler and the RenderingGen worker all
// resolve an overlay's semantic `kind` through ChrononOverlayRegistry instead
// of scattering switch statements (switch entity_card, switch lower_third,
// …) across the codebase.
//
//	OverlayPlan item.Kind
//	     ↓
//	ChrononOverlayRegistry.Resolve(kind)
//	     ↓
//	renderer / template (+ required_inputs, duration_policy,
//	                     positioning_policy, version)
//
// PipelineGen owns WHAT appears and WHEN (the OverlayPlan); Chronon owns the
// pixels. The registry is the declarative bridge: it names the renderer and
// template a kind compiles to, and the inputs/policies each kind requires, so
// the compiler never hard-codes a kind anywhere else.
package overlays

import (
	"fmt"
	"sort"
	"strings"
)

// OverlayKind is the canonical visual-capability discriminator. It is the
// semantic kind carried on OverlayItem.Kind (model.go) and is the registry
// lookup key.
type OverlayKind string

const (
	// KindEntityCard renders a person's name card (portrait optional).
	KindEntityCard OverlayKind = "entity_card"
	// KindOrganization renders an organization's name card.
	KindOrganization OverlayKind = "organization"
	// KindLocation renders a place/location name card.
	KindLocation OverlayKind = "location"
	// KindConcept renders a generic concept card (EVENT/DATE/unknown entity
	// types that are neither a person, organization nor place).
	KindConcept OverlayKind = "concept"
	// KindLowerThird renders a lower-third text banner.
	KindLowerThird OverlayKind = "lower_third"
	// KindImagePopup renders a contained image popup.
	KindImagePopup OverlayKind = "image_popup"
	// KindQuote renders a centered quote.
	KindQuote OverlayKind = "quote"
	// KindNumber renders a centered stat card (a spoken figure).
	KindNumber OverlayKind = "number"
	// KindProduct renders a product image popup (asset-driven).
	KindProduct OverlayKind = "product"
	// KindLogo renders a corner logo overlay (asset-driven).
	KindLogo OverlayKind = "logo"
)

// DurationPolicy declares how an overlay's on-screen duration is derived.
type DurationPolicy string

const (
	// DurationCertified bounds the overlay to the certified audio span of
	// the entity occurrence (entity cards: show the card exactly while the
	// name is spoken).
	DurationCertified DurationPolicy = "certified"
	// DurationBounded bounds the overlay to the plan's start_ms/end_ms
	// (scene-driven overlays: lower thirds, popups, quotes).
	DurationBounded DurationPolicy = "bounded"
)

// PositioningPolicy declares where an overlay is placed on the canvas.
type PositioningPolicy string

const (
	// PositionEntityCard uses the entity_card preset geometry.
	PositionEntityCard PositioningPolicy = "entity_card"
	// PositionLowerThird docks the overlay in the lower-third safe area.
	PositionLowerThird PositioningPolicy = "lower_third"
	// PositionPopup positions a contained image popup.
	PositionPopup PositioningPolicy = "popup"
	// PositionCentered centers the overlay on the canvas.
	PositionCentered PositioningPolicy = "centered"
	// PositionCorner docks the overlay in a canvas corner (logos).
	PositionCorner PositioningPolicy = "corner"
)

// OverlayEntry is one canonical visual-capability declaration. It is the
// complete contract a renderer needs to compile an OverlayItem of a given
// kind:
//
//   - Kind: the semantic kind (registry key).
//   - Template: the template id the item compiles to (OverlayItem.TemplateID
//     — e.g. "person_default").
//   - Renderer: the concrete renderer that materializes the template into
//     pixels (e.g. PersonCardRenderer).
//   - RequiredInputs: the item fields the renderer requires to produce a
//     valid layer (text, asset_refs, …).
//   - DurationPolicy: how the layer's duration is derived.
//   - PositioningPolicy: where the layer is placed.
//   - Version: the entry's own schema version.
type OverlayEntry struct {
	Kind              OverlayKind       `json:"kind"`
	Template          string            `json:"template"`
	Renderer          Renderer          `json:"renderer"`
	RequiredInputs    []string          `json:"required_inputs"`
	DurationPolicy    DurationPolicy    `json:"duration_policy"`
	PositioningPolicy PositioningPolicy `json:"positioning_policy"`
	Version           int               `json:"version"`
}

// ChrononOverlayRegistry is the immutable canonical registry. It is built
// once from the canonical table below and never mutated: there is one owner
// for the kind→renderer/template mapping, and ten scattered maps are
// forbidden by construction (no exported Register).
type ChrononOverlayRegistry struct {
	entries map[OverlayKind]OverlayEntry
}

// canonicalOverlayEntries is the single source of truth for the ten visual
// capabilities. Adding or removing a kind MUST update this table AND the
// frozen-entry-count test (registry_test.go) in lockstep.
var canonicalOverlayEntries = []OverlayEntry{
	{
		Kind:              KindEntityCard,
		Template:          "person_default",
		Renderer:          PersonCardRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationCertified,
		PositioningPolicy: PositionEntityCard,
		Version:           1,
	},
	{
		Kind:              KindOrganization,
		Template:          "org_default",
		Renderer:          OrganizationCardRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationCertified,
		PositioningPolicy: PositionEntityCard,
		Version:           1,
	},
	{
		Kind:              KindLocation,
		Template:          "gpe_default",
		Renderer:          LocationCardRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationCertified,
		PositioningPolicy: PositionEntityCard,
		Version:           1,
	},
	{
		Kind:              KindConcept,
		Template:          "concept_default",
		Renderer:          ConceptCardRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationCertified,
		PositioningPolicy: PositionEntityCard,
		Version:           1,
	},
	{
		Kind:              KindLowerThird,
		Template:          "lower_third",
		Renderer:          LowerThirdRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationBounded,
		PositioningPolicy: PositionLowerThird,
		Version:           1,
	},
	{
		Kind:              KindImagePopup,
		Template:          "image_popup",
		Renderer:          ImagePopupRenderer{},
		RequiredInputs:    []string{"asset_refs"},
		DurationPolicy:    DurationBounded,
		PositioningPolicy: PositionPopup,
		Version:           1,
	},
	{
		Kind:              KindQuote,
		Template:          "quote",
		Renderer:          QuoteRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationBounded,
		PositioningPolicy: PositionCentered,
		Version:           1,
	},
	{
		Kind:              KindNumber,
		Template:          "NUMBER",
		Renderer:          NumberRenderer{},
		RequiredInputs:    []string{"text"},
		DurationPolicy:    DurationCertified,
		PositioningPolicy: PositionCentered,
		Version:           1,
	},
	{
		Kind:              KindProduct,
		Template:          "PRODUCT",
		Renderer:          ProductRenderer{},
		RequiredInputs:    []string{"asset_refs"},
		DurationPolicy:    DurationBounded,
		PositioningPolicy: PositionPopup,
		Version:           1,
	},
	{
		Kind:              KindLogo,
		Template:          "LOGO",
		Renderer:          LogoRenderer{},
		RequiredInputs:    []string{"asset_refs"},
		DurationPolicy:    DurationBounded,
		PositioningPolicy: PositionCorner,
		Version:           1,
	},
}

// ErrUnknownOverlayKind is returned by Resolve when the kind is not part of
// the canonical visual-capability set.
var ErrUnknownOverlayKind = fmt.Errorf("overlay registry: unknown overlay kind")

// NewChrononOverlayRegistry builds the canonical registry from the frozen
// table. The returned registry is immutable and safe for concurrent reads.
func NewChrononOverlayRegistry() *ChrononOverlayRegistry {
	r := &ChrononOverlayRegistry{entries: make(map[OverlayKind]OverlayEntry, len(canonicalOverlayEntries))}
	for _, e := range canonicalOverlayEntries {
		r.entries[e.Kind] = e
	}
	return r
}

// DefaultChrononOverlayRegistry is the process-wide canonical registry.
// Planners and the entity resolver resolve through this single instance so
// every call site observes the same frozen kind→renderer/template mapping.
var DefaultChrononOverlayRegistry = NewChrononOverlayRegistry()

// Resolve maps a semantic kind to its canonical entry. It accepts the raw
// OverlayItem.Kind string and normalizes it (trim + lowercase) so planners
// are free of formatting constraints. Unknown kinds return
// ErrUnknownOverlayKind with the available kinds in the message.
func (r *ChrononOverlayRegistry) Resolve(kind string) (OverlayEntry, error) {
	key := OverlayKind(strings.ToLower(strings.TrimSpace(kind)))
	if r == nil {
		return OverlayEntry{}, fmt.Errorf("%w: %q (registry is nil)", ErrUnknownOverlayKind, kind)
	}
	e, ok := r.entries[key]
	if !ok {
		return OverlayEntry{}, fmt.Errorf("%w: %q (available: %s)", ErrUnknownOverlayKind, kind, r.AvailableKinds())
	}
	return e, nil
}

// ResolveTemplate returns only the template id for a kind. It is the
// convenience spelling for planners that only need the TemplateID.
func (r *ChrononOverlayRegistry) ResolveTemplate(kind string) (string, error) {
	e, err := r.Resolve(kind)
	if err != nil {
		return "", err
	}
	return e.Template, nil
}

// ResolveRenderer returns the concrete renderer for a kind.
func (r *ChrononOverlayRegistry) ResolveRenderer(kind string) (Renderer, error) {
	e, err := r.Resolve(kind)
	if err != nil {
		return nil, err
	}
	return e.Renderer, nil
}

// Has reports whether a kind is registered (normalized lookup, nil-safe).
func (r *ChrononOverlayRegistry) Has(kind string) bool {
	if r == nil {
		return false
	}
	_, ok := r.entries[OverlayKind(strings.ToLower(strings.TrimSpace(kind)))]
	return ok
}

// Kinds returns the registered kinds in deterministic (sorted) order.
func (r *ChrononOverlayRegistry) Kinds() []OverlayKind {
	if r == nil {
		return nil
	}
	out := make([]OverlayKind, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Len returns the number of registered kinds.
func (r *ChrononOverlayRegistry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

// AvailableKinds returns a sorted, comma-separated list of registered kinds
// for error messages.
func (r *ChrononOverlayRegistry) AvailableKinds() string {
	if r == nil {
		return "(none)"
	}
	kinds := r.Kinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return strings.Join(out, ", ")
}
