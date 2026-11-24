// Package scriptgeneration defines the pure domain model for the
// durable multi-stage script-generation workflow. It contains
// ZERO I/O, ZERO external dependencies — only types and pure
// functions.
//
// Architecture (per the verdetto):
//
//	internal/scriptgeneration/
//	    model.go        — GenerateRequest, Scene, GenerateResult (aggregates)
//	    model_values.go — Language, Source, Clip/Audio references
//	    model_render.go — RenderReference, RenderArtifact, audio projections
//	    ports*.go       — Technology-independent interfaces
//	    service.go      — Linear orchestrator (Start)
//	    runner*.go      — Durable stage-based execution with checkpoint
//
// The HTTP layer (internal/capabilities/script) calls service.Start; the
// runner owns the durable background execution. No I/O happens
// inside the builder (ingress registry) — the builder is demoted
//
// model.go was split three ways on 2026-09-12 (values | render | aggregates)
// to keep every file under max_lines_per_file_strict=600 (godlike/08).
package modelvalues

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Domain aggregates ───────────────────────────────────────────────

// FixedSceneKind maps a protected fixed-media scene onto the domain SceneKind
// from its explicit Role. The scene ID is identity only — it never selects the
// section kind. Opening/closing roles produce the intro/outro sections; a
// legacy fixed scene without a role degrades to the generic fixed clip
// treatment.
func FixedSceneKind(scene contract.Scene) scriptpkg.SceneKind {
	switch scene.Role.Normalize() {
	case scriptpkg.SceneRoleOpening:
		return scriptpkg.SceneIntro
	case scriptpkg.SceneRoleClosing:
		return scriptpkg.SceneOutro
	default:
		return scriptpkg.SceneClip
	}
}

func CloneFixedPlayback(in *scriptpkg.FixedPlaybackPolicy) *scriptpkg.FixedPlaybackPolicy {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
