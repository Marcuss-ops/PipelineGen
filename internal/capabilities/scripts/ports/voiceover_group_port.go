// Package scripts — voiceover_group_port.go declares the canonical
// typed port that maps a `voiceover_group` topic name (e.g.
// "Jackie Chan") to its Drive folder ID under the configured
// voiceover root, plus the production adapter that wires the
// canonical *destination.Resolver implementation into the port.
//
// PR-VOICEOVER-GROUPSRESOLVER-RETIRE (P0 #15, deadline 2026-07-22):
// the legacy type-alias shim at
// internal/application/voiceover/groups_resolver.go is RETIRED
// (file physically git-rm'd). The canonical concrete is now
// destination.Resolver directly; this port adapter bridges it
// from the *destination.Resolver struct into the canonical
// ports.VoiceoverGroupResolver port interface (script pipeline
// use case depends solely on the port — never on the concrete).
//
// fix/voiceover-group-resolver (June 2026): the script pipeline use
// case now resolves item.Output.VoiceoverGroup to
// item.Output.VoiceoverFolderID BEFORE BuildPlan runs, so that
// downstream processors see plan.VoiceoverFolderID populated and
// switch from the default-folder fallback path to
// GenerateWithDestination(...) without the "voiceover_group set
// but folder missing → processor warns and falls back" bug.
//
// Layering note: per AGENTS.md Pattern 0 the port is declared
// structurally here (in internal/capabilities/scripts/ports) and
// the adapter wraps the concrete *destination.Resolver from the
// destination application. The script pipeline use case depends
// solely on the port interface, never on the concrete type —
// script producers no longer import voiceover OR destination
// directly.
package ports

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/destination"
)

// VoiceoverGroupResolver is the canonical port that the script
// pipeline consumes to resolve a `voiceover_group` topic name into
// a Drive folder ID.
//
// Production wiring is NewVoiceoverGroupsAdapter(resolver) which
// wraps *destination.Resolver; test wiring is a simple stub that
// returns canned folder IDs (the stub satisfies the port — it
// does NOT need to satisfy the *destination.Resolver concrete
// type, so the audit-2026-07-03 RESIDUE-test-stub concern is
// resolved at the migration seam).
//
// The port signature intentionally returns only the folder ID
// (string) so the script pipeline does not depend on the
// destination.Entry concrete type. parentID is the voiceover
// root folder ID under which groups live — typically supplied by
// the composition root from cfg.Drive.VoiceoverFolder().
type VoiceoverGroupResolver interface {
	ResolveGroup(ctx context.Context, parentID, name string) (folderID string, err error)
}

// ErrVoiceoverGroupNotFound is the canonical sentinel returned
// when the requested group name does not match any folder under
// the supplied parent ID.
//
// The use case swallows this sentinel as "fall through to default
// folder" (see usecase.ResolveVoiceoverFolderForItem) so the
// processor's existing warning path is preserved for callers that
// pass an unknown group name. The adapter MUST wrap the underlying
// *destination.Resolver "not found" error with this sentinel so
// the use case can match it without importing destination.
var ErrVoiceoverGroupNotFound = errors.New("voiceover group not found")

// voiceoverGroupsAdapter bridges *destination.Resolver into the
// VoiceoverGroupResolver port. The composition root constructs one
// of these once and injects it into GenerateOneUseCase via
// SetVoiceoverRouting. nil-safe: a nil receiver returns ("", nil)
// so callers can pass an adapter constructed without a backing
// resolver for tests.
type voiceoverGroupsAdapter struct {
	resolver *destination.Resolver
}

// NewVoiceoverGroupsAdapter constructs the production adapter. The
// resolver argument may be nil — ResolveGroup will be a no-op (returns
// empty folder + nil error) so test fixtures can pass nil without
// nil-checking at every call site.
func NewVoiceoverGroupsAdapter(resolver *destination.Resolver) VoiceoverGroupResolver {
	return &voiceoverGroupsAdapter{resolver: resolver}
}

// Compile-time assertion that the adapter satisfies the port.
var _ VoiceoverGroupResolver = (*voiceoverGroupsAdapter)(nil)

// ResolveGroup delegates to *destination.Resolver.ResolveByName
// and translates its "not found" sentinel into the canonical
// ports.ErrVoiceoverGroupNotFound so the use case can match
// without importing the destination package.
func (a *voiceoverGroupsAdapter) ResolveGroup(ctx context.Context, parentID, name string) (string, error) {
	if a == nil || a.resolver == nil {
		return "", nil
	}
	entry, err := a.resolver.ResolveByName(ctx, parentID, name)
	if err != nil {
		if errors.Is(err, destination.ErrNotFound) {
			return "", ErrVoiceoverGroupNotFound
		}
		return "", err
	}
	return entry.FolderID, nil
}
