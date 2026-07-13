// Package job — codec.go alias layer (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
//
// The canonical body-bearing Codec interfaces (PayloadCodec +
// ResultCodec) now live in internal/kernel/job/codec.go (the
// kernel subzone is the SOLE owner of cross-cutting contracts per
// godlike/06 SSOT).
//
// This file is a back-compat alias layer preserving the in-tree
// reference sites that declared `type PC = domainjob.PayloadCodec`
// or referenced `domainjob.ResultCodec.EncodeResult(...)`
// directly. Go type aliases are transparent: `domainjob.PayloadCodec`
// and `kerneljob.PayloadCodec` are the same interface as far as the
// compiler and runtime are concerned.
//
// Future code SHOULD import internal/kernel/job directly. The
// aliases here are scheduled for cutover in the CONTRACT phase
// (deadline 2026-10-01).
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2 → commit 9) ──────────

type (
	// PayloadCodec is the canonical typed encode/decode surface
	// for a JobDefinition's INPUT payload. Embeds CodecDescriptor
	// (marker) + EncodePayload / DecodePayload bodies.
	PayloadCodec = kerneljob.PayloadCodec

	// ResultCodec is the canonical typed encode/decode surface
	// for a JobDefinition's OUTPUT result. Embeds CodecDescriptor
	// (marker) + EncodeResult / DecodeResult bodies.
	ResultCodec = kerneljob.ResultCodec
)
