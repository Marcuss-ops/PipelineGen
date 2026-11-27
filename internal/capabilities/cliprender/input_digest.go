// input_digest.go implements the CRA-03 cache guard: a cache key proves the
// REQUEST is the same, but not that the BYTES behind its asset ids still are.
// The input digest is the revision of the resolved inputs (source, background,
// watermark, transcript, overlays); it is recorded at publish time and
// recomputed on every cache hit. A mismatch is a miss, never a stale publish.
//
// The existing revision pins (source_revision, background.revision, …) remain
// the caller's way to make the same proof part of the cache KEY. This digest is
// the automatic half: it works for callers that pin nothing, using the
// registry/transcript hashes the preparer already resolves.
package cliprender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// InputDigestResolver is the narrow port the HTTP handler needs to validate a
// cache hit. The Preparer implements it; a nil resolver preserves the legacy
// key-only behaviour (documented migration, not a silent default).
type InputDigestResolver interface {
	InputDigest(ctx context.Context, req *RenderRequest) (string, error)
}

// ErrInputRevisionUnavailable is returned when the current revision of an
// input cannot be established (no content hash, no READY transcript, or a
// transcript policy that regenerates every run). The caller treats it as a
// cache MISS: rendering is the only way to produce bytes it can certify.
var ErrInputRevisionUnavailable = errors.New("clip.render: input revision unavailable")

const inputRevisionDigestVersion = "cliprender-inputs-v1"

// InputDigestFromPrepared computes the same digest as Preparer.InputDigest, but
// from the already-prepared inputs. It is what the publish path records.
func InputDigestFromPrepared(req *RenderRequest, prepared *Prepared) string {
	if req == nil || prepared == nil {
		return ""
	}
	return computeInputDigest(
		materializedSHA(prepared.Source),
		materializedSHA(prepared.Background),
		materializedSHA(prepared.Watermark),
		transcriptSHA(prepared.Transcript),
		req.Overlays,
	)
}

func materializedSHA(a *MaterializedAsset) string {
	if a == nil {
		return ""
	}
	return a.SHA256
}

func transcriptSHA(t *TranscriptResult) string {
	if t == nil {
		return ""
	}
	if strings.TrimSpace(t.TextSHA256) != "" {
		return t.TextSHA256
	}
	// A transcript without the canonical text digest cannot be identified by
	// revision; leave the component empty so the mismatch is explicit.
	return ""
}

// computeInputDigest hashes only content revisions, never ids, timings or
// destinations. Overlay render keys are content-addressed by construction, so
// they belong here; sorting them makes the digest order-independent.
func computeInputDigest(source, background, watermark, transcript string, overlays []OverlayRefSpec) string {
	keys := make([]string, 0, len(overlays))
	for _, o := range overlays {
		if k := strings.ToLower(strings.TrimSpace(o.RenderKey)); k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	b, err := json.Marshal(struct {
		Version     string   `json:"version"`
		Source      string   `json:"source"`
		Background  string   `json:"background,omitempty"`
		Watermark   string   `json:"watermark,omitempty"`
		Transcript  string   `json:"transcript,omitempty"`
		OverlayKeys []string `json:"overlay_keys,omitempty"`
	}{
		Version:     inputRevisionDigestVersion,
		Source:      strings.ToLower(strings.TrimSpace(source)),
		Background:  strings.ToLower(strings.TrimSpace(background)),
		Watermark:   strings.ToLower(strings.TrimSpace(watermark)),
		Transcript:  strings.ToLower(strings.TrimSpace(transcript)),
		OverlayKeys: keys,
	})
	if err != nil {
		return ""
	}
	return digest.SHA256Bytes(b)
}

// InputDigest resolves the current revisions and returns their digest. It is
// the lookup-side half of the guard: it performs no materialization and no ASR,
// only the wave-1 identity/lookup reads. Concurrent because it runs on the
// cache-hit path.
func (p *Preparer) InputDigest(ctx context.Context, req *RenderRequest) (string, error) {
	if p == nil {
		return "", fmt.Errorf("%w: preparer is nil", ErrInputRevisionUnavailable)
	}
	if req == nil {
		return "", fmt.Errorf("%w: request is nil", ErrInputRevisionUnavailable)
	}
	cp := req.cloneForFingerprint()
	cp.Normalize()
	if cp.Transcript == nil {
		return "", fmt.Errorf("%w: transcript spec is missing", ErrInputRevisionUnavailable)
	}
	// `generate` deliberately re-runs ASR: there is no revision to compare
	// against before the run, so a cached artifact can never be certified.
	if cp.Transcript.Mode == TranscriptModeGenerate {
		return "", fmt.Errorf("%w: transcript mode %q regenerates every run", ErrInputRevisionUnavailable, cp.Transcript.Mode)
	}

	var (
		source     *AssetRef
		background *AssetRef
		watermark  *AssetRef
		transcript *TranscriptResult
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		ref, err := p.assets.ResolveAsset(gctx, cp.SourceAssetID)
		if err != nil {
			return fmt.Errorf("resolve source revision: %w", err)
		}
		source = ref
		return nil
	})
	if cp.Background != nil && cp.Background.Mode == BackgroundModeAsset {
		g.Go(func() error {
			ref, err := p.assets.ResolveAsset(gctx, cp.Background.AssetID)
			if err != nil {
				return fmt.Errorf("resolve background revision: %w", err)
			}
			background = ref
			return nil
		})
	}
	if cp.Watermark != nil && cp.Watermark.Enabled && strings.TrimSpace(cp.Watermark.Text) == "" {
		g.Go(func() error {
			ref, err := p.assets.ResolveAsset(gctx, cp.Watermark.AssetID)
			if err != nil {
				return fmt.Errorf("resolve watermark revision: %w", err)
			}
			watermark = ref
			return nil
		})
	}
	g.Go(func() error {
		res, found, err := p.transcript.Lookup(gctx, TranscriptInput{
			AssetID:  cp.SourceAssetID,
			Language: cp.Transcript.Language,
			Mode:     cp.Transcript.Mode,
			Persist:  cp.Transcript.Persist,
		})
		if err != nil {
			return fmt.Errorf("resolve transcript revision: %w", err)
		}
		if !found || res == nil || strings.TrimSpace(res.TextSHA256) == "" {
			return fmt.Errorf("%w: no READY transcript for %q language %q", ErrInputRevisionUnavailable, cp.SourceAssetID, cp.Transcript.Language)
		}
		transcript = res
		return nil
	})
	if err := g.Wait(); err != nil {
		return "", err
	}
	if source == nil || strings.TrimSpace(source.ContentSHA256) == "" {
		return "", fmt.Errorf("%w: source %q has no content hash", ErrInputRevisionUnavailable, cp.SourceAssetID)
	}
	if background != nil && strings.TrimSpace(background.ContentSHA256) == "" {
		return "", fmt.Errorf("%w: background %q has no content hash", ErrInputRevisionUnavailable, cp.Background.AssetID)
	}
	if watermark != nil && strings.TrimSpace(watermark.ContentSHA256) == "" {
		return "", fmt.Errorf("%w: watermark %q has no content hash", ErrInputRevisionUnavailable, cp.Watermark.AssetID)
	}
	return computeInputDigest(
		source.ContentSHA256,
		refContentSHA(background),
		refContentSHA(watermark),
		transcript.TextSHA256,
		cp.Overlays,
	), nil
}

func preparedSourceTitle(prepared *Prepared) string {
	if prepared == nil || prepared.Source == nil {
		return ""
	}
	return prepared.Source.Title
}

func preparedLanguage(prepared *Prepared) string {
	if prepared == nil || prepared.Transcript == nil {
		return ""
	}
	return prepared.Transcript.Language
}

func refContentSHA(ref *AssetRef) string {
	if ref == nil {
		return ""
	}
	return ref.ContentSHA256
}

// cacheInputMatches reports whether a cache record still describes the current
// inputs.
//
// Fail-closed rules:
//   - no record: nothing to serve
//   - record without a digest: UNPROVEN, therefore a miss. The
//     RendererLoweringVersion bump already makes pre-digest fingerprints
//     unreachable, so this branch cannot strand live rows; every production
//     write path records a digest.
//   - resolver error or empty revision: miss (the bytes cannot be certified)
//   - resolver nil: the explicit legacy key-only mode, preserved for callers
//     that have not wired the port yet (see InputDigestResolver).
func cacheInputMatches(ctx context.Context, resolver InputDigestResolver, req *RenderRequest, rec *RenderCacheRecord) bool {
	if rec == nil {
		return false
	}
	if resolver == nil {
		return true
	}
	if strings.TrimSpace(rec.InputSHA256) == "" {
		return false
	}
	digestNow, err := resolver.InputDigest(ctx, req)
	if err != nil || digestNow == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(rec.InputSHA256), digestNow)
}
