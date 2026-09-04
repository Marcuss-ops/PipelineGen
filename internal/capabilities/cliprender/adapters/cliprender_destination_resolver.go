package adapters

// cliprender_destination_resolver.go wires the canonical Drive leaf-folder
// resolver for clip.render batches.
//
// The resolver is the ONE owner of script/batch subfolder creation inside
// cliprender. It routes through delivery.Publisher.ResolveFolder — the same
// publisher the final upload goes through — so no second Drive reach-through
// exists inside the capability (architecture rule: a caller that holds a
// delivery.Publisher must not also reach the Drive SDK directly).
//
// The publisher never creates folders: the worker calls this port ONCE per
// job when the request carries destination.subfolder_name, then hands the
// publisher the fully-resolved leaf folder ID. All clips of one script/batch
// carry the same subfolder_name, so every job resolves create-or-reuse and
// they converge on a single shared Drive folder
// (ROOT/<SafeName(SubfolderName)>/).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ClipRenderDestinationFolderResolver resolves (create-or-reuse) the Drive
// leaf folder a clip.render batch publishes into via the canonical
// delivery.Publisher.ResolveFolder. Fail-closed: an empty resolved folder ID
// is a typed error, never a silent root fallback.
type ClipRenderDestinationFolderResolver struct {
	publisher delivery.Publisher
	log       *zap.Logger
}

// NewClipRenderDestinationFolderResolver wires the resolver over the
// canonical delivery publisher. publisher is mandatory (nil fails closed at
// construction time — a resolver without a publisher could never create the
// folder it promises to resolve).
func NewClipRenderDestinationFolderResolver(publisher delivery.Publisher, log *zap.Logger) (*ClipRenderDestinationFolderResolver, error) {
	if publisher == nil {
		return nil, errors.New("clip.render destination resolver: delivery.Publisher is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipRenderDestinationFolderResolver{publisher: publisher, log: log}, nil
}

// ResolveDestinationFolder resolves RootFolderID/<SafeName(SubfolderName)>
// through delivery.Publisher.ResolveFolder and returns the leaf folder ID.
// The subfolder name is sanitised with textutil.SafeName — the canonical
// folder-name form the FolderManager matches exactly (it never passes a raw
// user-supplied string as a Drive folder segment).
func (r *ClipRenderDestinationFolderResolver) ResolveDestinationFolder(ctx context.Context, in cliprender.DestinationFolderResolveInput) (string, error) {
	if r == nil || r.publisher == nil {
		return "", errors.New("clip.render destination resolver: delivery.Publisher not wired")
	}
	rootID := strings.TrimSpace(in.RootFolderID)
	folderName := textutil.SafeName(strings.TrimSpace(in.SubfolderName))
	if rootID == "" {
		return "", errors.New("clip.render destination resolver: root folder ID is empty")
	}
	if folderName == "" {
		return "", errors.New("clip.render destination resolver: subfolder name is empty")
	}

	t0 := time.Now()
	r.log.Info("clip.render.destination_resolve.start",
		zap.String("subsystem", "cliprender_destination_resolver"),
		zap.String("root_folder_id", rootID),
		zap.String("subfolder_name", folderName),
	)
	folderID, err := r.publisher.ResolveFolder(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: rootID,
		DestinationSubpath:  []string{folderName},
	})
	if err != nil {
		r.log.Error("clip.render.destination_resolve.failed",
			zap.String("subsystem", "cliprender_destination_resolver"),
			zap.String("root_folder_id", rootID),
			zap.String("subfolder_name", folderName),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
			zap.Error(err),
		)
		return "", fmt.Errorf("resolve drive leaf folder %q under %q: %w", folderName, rootID, err)
	}
	if strings.TrimSpace(folderID) == "" {
		r.log.Error("clip.render.destination_resolve.empty",
			zap.String("subsystem", "cliprender_destination_resolver"),
			zap.String("root_folder_id", rootID),
			zap.String("subfolder_name", folderName),
		)
		return "", fmt.Errorf("resolve drive leaf folder %q under %q: resolver returned an empty folder ID", folderName, rootID)
	}
	r.log.Info("clip.render.destination_resolve.done",
		zap.String("subsystem", "cliprender_destination_resolver"),
		zap.String("root_folder_id", rootID),
		zap.String("subfolder_name", folderName),
		zap.String("folder_id", folderID),
		zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
	)
	return folderID, nil
}
