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
//
// BATCH MEMOIZATION. The port is called once per JOB, and a batch is many
// jobs — without memoization each job would repeat the same create-or-reuse
// Drive round-trip. This adapter therefore memoizes
// (RootFolderID, SafeName(SubfolderName)) → leaf folder ID for a bounded TTL
// and singleflight-dedups concurrent misses, so a whole batch costs ONE
// Drive round-trip per TTL window even across sequential jobs. Errors are
// NEVER cached: a failed resolution is retried on the next call, and the TTL
// bounds how long a stale leaf ID (e.g. a folder deleted externally) can
// survive before the adapter re-resolves create-or-reuse.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// destinationFolderMemoTTL bounds how long a resolved leaf folder ID is
// reused without asking Drive again. It is the staleness bound: a folder
// deleted externally is at worst served for one TTL window (the publish then
// fails closed and the next call re-resolves), while a healthy batch still
// pays exactly one create-or-reuse round-trip per window.
const destinationFolderMemoTTL = 10 * time.Minute

// resolvedLeafFolder is one memo entry: the resolved leaf folder ID plus the
// instant after which it must be re-resolved.
type resolvedLeafFolder struct {
	folderID  string
	expiresAt time.Time
}

// ClipRenderDestinationFolderResolver resolves (create-or-reuse) the Drive
// leaf folder a clip.render batch publishes into via the canonical
// delivery.Publisher.ResolveFolder. Fail-closed: an empty resolved folder ID
// is a typed error, never a silent root fallback.
type ClipRenderDestinationFolderResolver struct {
	publisher delivery.Publisher
	log       *zap.Logger

	// memo collapses the N create-or-reuse calls of a batch (all clips carry
	// the same subfolder_name) into ONE Drive round-trip per TTL window;
	// flights dedups concurrent misses into a single in-flight resolution.
	mu      sync.Mutex
	memo    map[string]resolvedLeafFolder
	flights singleflight.Group

	ttl time.Duration
	now func() time.Time // injectable clock (same-package tests); nil = time.Now
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
	return &ClipRenderDestinationFolderResolver{
		publisher: publisher,
		log:       log,
		memo:      make(map[string]resolvedLeafFolder),
		ttl:       destinationFolderMemoTTL,
		now:       time.Now,
	}, nil
}

// clock returns the current instant (test-injectable).
func (r *ClipRenderDestinationFolderResolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// memoHit returns the cached leaf folder ID for key when present and fresh.
func (r *ClipRenderDestinationFolderResolver) memoHit(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.memo[key]
	if !ok {
		return "", false
	}
	if !r.clock().Before(entry.expiresAt) {
		delete(r.memo, key) // expired: force the next call to re-resolve
		return "", false
	}
	return entry.folderID, true
}

// memoStore records a successfully resolved leaf folder ID with the TTL
// expiry. Only success is ever stored — errors are never cached.
func (r *ClipRenderDestinationFolderResolver) memoStore(key, folderID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memo[key] = resolvedLeafFolder{folderID: folderID, expiresAt: r.clock().Add(r.ttl)}
}

// ResolveDestinationFolder resolves RootFolderID/<SafeName(SubfolderName)>
// through delivery.Publisher.ResolveFolder and returns the leaf folder ID.
// The subfolder name is sanitised with textutil.SafeName — the canonical
// folder-name form the FolderManager matches exactly (it never passes a raw
// user-supplied string as a Drive folder segment).
//
// Repeated calls with the same root + subfolder name are served from the TTL
// memo; concurrent misses share ONE Drive round-trip (singleflight). Each
// distinct (root, name) pair resolves independently.
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

	key := rootID + "\x00" + folderName
	if folderID, ok := r.memoHit(key); ok {
		return folderID, nil
	}
	// Concurrent jobs of one batch coalesce into ONE in-flight resolution;
	// sharers get the same folder ID (or the same retryable error).
	v, err, _ := r.flights.Do(key, func() (any, error) {
		// Double-check inside the flight: a flight that completed between the
		// memo probe above and this callback has already stored the result.
		if folderID, ok := r.memoHit(key); ok {
			return folderID, nil
		}
		folderID, err := r.resolveOnDrive(ctx, rootID, folderName)
		if err != nil {
			return "", err
		}
		r.memoStore(key, folderID)
		return folderID, nil
	})
	if err != nil {
		return "", err
	}
	folderID, _ := v.(string)
	return folderID, nil
}

// resolveOnDrive performs the actual create-or-reuse round-trip against the
// canonical delivery.Publisher. Only successful, non-empty resolutions are
// memoized by the caller.
func (r *ClipRenderDestinationFolderResolver) resolveOnDrive(ctx context.Context, rootID, folderName string) (string, error) {
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
