// Package app — wire_script_postprocess_document.go.
//
// FASE 2.A PR3 split (July 2026): Google Doc processor registration
// extracted from wire_script_postprocess.go per AGENTS.md Pattern 5
// godlike/06 SSOT one-canonical-owner-per-fact. The document processor
// is the ONLY inline postprocessor that involves Drive I/O (doc creation)
// and folder resolution, so it warrants its own file.
//
// Cross-references:
//   - internal/app/wire_script_postprocess.go: registerScriptPostProcessors
//     calls registerDocumentProcessor when root.Drive.DocClient is available.
//   - internal/application/scripts/adapters: NewDocumentProcessor
//   - internal/application/scripts/usecase: NewDocumentsService
//   - internal/application/assets/delivery: PublishRequest, DestinationScript
package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// registerDocumentProcessor registers the inline Google Doc postprocessor.
// The processor creates a Google Doc for each generated script and resolves
// the target Drive folder from the caller-supplied folder input.
//
// Pre-conditions:
//   - root.Drive != nil && root.Drive.DocClient != nil
//   - ppReg != nil
//
// Post-conditions:
//   - DocumentProcessor is registered on ppReg (preserving the
//     Persistence → Document canonical ordering invariant).
//
// SCRIPTCONTRACT-2026-07-08: Persistence is FIRST (slot 0); Document is
// SECOND (slot 1) — this function is called after PersistenceRegistration
// in registerScriptPostProcessors so the ordering invariant holds.
func registerDocumentProcessor(
	ppReg *adapters.PostProcessorRegistry,
	root *ComposeRoot,
	cfg *config.Config,
	log *zap.Logger,
) error {
	docsSvc := usecase.NewDocumentsService(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())
	resolveFolder := resolveDocumentFolder(root)
	if !ppReg.Register(adapters.NewDocumentProcessor(docsSvc, resolveFolder)) {
		return fmt.Errorf("register document processor: composition bug or duplicate name")
	}
	log.Info("DocumentProcessor (inline Google Docs) successfully registered")
	return nil
}

// resolveDocumentFolder returns a folder-resolution closure that the
// DocumentProcessor uses to resolve caller-supplied folder inputs into
// canonical Drive folder IDs. The closure is parameterized on the
// ComposeRoot so it can access root.Drive.Publisher at call time.
//
// Resolution strategy:
//  1. Empty input → return defaultRootID (the canonical scripts-gen folder).
//  2. Raw Drive file ID (19-45 alphanumeric chars) → return as-is.
//  3. Path-like input (contains / or \) → resolve via delivery.Publisher
//     using the canonical DestinationScript routing.
//  4. Publisher not available → fall back to defaultRootID.
func resolveDocumentFolder(root *ComposeRoot) func(ctx context.Context, input, defaultRootID string) (string, error) {
	return func(ctx context.Context, input, defaultRootID string) (string, error) {
		input = strings.TrimSpace(input)
		if input == "" {
			return defaultRootID, nil
		}
		if len(input) >= 19 && len(input) <= 45 {
			isRawID := true
			for _, r := range input {
				if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
					isRawID = false
					break
				}
			}
			if isRawID {
				return input, nil
			}
		}
		if root.Drive.Publisher == nil {
			return defaultRootID, nil
		}
		parts := strings.FieldsFunc(input, func(r rune) bool {
			return r == '/' || r == '\\'
		})
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
		group := clean[0]
		subject := "_script"
		if len(clean) > 1 {
			group = strings.Join(clean[:len(clean)-1], "/")
			subject = clean[len(clean)-1]
		}
		folderID, err := root.Drive.Publisher.ResolveFolder(ctx, delivery.PublishRequest{
			Destination:        delivery.DestinationScript,
			Group:              group,
			Subject:            subject,
			RootFolderOverride: defaultRootID,
		})
		if err != nil {
			return "", err
		}
		return folderID, nil
	}
}
