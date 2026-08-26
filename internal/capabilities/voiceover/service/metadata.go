// Package voiceover — metadata.go (PR-VO-B2 metadata + StyleGroup
// restoration, June 2026; PR-VO-AUDIT-P02-P03 wrapper refactor, June 2026).
//
// Owns two production bodies:
//
//  1. mergeUserMetadata — meta-build bridge that injects
//     ResolvedDestination.StyleGroup (omitempty) and overlays
//     user-supplied metadata onto the meta map (dropping on
//     collision with a WARN log so callers learn about the drop).
//
//  2. resolveDestination — *Service thin WRAPPER over the canonical
//     ResolveVoiceoverDestination function (declared in
//     `destination_resolver.go`, June 2026 P0.2+P0.3 closure). The
//     wrapper exists only to read
//     `s.cfg.Drive.VoiceoverFolder()` nil-safe and forward it as
//     `defaultFolderID`. All routing semantics (KindExplicit /
//     KindGroup / KindAuto precedence, StyleGroup forwarding + MIRROR
//     on the KindGroup branch, nil-dest fallback) live in the
//     canonical function. See that file for the production behaviour
//     and the audit-mandated precedence rules.
//
// File-placement rationale (AGENTS.md Pattern 5): metadata.go owns
// metadata-layer concerns (mergeUserMetadata + the cfg→resolver
// bridge), stages.go owns the orchestrator entrypoint shells
// (GenerateBatch + 3 stage methods), and destination_resolver.go
// owns the destination routing decision surface. The split is
// per-capability-stable, not per-line-count.
//
// Identifier convention: the magic string "PR-VO-B2" surfaces in the
// WARN log message inside mergeUserMetadata so an operator can
// grep production logs to find dropped-user-meta events.
// PR-VO-AUDIT-P02-P03 markers surface in destination_resolver.go
// (canonical precedence + sentinel errors) so an operator can grep
// production logs to find central-fallback or Kind-routing events.
package voiceover

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"

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

// resolveDestination is the *Service method that the pre-PR8
// process.go called to convert a wire-shape DestinationRequest
// into an internal ResolvedDestination ready for the per-language
// orchestrator (processLanguage).
//
// Behaviour pinned by tests:
//
//  1. FORWARD: caller-supplied dest.Group + dest.StyleGroup land
//     in the asset.ResolveRequest the resolver sees
//     (Source = "voiceover" hardcoded).
//  2. MIRROR:  dest.StyleGroup is mirrored onto the returned
//     ResolvedDestination verbatim (resolver is a folder-
//     mapping layer; it does NOT echo StyleGroup back).
//  3. Empty:   empty StyleGroup propagates as zero through both
//     directions (forward + mirror).
//  4. Folder:  resolver's FolderID / FolderPath / DriveLink
//     pass through into the returned ResolvedDestination.
//
// PR-VO-AUDIT-P02-P03 (June 2026): Service.resolveDestination is now a
// thin wrapper over the canonical ResolveVoiceoverDestination function
// (declared in `destination_resolver.go`). The cfg.Drive.VoiceoverFolder()
// fallback is read nil-safe here and forwarded as defaultFolderID; the
// resolver itself never reads cfg, so a future composition root that
// supplies the default folder via a different channel (e.g. a typed
// RuntimeConfig surface, per Wave 5 #3) just needs to swap the
// service-side read without touching the canonical function.
//
// Behaviour pinned by tests (process_metadata_test.go +
// destination_resolver_test.go):
//
//  1. The legacy `ForwardsAndMirrorsStyleGroup` +
//     `StyleGroupEmpty_NoForwardOrMirror` pins continue to fire —
//     empty Kind → auto-detect → resolver call (Group is set in
//     those tests) → MIRROR verbatim. Back-compat preserved.
//  2. Nil cfg is tolerated (the wrapper reads s.cfg.Drive
//     nil-safe so test doubles and minimal Service{log:…} fixtures
//     keep compiling without spurious panics).
//  3. Nil dest is delegated to the resolver's nil-dest branch
//     which consults defaultFolderID (empty in tests = ErrMissingFolder).
func (s *Service) resolveDestination(
	ctx context.Context,
	dest *DestinationRequest,
) (*ResolvedDestination, error) {
	defaultFolderID := ""
	if s != nil && s.cfg != nil {
		defaultFolderID = s.cfg.Drive.VoiceoverFolder()
	}
	return ResolveVoiceoverDestination(ctx, dest, s.assetDestResolver, defaultFolderID)
}
