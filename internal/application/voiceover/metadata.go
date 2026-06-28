// Package voiceover — metadata.go (PR-VO-B2 metadata + StyleGroup
// restoration, June 2026).
//
// Owns the two PR-VO-B2 production bodies that the pre-PR8 voiceover
// code contained before the PR8 slim-orchestrator extraction stripped
// them along with the rest of the 695-line pre-PR8 process.go:
//
//   1. mergeUserMetadata — meta-build bridge that injects
//      ResolvedDestination.StyleGroup (omitempty) and overlays
//      user-supplied metadata onto the meta map (dropping on
//      collision with a WARN log so callers learn about the drop).
//
//   2. resolveDestination — *Service method that wraps
//      assetDestResolver.Resolve(ctx, &asset.ResolveRequest{...}) with
//      the canonical voiceover forwarding pattern: StyleGroup is
//      passed FORWARD into the resolver's ResolveRequest and then
//      mirrored BACK into ResolvedDestination verbatim, because the
//      resolver is a folder-mapping layer (it does not own
//      StyleGroup routing).
//
// File-placement rationale (AGENTS.md Pattern 5): metadata.go owns
// PR-VO-B2's metadata-layer concerns; stages.go stays a stub layer
// for the orchestrator entrypoint shells (GenerateBatch + 3 stage
// methods). The split is per-capability-stable, not per-line-count.
//
// Identifier convention: the magic string "PR-VO-B2" surfaces in the
// WARN log message inside mergeUserMetadata so an operator can
// grep production logs to find dropped-user-meta events.
package voiceover

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// mergeUserMetadata is the meta-build bridge between synthesize
// (Stage 1) and destination (Stage 2) in process.go. The production
// body is REQUIRED (not stubbed) because process_metadata_test.go
// pins 4 contract cases via the test suite.
//
// Behaviour pinned by tests:
//
//   1. No-collision: every userMeta key lands in meta unchanged.
//   2. Collision:    a userMeta key colliding with an existing meta
//                    key is DROPPED (core value wins) and a WARN log
//                    line is emitted.
//   3. Style inject: when dest.StyleGroup is non-empty, the meta
//                    map gains a "style_group" entry.
//   4. Style omit:   when dest.StyleGroup is empty, no
//                    "style_group" entry is created (omitempty
//                    contract so empty StyleGroup does not shadow
//                    real defaults downstream).
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
	if dest != nil && dest.StyleGroup != "" {
		meta["style_group"] = dest.StyleGroup
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

// resolveDestination is the *Service method that the pre-PR8
// process.go called to convert a wire-shape DestinationRequest
// into an internal ResolvedDestination ready for the per-language
// orchestrator (processLanguage).
//
// Behaviour pinned by tests:
//
//   1. FORWARD: caller-supplied dest.Group + dest.StyleGroup land
//               in the asset.ResolveRequest the resolver sees
//               (Source = "voiceover" hardcoded).
//   2. MIRROR:  dest.StyleGroup is mirrored onto the returned
//               ResolvedDestination verbatim (resolver is a folder-
//               mapping layer; it does NOT echo StyleGroup back).
//   3. Empty:   empty StyleGroup propagates as zero through both
//               directions (forward + mirror).
//   4. Folder:  resolver's FolderID / FolderPath / DriveLink
//               pass through into the returned ResolvedDestination.
//
// Defensive early-outs: nil dest and nil assetDestResolver return
// errors so the caller can surface a 400/500 instead of panicking
// at the resolver call site.
func (s *Service) resolveDestination(
	ctx context.Context,
	dest *DestinationRequest,
) (*ResolvedDestination, error) {
	if dest == nil {
		return nil, fmt.Errorf("resolveDestination: nil DestinationRequest")
	}
	if s.assetDestResolver == nil {
		return nil, fmt.Errorf("resolveDestination: nil assetDestResolver (composition root did not wire asset.Resolver)")
	}

	// FORWARD: build the resolver request with the canonical voiceover
	// Source marker + StyleGroup pass-through.
	req := &asset.ResolveRequest{
		Source:     "voiceover",
		Group:      dest.Group,
		StyleGroup: dest.StyleGroup,
	}

	result, err := s.assetDestResolver.Resolve(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolveDestination: resolver failed: %w", err)
	}
	if result == nil {
		// Defensive: a misbehaving resolver might return (nil, nil).
		// Substitute an empty result so the rest of the merge can
		// proceed with zero-valued folder fields.
		result = &asset.ResolveResult{}
	}

	// MIRROR + Folder field pass-through.
	return &ResolvedDestination{
		Group:      dest.Group,
		FolderID:   result.FolderID,
		FolderPath: result.FolderPath,
		DriveLink:  result.DriveLink,
		StyleGroup: dest.StyleGroup, // verbatim mirror (NOT from resolver result)
	}, nil
}
