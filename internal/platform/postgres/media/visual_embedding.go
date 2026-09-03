// Package media — visual_embedding.go: the PRODUCTION visual-embedding
// pipeline (POSTGRES-MEDIA-CUTOVER TODO 3).
//
// Chain of custody for one video asset:
//
//	asset (local path)
//	  ↓  KeyframeSamplerPort (shared with MediaFeatureAnalyzer)
//	  ↓  VisualEmbedder (SigLIP image encoder via the canonical sidecar)
//	  ↓  mean pooling over the per-frame vectors (the "3 keyframes → 1
//	     asset vector" recipe; the sampler cadence is configurable)
//	  ↓  VectorSurfaceWriter.UpsertEmbedding (family "visual")
//
// Model identity is registry-driven, never hardcoded into a provider:
// VisualEmbeddingModelRegistry pins the (model_id, dim) pairs allowed to
// produce visual vectors; the composition root selects THE production
// model at boot and the media_embedding_families fail-closed gate
// validates every vector against it.
//
// godlike/07 NO-FAKE-AVAILABILITY: an unavailable sidecar is a typed
// error (never a zero vector, never a silent skip). Dimension and model
// identity of every sidecar response are validated against the registry
// before the pooling step.
package media

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Canonical visual model identity (kernel/models registry SSOT:
// SigLIP so400m patch14-384, 768 dims, cosine, normalized).
const (
	DefaultVisualModelID = "google/siglip-so400m-patch14-384"
	DefaultVisualDim     = 768
)

// VisualEmbedder produces one embedding vector per input frame path.
// Production concrete: SidecarVisualEmbedder (SigLIP image encoder).
type VisualEmbedder interface {
	EmbedFrames(ctx context.Context, framePaths []string) ([][]float32, error)
}

// VisualModelSpec is one registry entry: a model allowed to produce
// visual embeddings for the media SSOT.
type VisualModelSpec struct {
	ModelID string
	Dim     int
}

// VisualEmbeddingModelRegistry is the fail-closed registry of visual
// embedding models. The composition root selects THE production model;
// the embedder validates every sidecar response against it before any
// write (the DB family gate is the second, authoritative check).
type VisualEmbeddingModelRegistry struct {
	entries map[string]VisualModelSpec
}

// NewVisualEmbeddingModelRegistry constructs the registry from explicit
// entries. An empty entry list fails closed at construction.
func NewVisualEmbeddingModelRegistry(entries ...VisualModelSpec) (*VisualEmbeddingModelRegistry, error) {
	if len(entries) == 0 {
		return nil, errors.New("visual embedding registry: at least one model entry is required")
	}
	m := make(map[string]VisualModelSpec, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.ModelID) == "" || e.Dim <= 0 {
			return nil, fmt.Errorf("visual embedding registry: invalid entry (model_id=%q dim=%d)", e.ModelID, e.Dim)
		}
		m[e.ModelID] = e
	}
	return &VisualEmbeddingModelRegistry{entries: m}, nil
}

// DefaultVisualEmbeddingModelRegistry returns the canonical registry:
// SigLIP so400m patch14-384 @ 768d (the kernel/models SSOT identity).
func DefaultVisualEmbeddingModelRegistry() *VisualEmbeddingModelRegistry {
	r, err := NewVisualEmbeddingModelRegistry(VisualModelSpec{ModelID: DefaultVisualModelID, Dim: DefaultVisualDim})
	if err != nil {
		panic("media.DefaultVisualEmbeddingModelRegistry: canonical entry rejected: " + err.Error())
	}
	return r
}

// Resolve returns the spec for a model id (typed error when unknown).
func (r *VisualEmbeddingModelRegistry) Resolve(modelID string) (VisualModelSpec, error) {
	spec, ok := r.entries[modelID]
	if !ok {
		return VisualModelSpec{}, fmt.Errorf("visual embedding registry: unknown model %q (registered: %v)", modelID, r.modelIDs())
	}
	return spec, nil
}

func (r *VisualEmbeddingModelRegistry) modelIDs() []string {
	out := make([]string, 0, len(r.entries))
	for id := range r.entries {
		out = append(out, id)
	}
	return out
}

// Typed sentinel errors (godlike/07).
var (
	ErrVisualSidecarUnavailable    = errors.New("visual embedder: sidecar unavailable (HTTP 501 or no server URL)")
	ErrVisualDimMismatch           = errors.New("visual embedder: sidecar dimension mismatch against the model registry")
	ErrVisualModelIdentityMismatch = errors.New("visual embedder: sidecar model identity mismatch against the model registry")
	ErrVisualEmptyResponse         = errors.New("visual embedder: sidecar returned no embeddings")
)

// SidecarVisualEmbedder calls the Python embedding sidecar's canonical
// batch endpoint (/embed_visual_from_images — SigLIP image encoder,
// one batched forward pass for N frames) and validates every response
// against the VisualEmbeddingModelRegistry.
type SidecarVisualEmbedder struct {
	serverURL string
	client    *http.Client
	registry  *VisualEmbeddingModelRegistry
	modelID   string
}

// NewSidecarVisualEmbedder constructs the embedder. All deps are
// mandatory: an empty server URL or an unresolvable model id fails at
// construction (the composition root cannot express "half-wired").
func NewSidecarVisualEmbedder(serverURL string, registry *VisualEmbeddingModelRegistry, modelID string, timeout time.Duration) (*SidecarVisualEmbedder, error) {
	if strings.TrimSpace(serverURL) == "" {
		return nil, errors.New("visual embedder: server URL is required (no fake availability)")
	}
	if registry == nil {
		return nil, errors.New("visual embedder: model registry is required")
	}
	if _, err := registry.Resolve(modelID); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &SidecarVisualEmbedder{
		serverURL: strings.TrimRight(serverURL, "/"),
		client:    &http.Client{Timeout: timeout},
		registry:  registry,
		modelID:   modelID,
	}, nil
}

// EmbedFrames POSTs {"image_paths": [...]} to the sidecar's batch
// endpoint and returns one order-preserved vector per frame. The
// response's model identity and dimension are validated against the
// registry (fail-closed) BEFORE the caller's pooling step.
func (e *SidecarVisualEmbedder) EmbedFrames(ctx context.Context, framePaths []string) ([][]float32, error) {
	if len(framePaths) == 0 {
		return nil, errors.New("visual embedder: no frames supplied")
	}
	resp, err := postJSON(ctx, e.client, e.serverURL+"/embed_visual_from_images", map[string][]string{"image_paths": framePaths})
	if err != nil {
		return nil, fmt.Errorf("visual embedder: sidecar call: %w", err)
	}
	if resp.status == http.StatusNotImplemented {
		return nil, fmt.Errorf("%w (HTTP 501 — model not loaded)", ErrVisualSidecarUnavailable)
	}
	if resp.status != http.StatusOK {
		return nil, fmt.Errorf("visual embedder: sidecar HTTP %d: %s", resp.status, truncateForLog(resp.body))
	}

	spec, specErr := e.registry.Resolve(e.modelID)
	if specErr != nil {
		return nil, specErr
	}
	envelope, err := decodeVisualBatch(resp.body, spec)
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

// PoolMean averages the per-frame vectors into ONE asset-level visual
// embedding. This is the canonical "sample N keyframes → pool → 1 vector"
// recipe. All vectors must share the registry dimension (the embedder
// guarantees it, but pooling re-checks so a custom VisualEmbedder cannot
// smuggle a ragged batch through).
func (r *VisualEmbeddingModelRegistry) PoolMean(spec VisualModelSpec, vectors [][]float32) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, errors.New("visual pooling: no vectors to pool")
	}
	out := make([]float32, spec.Dim)
	for i, v := range vectors {
		if len(v) != spec.Dim {
			return nil, fmt.Errorf("%w: vector %d is %dd, registry dim is %d", ErrVisualDimMismatch, i, len(v), spec.Dim)
		}
		for j := range v {
			out[j] += v[j]
		}
	}
	n := float32(len(vectors))
	for j := range out {
		out[j] /= n
	}
	return out, nil
}

// VisualEmbeddingResult is the machine-readable outcome of one asset run.
type VisualEmbeddingResult struct {
	AssetID        string
	ModelID        string
	Dim            int
	FramesEmbedded int
}

// VisualEmbeddingPipeline is the production orchestrator: keyframes →
// embedder → pooling → VectorSurfaceWriter.
type VisualEmbeddingPipeline struct {
	deps VisualEmbeddingDeps
}

// VisualEmbeddingDeps wires the pipeline.
type VisualEmbeddingDeps struct {
	// Keyframes is the shared sampler port (percentage cadence).
	Keyframes KeyframeSamplerPort
	// Embedder is the visual embedding port (sidecar concrete).
	Embedder VisualEmbedder
	// Registry + ModelID pin the production visual family.
	Registry *VisualEmbeddingModelRegistry
	ModelID  string
	// FrameCount is the sampling cadence. Zero means 5.
	FrameCount int
}

// NewVisualEmbeddingPipeline constructs the pipeline; every dep slot is
// mandatory and fails closed at construction.
func NewVisualEmbeddingPipeline(deps VisualEmbeddingDeps) (*VisualEmbeddingPipeline, error) {
	if deps.Keyframes == nil {
		return nil, errors.New("visual embedding pipeline: keyframe sampler is required")
	}
	if deps.Embedder == nil {
		return nil, errors.New("visual embedding pipeline: visual embedder is required")
	}
	if deps.Registry == nil {
		return nil, errors.New("visual embedding pipeline: model registry is required")
	}
	if _, err := deps.Registry.Resolve(deps.ModelID); err != nil {
		return nil, err
	}
	if deps.FrameCount <= 0 {
		deps.FrameCount = 5
	}
	return &VisualEmbeddingPipeline{deps: deps}, nil
}

// EmbedAndStore runs the visual pipeline for one asset and upserts the
// pooled vector into media_embeddings (family "visual") through the
// VectorSurfaceWriter. The asset row must already exist (FK enforced).
func (p *VisualEmbeddingPipeline) EmbedAndStore(ctx context.Context, vectors *VectorSurfaceWriter, assetID, localPath string) (*VisualEmbeddingResult, error) {
	if vectors == nil {
		return nil, errors.New("visual embedding pipeline: vector surface writer is required")
	}
	spec, err := p.deps.Registry.Resolve(p.deps.ModelID)
	if err != nil {
		return nil, err
	}

	// 1. Keyframes.
	outDir, err := os.MkdirTemp("", "pgmedia-visual-*")
	if err != nil {
		return nil, fmt.Errorf("visual embedding: temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)
	frames, err := p.deps.Keyframes.ExtractPercentageFrames(ctx, localPath, uniformPercentages(p.deps.FrameCount), outDir)
	if err != nil {
		return nil, fmt.Errorf("visual embedding: keyframes asset %q: %w", assetID, err)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("%w: asset %q", ErrFeatureAnalyzerNoFrames, assetID)
	}
	framePaths := make([]string, 0, len(frames))
	for _, f := range frames {
		framePaths = append(framePaths, f.Path)
	}

	// 2. Embed each frame (order-preserved batch).
	perFrame, err := p.deps.Embedder.EmbedFrames(ctx, framePaths)
	if err != nil {
		return nil, fmt.Errorf("visual embedding: embed asset %q: %w", assetID, err)
	}

	// 3. Pool into one asset-level vector.
	pooled, err := p.deps.Registry.PoolMean(spec, perFrame)
	if err != nil {
		return nil, fmt.Errorf("visual embedding: pool asset %q: %w", assetID, err)
	}

	// 4. Persist (family validation trigger is the DB-side gate).
	if err := vectors.UpsertEmbedding(ctx, assetID, "visual", p.deps.ModelID, pooled); err != nil {
		return nil, fmt.Errorf("visual embedding: store asset %q: %w", assetID, err)
	}
	return &VisualEmbeddingResult{
		AssetID:        assetID,
		ModelID:        p.deps.ModelID,
		Dim:            spec.Dim,
		FramesEmbedded: len(frames),
	}, nil
}
