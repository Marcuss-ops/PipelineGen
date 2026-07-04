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
//   - MaybeCreateGoogleDoc: creates a Google Doc via the document
//     Creator when createDoc=true.
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
//   - driveFolderClient: typed Drive folder resolver (consumes
//     DriveFolderClient interface declared in handler_flow.go).
//   - documentCreator: typed Google Doc creator (consumes
//     DocumentCreator interface).
//   - log: structured logger (used by ResolveDriveFolderID's
//     nil-tolerance warning path).
type FacadeHandler struct {
	voService         *voiceover.Service
	groupsResolver    *voiceover.GroupsResolver
	driveFolderClient DriveFolderClient
	documentCreator   DocumentCreator
	log               *zap.Logger
}

// NewFacadeHandler constructs the canonical FacadeHandler with all
// required fields. NewScriptFlowHandler is the only intended caller
// (composition-root-mediated). Log is nil-coalesced to zap.NewNop()
// so ResolveDriveFolderID's nil-driveFolderClient warning path is
// safe even when log is nil (mirrors ScriptFlowHandler's NewNop
// fallback per godlike/06 SSOT).
func NewFacadeHandler(
	voService *voiceover.Service,
	groupsResolver *voiceover.GroupsResolver,
	driveFolderClient DriveFolderClient,
	documentCreator DocumentCreator,
	log *zap.Logger,
) *FacadeHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &FacadeHandler{
		voService:         voService,
		groupsResolver:    groupsResolver,
		driveFolderClient: driveFolderClient,
		documentCreator:   documentCreator,
		log:               log,
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
func (fh *FacadeHandler) GetGroupsResolver() *voiceover.GroupsResolver {
	return fh.groupsResolver
}

// ResolveDriveFolderID resolves the Drive folder ID for a script
// generation request. Heuristic: empty → defaultRootID; raw Google
// Drive ID (19-45 chars alphanumeric+_-) → verbatim return;
// otherwise treat as path segments separated by '/' or '\\' and
// resolve each via fh.driveFolderClient.GetOrCreateFolder walking
// the path top-down (currentID starts at defaultRootID).
//
// Body verbatim from internal/api/script/flow.go's lowercase
// `resolveDriveFolderID` method on ScriptFlowHandler (retired
// post-extraction per godlike/07 minimum-blast-radius — the only
// caller was the public ScriptFlowHandler.ResolveDriveFolderID,
// now a thin delegator to this method).
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

	if fh.driveFolderClient == nil {
		fh.log.Warn("driveFolderClient not initialized, cannot resolve folder name/path; returning defaultRootID",
			zap.String("input", input))
		return defaultRootID, nil
	}

	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	currentID := defaultRootID
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := fh.driveFolderClient.GetOrCreateFolder(ctx, part, currentID)
		if err != nil {
			return "", fmt.Errorf("failed to get or create folder %q under %q: %w", part, currentID, err)
		}
		currentID = id
	}

	return currentID, nil
}

// MaybeCreateGoogleDoc creates a Google Doc via the document Creator
// when createDoc=true; returns empty URL+ID otherwise. The creator
// is expected to handle its own connectivity (returns empty strings
// on transient failure — pre-extraction semantics preserved).
func (fh *FacadeHandler) MaybeCreateGoogleDoc(ctx context.Context, title, content, folderID string, createDoc bool) (string, string) {
	if !createDoc {
		return "", ""
	}
	return fh.documentCreator.CreateDoc(ctx, title, content, folderID)
}
