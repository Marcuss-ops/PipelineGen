// Package mediacert is the semantic certification capability for VidRush
// pipeline results. It is the fail-closed gate that decides whether a
// technically-successful run is SEMANTICALLY correct.
//
// Fase 2 of the VidRush semantic-correctness plan. It is built immediately
// after SceneIR (Fase 1) so every later stage can be modified against an
// automatic pass/fail verdict instead of a count-only check that declared
// success at a semantically broken pipeline.
//
// A count-only test passes a pipeline that produced 15 entities but
// assigned them to the wrong scenes (wrong_segment_entities = 5/5 FAIL).
// MediaCert instead checks, per segment:
//
//   - SCENE IDENTITY          — canonical segment_id preserved (no
//     mediterranean-* → scene-N rewrite).
//   - SOURCE IMMUTABILITY     — source_text + source_text_hash unchanged
//     after compilation (SceneIR tamper check).
//   - SEMANTIC PROFILES       — every segment has a non-null visual profile
//     (subject + ≥1 visual term).
//   - ENTITY GROUNDING        — every entity has source evidence
//     (NO EVIDENCE → NO ENTITY).
//   - QUERY OWNERSHIP         — every query's owner_segment_id matches the
//     segment that emitted it (no cross-scene query drift).
//   - ASSET OWNERSHIP         — every selected asset's provenance segment
//     matches the segment it is bound to.
//   - ARTLIST RELEVANCE       — the winner candidate's subject matches the
//     segment subject (boxing REJECTED for Greek Salad).
//   - CROSS-SCENE REUSE       — when the spec forbids reuse, no asset is
//     bound to two segments.
//   - IMAGE FANOUT            — one image query per entity, three images
//     per scene when the spec demands it.
//   - PROVIDER POLICY         — only spec-allowed providers are used.
//
// The canonical CLI is:
//
//	mediacert verify result.json spec.json
//
// and the canonical Make target is `make verify-vidrush-semantic`, which
// must print the human-readable report and exit non-zero when
// CERTIFIED=false.
//
// This package depends only on internal/kernel/sceneir +
// internal/kernel/script + stdlib. No transport, no SQL, no logger.
package mediacert
