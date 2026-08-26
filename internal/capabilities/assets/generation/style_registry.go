// Package generation — back-compat shim (FASE 8 image-territories, July 2026).
//
// The real StyleRegistry implementation was moved to
// internal/capabilities/images/workflow/styles/. This file provides Go type aliases
// so existing consumers of *generation.StyleRegistry, generation.StyleResolver,
// and generation.ResolvedStyle continue to compile unchanged.
//
// Per AGENTS.md godlike/07 "no fake availability": the aliases are Go type
// aliases (not wrapper structs), so method sets and identity are transparent.
// NewStyleRegistry delegates to styles.NewStyleRegistry — the shim is a
// pass-through, not a reimplementation.
//
// Go type aliases are declared with `=` (e.g. `type StyleRegistry = styles.StyleRegistry`).
// This means:
//   - generation.StyleRegistry IS styles.StyleRegistry — same type, same methods.
//   - Struct literals like `generation.ResolvedStyle{ID: "x"}` compile identically.
//   - Sentinel errors (`generation.ErrStyleNotFound`) are the same pointer as
//     styles.ErrStyleNotFound, so `errors.Is(err, generation.ErrStyleNotFound)` works.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
// the underlying StyleDefinition canonically lives at
// internal/kernel/asset/types_style.go (slim 8-field shape). The 3-level
// alias chain (image/styles.StyleDefinition = asset.GenerationStyle =
// asset.StyleDefinition) collapses to a single type identity at compile
// time, so existing consumers in this shim continue to work unchanged.
package generation

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"

// ── Type aliases (transparent — same type, same method set) ─────────────

// StyleRegistry is the canonical YAML-backed style registry.
// See internal/capabilities/images/ for the implementation.
type StyleRegistry = images.StyleRegistry

// ResolvedStyle is the output of StyleResolver.Resolve.
//
// Step-1 typed migration (A1, July 2026): Width/Height were dropped
// from ResolvedStyle (caller-supplied dimensions through the image
// generation request). Existing call sites that read ResolvedStyle.W/H
// must migrate.
type ResolvedStyle = images.ResolvedStyle

// StyleResolver resolves a style ID into validated generation parameters.
type StyleResolver = images.StyleResolver

// ── Sentinel errors (same pointer as images/) ──────────────────────────

var (
	ErrStyleNotFound            = images.ErrStyleNotFound
	ErrStyleProviderUnsupported = images.ErrStyleProviderUnsupported
	ErrStyleModelUnsupported    = images.ErrStyleModelUnsupported
	ErrStyleDisabled            = images.ErrStyleDisabled
)

// ── Constructor ────────────────────────────────────────────────────────

// NewStyleRegistry creates a new registry and loads styles from the given
// YAML file. Delegates to images.NewStyleRegistry.
func NewStyleRegistry(yamlPath string) (*StyleRegistry, error) {
	return images.NewStyleRegistry(yamlPath)
}
