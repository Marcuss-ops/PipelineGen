// Package usecase hosts engine-side use cases for the script pipeline.
//
// post_voiceover_composer.go is the post-voiceover canonical composer: it takes
// the strict JSON envelope produced by the LLM (Ref+Text only — clip_id is the
// infrastructure concern, never the model concern) and emits a SpecScene manifest
// whose clip entries are hydrated via a binding table. The Drive write goes
// through the canonical delivery.Publisher port — godlike/08 forward-prevention
// gate forbids ParentFolderID in application/api layers; this file does not
// touch it.

package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Godlike/07 NO-FAKE-AVAILABILITY: typed sentinels. Every caller can errors.Is
// these to distinguish composition-time failures from runtime failures.
var (
	ErrComposerEmptyModelOutput     = errors.New("post-voiceover composer: empty model output (no segments)")
	ErrComposerRefBindingMissing    = errors.New("post-voiceover composer: ref has no binding entry in BindingTable")
	ErrComposerNilPublisher         = errors.New("post-voiceover composer: nil delivery.Publisher at construction (would silently no-op Drive write)")
	ErrComposerNilResolver          = errors.New("post-voiceover composer: nil RefBindingResolver at construction")
	ErrComposerManifestDestination  = errors.New("post-voiceover composer: empty delivery.DestinationKey (would dangle on Drive)")
	ErrComposerIncompleteRefBinding = errors.New("post-voiceover composer: resolved RefBinding has empty ClipID or DriveLink (would write a structurally bogus manifest; godlike/07 NO-FAKE-AVAILABILITY)")
	ErrComposerInvalidRefTimeRange  = errors.New("post-voiceover composer: resolved RefBinding has invalid time range (StartMs < 0 or EndMs <= StartMs)")
)

// RefBinding is the canonical tabular binding row produced post-voiceover.
// For every model ref ("slot-1:candidate-0") the resolver hydrates one row
// with the infrastructure identity (ClipID, DriveLink, StartMs, EndMs) that
// the strict JSON envelope deliberately excludes.
type RefBinding struct {
	ClipID    string
	ClipTitle string
	DriveLink string
	StartMs   int64
	EndMs     int64
}

// BindingTable maps a model ref (the exact string emitted as "ref" by the
// LLM, e.g. "slot-1:candidate-0") to its RefBinding row.
type BindingTable map[string]RefBinding

// RefBindingResolver is the application-level port that resolves a model
// ref into a RefBinding. Implementations may read from DB, in-memory table,
// static fixture, etc.
type RefBindingResolver interface {
	Resolve(ctx context.Context, ref string) (RefBinding, error)
}

// StaticRefBindingResolver is a fixture-friendly resolver bound to a
// BindingTable snapshot. Useful for tests and the Pacquiao repro.
type StaticRefBindingResolver struct {
	Table BindingTable
}

func (s *StaticRefBindingResolver) Resolve(_ context.Context, ref string) (RefBinding, error) {
	if s.Table == nil {
		return RefBinding{}, fmt.Errorf("static resolver: nil table for ref %q: %w", ref, ErrComposerRefBindingMissing)
	}
	b, ok := s.Table[ref]
	if !ok {
		return RefBinding{}, fmt.Errorf("static resolver: ref %q not in table: %w", ref, ErrComposerRefBindingMissing)
	}
	return b, nil
}

// SpecSceneManifest is the on-Drive artifact the composer publishes. The
// shape is fixed at version "1.0"; per the LLM-compact contract the model
// never authors clip_id/drive_link/start_ms/end_ms — those land here as a
// post-voiceover hydration step.
type SpecSceneManifest struct {
	Version   string           `json:"version"`
	CreatedAt string           `json:"created_at"`
	AssetID   string           `json:"asset_id,omitempty"`
	Scenes    []SpecSceneEntry `json:"scenes"`
}

type SpecSceneEntry struct {
	Ref   string               `json:"ref"`
	Text  string               `json:"text"`
	Index int                  `json:"index"`
	Clip  SpecSceneClipBinding `json:"clip"`
}

type SpecSceneClipBinding struct {
	ClipID    string `json:"clip_id"`
	DriveLink string `json:"drive_link"`
	StartMs   int64  `json:"start_ms"`
	EndMs     int64  `json:"end_ms"`
}

// PostVoiceoverComposer is the canonical entrypoint that binds the LLM
// strict envelope to the delivery.Publisher after voiceover has run.
type PostVoiceoverComposer struct {
	publisher delivery.Publisher
	resolver  RefBindingResolver
}

// NewPostVoiceoverComposer fails-closed if either port is nil. Per
// godlike/07 NO-FAKE-AVAILABILITY we never silently no-op the Drive
// write; a missing port is a hard composition error.
func NewPostVoiceoverComposer(pub delivery.Publisher, resolver RefBindingResolver) (*PostVoiceoverComposer, error) {
	if pub == nil {
		return nil, ErrComposerNilPublisher
	}
	if resolver == nil {
		return nil, ErrComposerNilResolver
	}
	return &PostVoiceoverComposer{publisher: pub, resolver: resolver}, nil
}

// ComposeAndPublish is the post-voiceover canonical entrypoint.
//
// Steps:
//  1. Validate (non-empty envelope, non-empty destination).
//  2. ResolveFolder via delivery.Publisher BEFORE any local disk write
//     so auth/perm failures do not leak an orphan temp file.
//  3. Hydrate manifest by walking modelOutput.Segments and resolving each
//     ref via resolver. A missing binding is a typed error and stops the
//     publish.
//  4. Marshal manifest → os.CreateTemp → defer os.Remove → Publish with
//     LocalPath (the only path-based publish contract in the repo).
//
// Returns the manifest (for downstream logging / inspection) and the
// PublishResult (file_id, web_view_link) from the Drive write.
func (c *PostVoiceoverComposer) ComposeAndPublish(
	ctx context.Context,
	modelOutput scriptpkg.ModelScriptOutputV1,
	destination delivery.DestinationKey,
	driveGroup string,
	driveSubject string,
	assetID string,
) (SpecSceneManifest, delivery.PublishResult, error) {
	// 1. Validate envelope + destination.
	if len(modelOutput.SpecScene.Scenes) == 0 {
		return SpecSceneManifest{}, delivery.PublishResult{}, ErrComposerEmptyModelOutput
	}
	if destination == "" {
		return SpecSceneManifest{}, delivery.PublishResult{}, ErrComposerManifestDestination
	}

	// 2. ResolveFolder first — pass via PublishRequest so the canonical
	// publisher can use Group/Subject/AssetID metadata. ParentFolderID
	// is deliberately NOT set (godlike/08 forward-prevention).
	if _, err := c.publisher.ResolveFolder(ctx, delivery.PublishRequest{
		Destination: destination,
		Group:       driveGroup,
		Subject:     driveSubject,
		AssetID:     assetID,
	}); err != nil {
		return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("resolve folder for destination %q: %w", destination, err)
	}

	// 3. Hydrate manifest.
	manifest := SpecSceneManifest{
		Version:   "1.0",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AssetID:   assetID,
		Scenes:    make([]SpecSceneEntry, 0, len(modelOutput.SpecScene.Scenes)),
	}
	for i, seg := range modelOutput.SpecScene.Scenes {
		b, err := c.resolver.Resolve(ctx, seg.ID)
		if err != nil {
			return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("resolve ref %q at index %d: %w", seg.ID, i, err)
		}
		// godlike/07 NO-FAKE-AVAILABILITY: validate the resolved RefBinding
		// BEFORE hydrating the manifest. A resolver returning a partial
		// binding (empty ClipID/DriveLink or invalid StartMs/EndMs) would
		// otherwise write a structurally-bogus JSON to Drive — clients
		// would parse it successfully but find the clip unusable.
		if b.ClipID == "" || b.DriveLink == "" {
			return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("ref %q at index %d: incomplete RefBinding (ClipID=%q, DriveLink=%q): %w", seg.ID, i, b.ClipID, b.DriveLink, ErrComposerIncompleteRefBinding)
		}
		if b.StartMs < 0 || b.EndMs <= b.StartMs {
			return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("ref %q at index %d: invalid RefBinding time range (StartMs=%d, EndMs=%d): %w", seg.ID, i, b.StartMs, b.EndMs, ErrComposerInvalidRefTimeRange)
		}
		manifest.Scenes = append(manifest.Scenes, SpecSceneEntry{
			Ref:   seg.ID,
			Text:  seg.Text,
			Index: i,
			Clip: SpecSceneClipBinding{
				ClipID:    b.ClipID,
				DriveLink: b.DriveLink,
				StartMs:   b.StartMs,
				EndMs:     b.EndMs,
			},
		})
	}

	// 4. Marshal + temp file + Publish.
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("marshal manifest: %w", err)
	}
	tmp, err := os.CreateTemp("", "spec-scene-*.json")
	if err != nil {
		return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("close temp file: %w", err)
	}

	// ParentFolderID is deliberately NOT set: godlike/08 forward-
	// prevention gate forbids it outside infra/cmd/admin. The folder
	// resolved above is the canonical destination.
	res, err := c.publisher.Publish(ctx, delivery.PublishRequest{
		Destination: destination,
		LocalPath:   tmpPath,
		Filename:    "spec-scene.json",
		Description: "SpecScene manifest (ref -> drive clip + times)",
		Group:       driveGroup,
		Subject:     driveSubject,
		AssetID:     assetID,
	})
	if err != nil {
		return SpecSceneManifest{}, delivery.PublishResult{}, fmt.Errorf("publish manifest: %w", err)
	}
	return manifest, *res, nil
}
