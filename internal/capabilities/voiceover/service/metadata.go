// Package voiceover — metadata.go owns the metadata bridge used while
// building voiceover artifacts. Destination selection is centralized in
// destination_resolver.go; the unused legacy Service wrapper was retired.
package voiceover

import (
	"go.uber.org/zap"
)

// mergeUserMetadata is the meta-build bridge between synthesize
// (Stage 1) and destination (Stage 2) in process.go. The production
// body is REQUIRED (not stubbed) because process_metadata_test.go
// pins 4 contract cases via the test suite.
//
// Behaviour pinned by tests:
//
//  1. No-collision: every userMeta key lands in meta unchanged.
//  2. Collision:    a userMeta key colliding with an existing meta
//     key is DROPPED (core value wins) and a WARN log
//     line is emitted.
//  3. Style inject: when dest.StyleGroup is non-empty, the meta
//     map gains a "style_group" entry.
//  4. Style omit:   when dest.StyleGroup is empty, no
//     "style_group" entry is created (omitempty
//     contract so empty StyleGroup does not shadow
//     real defaults downstream).
//
// Step ordering matters: StyleGroup injection happens before the
// userMeta overlay so a caller who supplies their own "style_group"
// key in userMeta would collide with the injection (core-wins
// semantics; consistent with collision pin #2).
func mergeUserMetadata(
	meta map[string]any,
	dest *ResolvedDestination,
	userMeta map[string]any,
	log *zap.Logger,
) {
	if meta == nil {
		// Defensive: production callers must pre-allocate meta, but
		// ignoring a nil meta map silently would let caller bugs
		// flow through. Fail-closed by no-op (the test suite always
		// pre-allocates meta; this branch is for direct callers).
		return
	}

	// Step 1: StyleGroup injection (omitempty contract).
	//
	// PR-VO-TYPED-PRIMITIVES (July 2026): dest.StyleGroup is the
	// typed StyleGroup envelope; the IsEmpty() predicate is the
	// typed-envelope equivalent of the pre-refactor `!= ""` check.
	if dest != nil && !dest.StyleGroup.IsEmpty() {
		meta["style_group"] = string(dest.StyleGroup)
	}

	// Step 2: userMeta overlay, with collision-drop semantics.
	// We treat "existing meta keys" as the canonical core set
	// rather than maintaining a separate hardcoded list — this
	// couples the collision rule to whichever keys process.go
	// has already populated (text_hash, text_preview, language,
	// voice, strategy, request_id, cleaned_path, semantic_*).
	// A user key that collides with ANY of those loses.
	for k, v := range userMeta {
		if _, exists := meta[k]; exists {
			if log != nil {
				log.Warn("PR-VO-B2: user metadata key colliding with core key; dropping user value",
					zap.String("key", k))
			}
			continue
		}
		meta[k] = v
	}
}
