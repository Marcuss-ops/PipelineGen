// Package dto holds pure-data transfer shapes that cross the
// scripts-package boundary: enqueue payloads, generation-result
// envelopes, asset-search DTOs, clip-suggestion shapes, scene
// images, Drive-folder-suggestion shapes, etc.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): flow_helpers.go's stub
// types (RealtimeMatchAsset, AssociationCandidatesRequest, Script*Suggestion
// shapes) + generation_*.go result envelopes + SceneVoiceover /
// SceneImage / VideoMetadata request DTOs all migrate here.
//
// Import discipline:
//   - MUST be import-cycle-safe: this package MUST NOT import any
//     other scripts/ sub-package (consumers depend on us, never
//     the reverse).
//   - MAY import internal/kernel/* (canonical domain types).
//   - All exported types are JSON-tagged where applicable.
package dto
