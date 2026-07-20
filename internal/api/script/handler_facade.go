// Package script — handler_facade.go extracts the domain facade
// cluster (GetVoiceoverService + GetGroupsResolver + ResolveDriveFolderID
// + MaybeCreateGoogleDoc) from ScriptFlowHandler (22-field God Object)
// into a dedicated FacadeHandler per
// architecture/current.yaml#SCRIPT-FLOW-SPLIT.linked_issues[PR-SCRIPT-FACADE-EXTRACT].
//
// Pattern 5 (AGENTS.md): one capability per file, one struct per
// capability. FacadeHandler owns the 4 narrow typed accessors that
// other orchestrators / cross-package callers consume via thin
// delegators on ScriptFlowHandler.
//
//   - GetVoiceoverService: returns the wired voiceover service
//     (cross-handler read).
//   - GetGroupsResolver: returns the script-side groups resolver
//     (asset-tree-backed folder group picker).
//   - ResolveDriveFolderID: resolves the Drive folder ID for a
//     script generation request (50-line body — see verbatim body
//     below; pre-extraction lowercase helper on ScriptFlowHandler
//     retired as dead code post-extraction per godlike/07).
//
// Sprint 1.0 (July 2026): MaybeCreateGoogleDoc facade method is
// RETIRED. Inline document creation is decommissioned; document
// generation is produced by the canonical downstream document.generate
// job (internal/application/document/usecase.go).
//
// ScriptFlowHandler retains thin delegator methods so existing
// cross-package call sites (h.GetVoiceoverService() etc.) compile
// unchanged — godlike/07 minimum-blast-radius. Auth-related
// methods (EnableAuth + AdminToken) STAY on ScriptFlowHandler
// because the canonical middleware.RequireAdminToken path requires
// *ScriptFlowHandler to satisfy AdminTokenProvider structurally
// — the compile-time assertion in middleware_auth.go
// (`var _ AdminTokenProvider = (*ScriptFlowHandler)(nil)`) locks
// that contract.

package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

// FacadeHandler owns the script-side domain-facade primitives.
// It is a separate type from ScriptFlowHandler (22-field struct) per
// AGENTS.md Pattern 5: one capability per file, one struct per
// capability. godlike/06 SSOT (one canonical owner per fact):
// FacadeHandler is the SOLE owner of the 4 methods declared here —
// nothing else.
//
// Fields:
//   - voService: pre-built voiceover service (cross-handler access).
//   - groupsResolver: script-side resolver (asset-tree-backed).
//   - publisher: canonical delivery.Publisher for Drive folder resolution
//     (replaces the legacy DriveFolderClient per FASE A5, July 2026).
//   - log: structured logger (used by ResolveDriveFolderID's
//     nil-tolerance warning path). Sprint 1.0 (July 2026): the
//     documentCreator facade field is RETIRED inline document creation
//     was decommissioned; the canonical downstream document.generate
//     job owns Doc creation.
type FacadeHandler struct {
	voService      *voiceover.Service
	groupsResolver *destination.Resolver
	publisher      delivery.Publisher
	log            *zap.Logger
}

// NewFacadeHandler constructs the canonical FacadeHandler with all
// required fields. NewScriptFlowHandler is the only intended caller
// (composition-root-mediated). Log is nil-coalesced to zap.NewNop()
// so ResolveDriveFolderID's nil-publisher warning path is
// safe even when log is nil (mirrors ScriptFlowHandler's NewNop
// fallback per godlike/06 SSOT).
func NewFacadeHandler(
	voService *voiceover.Service,
	groupsResolver *destination.Resolver,
	publisher delivery.Publisher,
	log *zap.Logger,
) *FacadeHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &FacadeHandler{
		voService:      voService,
		groupsResolver: groupsResolver,
		publisher:      publisher,
		log:            log,
	}
}

// GetVoiceoverService returns the pre-built voiceover service so
// orchestrators / cross-package callers can read it without going
// through ScriptFlowHandler. Typed-nil-tolerant (returns nil if
// voService was wired nil — orchestrator-level 503 path).
func (fh *FacadeHandler) GetVoiceoverService() *voiceover.Service {
	return fh.voService
}

// GetGroupsResolver returns the script-side groups resolver (asset-
// tree-backed folder group picker). Typed-nil-tolerant.
func (fh *FacadeHandler) GetGroupsResolver() *destination.Resolver {
	return fh.groupsResolver
}

// ResolveDriveFolderID resolves the Drive folder ID for a script
// generation request. Heuristic: empty → defaultRootID; raw Google
// Drive ID (19-45 chars alphanumeric+_-) → verbatim return;
// otherwise treat as path segments separated by '/' or '\\' and
// resolve via fh.publisher.ResolveFolder with DestinationYouTubeClip.
//
// FASE A5 (July 2026): migrated from the legacy DriveFolderClient
// (iterative GetOrCreateFolder per segment) to the canonical
// delivery.Publisher.ResolveFolder. The PathBuilder model produces up
// to 2 levels (Group / Subject) per call; multi-segment paths use the
// last segment as Subject and the remaining segments as Group.
//
// PR-P12-HANDLER-FACADE-SEMANTIC (July 2026): the legacy
// RootFolderOverride=defaultRootID bypass is RETIRED per godlike/07
// NO-FAKE-AVAILABILITY. The canonical Publisher now resolves the
// root folder for DestinationYouTubeClip via DestinationRegistry +
// DestinationPolicy.RootFolderID (single source of truth for root
// folders per the architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY
// wave). Group + Subject are the SOLE wire-shape inputs; the
// caller-supplied defaultRootID remains as the empty-input
// fallback only (no path to resolve → return the operator's
// configured default verbatim, never as a bypass literal).
func (fh *FacadeHandler) ResolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultRootID, nil
	}

	isRawID := true
	if len(input) < 19 || len(input) > 45 {
		isRawID = false
	} else {
		for _, r := range input {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				isRawID = false
				break
			}
		}
	}

	if isRawID {
		return input, nil
	}

	if fh.publisher == nil {
		fh.log.Warn("publisher not initialized, cannot resolve folder path; returning defaultRootID",
			zap.String("input", input))
		return defaultRootID, nil
	}

	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	// Build clean parts list (prune empty segments).
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return defaultRootID, nil
	}

	// Map to the Publisher's 2-level Group / Subject model per
	// PR-P12-HANDLER-FACADE-SEMANTIC (July 2026). Single segment:
	// use as Group with placeholder Subject. Multi-segment: last
	// segment = Subject, preceding segments joined as Group.
	// RootFolderOverride is INTENTIONALLY OMITTED — the canonical
	// Publisher resolves the root folder for DestinationYouTubeClip
	// via DestinationRegistry + DestinationPolicy.RootFolderID
	// (single source of truth for root folders per the
	// architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY wave).
	// defaultRootID is preserved as the empty-input + nil-publisher
	// fallback only (godlike/07 NO-FAKE-AVAILABILITY: no path to
	// resolve → return the operator's configured default verbatim,
	// never as a bypass literal).
	var group, subject string
	if len(clean) == 1 {
		group = clean[0]
		subject = "_script"
	} else {
		group = strings.Join(clean[:len(clean)-1], "/")
		subject = clean[len(clean)-1]
	}

	dirID, err := fh.publisher.ResolveFolder(ctx, delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		Group:       group,
		Subject:     subject,
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve folder path %q: %w", input, err)
	}

	return dirID, nil
}
