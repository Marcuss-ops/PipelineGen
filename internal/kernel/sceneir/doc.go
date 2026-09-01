// Package sceneir owns the SceneIR semantic compiler: the immutable
// intermediate representation produced from a canonical VidRush segment
// before any provider (Artlist, images, stock) builds retrieval queries.
//
// SceneIR resolves the deepest contamination bug observed in the VidRush
// LIVE test: the original segment is rewritten by the creative LLM, loses
// its canonical ID (mediterranean-* → scene-N) and the generated narration
// text leaks into the downstream query builders. The single rule this
// package enforces is therefore:
//
//	SOURCE IDENTITY IS IMMUTABLE
//
// The LLM may rewrite:
//
//	NarrationText
//
// It may NEVER touch:
//
//	SegmentID
//	Position
//	SourceText
//	SourceTextHash
//
// Artlist, image and stock query builders MUST consume SourceText +
// SemanticProfile, never NarrationText. This package is the boundary that
// makes that separation physical: SceneIR carries both surfaces and exposes
// distinct read APIs for each.
//
// Chain position (see Fase 1 of the VidRush semantic-correctness plan):
//
//	SceneIR
//	   ↓
//	EntityExtractor
//	   ↓
//	QueryPlanner
//	   ↓
//	MediaResolver
//	   ↓
//	MediaSampler
//	   ↓
//	Bindings
//	   ↓
//	MediaCert
//
// This package depends only on internal/kernel/script (the canonical
// segment identity boundary) and stdlib. No transport, no SQL, no logger.
package sceneir
