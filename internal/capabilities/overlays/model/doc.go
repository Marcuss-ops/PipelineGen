// Package model owns the overlay wire model: the OverlayPlan/OverlayItem
// types, their validation invariants, the overlay-kind registry, the entity
// intent vocabulary, the media contract, the chain segment shape, the render
// key fingerprint and the animation reference admission rules.
//
// It is the leaf of the overlay stack: it may import capabilities/animreg and
// kernel leaves, but the selection/planning code in package overlays depends
// ON it and never the reverse.
package model
