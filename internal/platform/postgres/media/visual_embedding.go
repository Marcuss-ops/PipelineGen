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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// Canonical visual model identity. These are ALIASES of the kernel/models
// registry SSOT (SigLIP so400m patch14-384, 1152 dims, cosine, normalized):
// the model id and its dimension are declared exactly once, in
// internal/kernel/models, and derived here so the media pipeline cannot drift
// from the schema, the HNSW indexes or the Python sidecar mirror.
const (
	DefaultVisualModelID = models.CanonicalVisualModelID
	DefaultVisualDim     = models.CanonicalVisualModelDimensions
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
// SigLIP so400m patch14-384 @ 1152d (the kernel/models SSOT identity).
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
	resp, err := PostJSON(ctx, e.client, e.serverURL+"/embed_visual_from_images", map[string][]string{"image_paths": framePaths})
	if err != nil {
		return nil, fmt.Errorf("visual embedder: sidecar call: %w", err)
	}
	if resp.Status == http.StatusNotImplemented {
		return nil, fmt.Errorf("%w (HTTP 501 — model not loaded)", ErrVisualSidecarUnavailable)
	}
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("visual embedder: sidecar HTTP %d: %s", resp.Status, TruncateForLog(resp.Body))
	}

	spec, specErr := e.registry.Resolve(e.modelID)
	if specErr != nil {
		return nil, specErr
	}
	envelope, err := decodeVisualBatch(resp.Body, spec)
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

// KeyframeSamplerPort is the narrow sampler surface (mirrors
// indexing.PercentageFrameSampler so the production concrete satisfies it
// via a thin adapter in the wiring). It is shared by the visual embedding
// pipeline and the (backfill-only) feature analyzer, so the sampling cadence
// and the sample shape have exactly one owner.
type KeyframeSamplerPort interface {
	ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]KeyframeSample, error)
}

// KeyframeSample mirrors indexing.FrameSample (path + timestamp) without
// importing the indexing package (leaf-only imports for the media package).
type KeyframeSample struct {
	Path       string
	Timestamp  float64
	Percentage float64
}

// ErrKeyframeSampleEmpty is the typed fail-closed error for a sampler that
// produced no frames. It is pipeline-wide: an empty cadence makes both the
// visual embedding pipeline and the feature analyzer unable to measure
// anything, and neither may fabricate a surface.
var ErrKeyframeSampleEmpty = errors.New("keyframe sampling produced no frames")

// UniformPercentages returns the canonical uniform cadence: n evenly spaced
// percentages including both endpoints (n=5 → 0, .25, .5, .75, 1).
func UniformPercentages(n int) []float64 {
	if n <= 1 {
		return []float64{0}
	}
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, float64(i)/float64(n-1))
	}
	return out
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
	defer func() { _ = os.RemoveAll(outDir) }()
	frames, err := p.deps.Keyframes.ExtractPercentageFrames(ctx, localPath, UniformPercentages(p.deps.FrameCount), outDir)
	if err != nil {
		return nil, fmt.Errorf("visual embedding: keyframes asset %q: %w", assetID, err)
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("%w: asset %q", ErrKeyframeSampleEmpty, assetID)
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

// ── absorbed from embed_asset_text_adapter.go ──

// EmbedAssetTextAdapter adapts the kernel asset.Embedder (HTTPTextEmbedder)
// to the worker's AssetEmbedder port: the asset's search_text is fetched
// from the media SSOT and embedded with the canonical text-channel model.
type EmbedAssetTextAdapter struct {
	db      *sql.DB
	embeder coreasset.Embedder
}

// NewEmbedAssetTextAdapter constructs the adapter. Both deps required.
func NewEmbedAssetTextAdapter(db *sql.DB, embedder coreasset.Embedder) *EmbedAssetTextAdapter {
	if db == nil {
		panic("media.NewEmbedAssetTextAdapter: db is required")
	}
	if embedder == nil {
		panic("media.NewEmbedAssetTextAdapter: embedder is required")
	}
	return &EmbedAssetTextAdapter{db: db, embeder: embedder}
}

// EmbedAssetText reads search_text from media_assets and embeds it.
func (a *EmbedAssetTextAdapter) EmbedAssetText(ctx context.Context, assetID string) ([]float32, error) {
	var text string
	if err := a.db.QueryRowContext(ctx,
		`SELECT search_text FROM media_assets WHERE id = $1`, assetID,
	).Scan(&text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("embed asset %q: asset not found in media SSOT", assetID)
		}
		return nil, fmt.Errorf("embed asset %q: read search_text: %w", assetID, err)
	}
	if strings.TrimSpace(text) == "" {
		// Empty source data cannot be repaired by retrying the provider. Mark it
		// terminal so the outbox does not burn exponential-backoff attempts.
		return nil, event.NewTerminalError(fmt.Errorf("embed asset %q: search_text is empty", assetID))
	}
	res, err := a.embeder.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	return res.Vector, nil
}

// LoadAssetSearchTexts reads the canonical search_text for every requested
// asset in ONE round-trip (a parameterised IN list) instead of one SELECT
// per asset. Assets absent from the media SSOT are simply absent from the
// returned map, so the caller decides whether an unresolved ID is fatal.
func (a *EmbedAssetTextAdapter) LoadAssetSearchTexts(ctx context.Context, assetIDs []string) (map[string]string, error) {
	texts := make(map[string]string, len(assetIDs))
	if len(assetIDs) == 0 {
		return texts, nil
	}
	placeholders := make([]string, len(assetIDs))
	args := make([]any, len(assetIDs))
	for i, id := range assetIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, search_text FROM media_assets WHERE id IN (`+strings.Join(placeholders, ", ")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("load search_text for %d asset(s): %w", len(assetIDs), err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, fmt.Errorf("scan search_text row: %w", err)
		}
		texts[id] = text
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search_text rows: %w", err)
	}
	return texts, nil
}

// batchTextEmbedder is the optional extension implemented by sidecars that
// provide canonical, order-preserving text batch inference.
type batchTextEmbedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([]coreasset.EmbeddingResult, error)
}

// EmbedAssetTexts resolves the canonical search_text values in one database
// query and, when supported, one E5 batch inference request. The single-text
// interface remains the compatibility fallback for other Embedder providers.
// Blank search_text remains omitted so worker retry/dead-letter behavior does
// not change.
func (a *EmbedAssetTextAdapter) EmbedAssetTexts(ctx context.Context, assetIDs []string) (map[string][]float32, error) {
	texts, err := a.LoadAssetSearchTexts(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	return a.embedSearchTexts(ctx, assetIDs, texts)
}

func (a *EmbedAssetTextAdapter) embedSearchTexts(ctx context.Context, assetIDs []string, texts map[string]string) (map[string][]float32, error) {
	out := make(map[string][]float32, len(texts))
	batch, supportsBatch := a.embeder.(batchTextEmbedder)
	if !supportsBatch {
		seen := make(map[string]struct{}, len(assetIDs))
		for _, id := range assetIDs {
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			text, found := texts[id]
			if !found || strings.TrimSpace(text) == "" {
				continue
			}
			res, err := a.embeder.Embed(ctx, text)
			if err != nil {
				return nil, fmt.Errorf("embed asset %q: %w", id, err)
			}
			if len(res.Vector) != 0 {
				out[id] = res.Vector
			}
		}
		return out, nil
	}

	orderedIDs := make([]string, 0, len(texts))
	orderedTexts := make([]string, 0, len(texts))
	seen := make(map[string]struct{}, len(texts))
	for _, id := range assetIDs {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		text, found := texts[id]
		if !found || strings.TrimSpace(text) == "" {
			continue
		}
		orderedIDs = append(orderedIDs, id)
		orderedTexts = append(orderedTexts, text)
	}
	if len(orderedTexts) == 0 {
		return out, nil
	}
	for start := 0; start < len(orderedIDs); start += 32 {
		end := min(start+32, len(orderedIDs))
		results, err := batch.EmbedBatch(ctx, orderedTexts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embed assets %q..%q as batch: %w", orderedIDs[start], orderedIDs[end-1], err)
		}
		if len(results) != end-start {
			return nil, fmt.Errorf("text embedder returned %d batch results for %d asset texts", len(results), end-start)
		}
		for i, result := range results {
			if len(result.Vector) != 0 {
				out[orderedIDs[start+i]] = result.Vector
			}
		}
	}
	return out, nil
}

// ── absorbed from sidecar_http.go ──

// SidecarResponse is one raw HTTP response.
// SidecarResponse is one raw sidecar HTTP response.
type SidecarResponse struct {
	Status int
	Body   []byte
}

// postJSON performs the canonical sidecar POST (JSON in, JSON out).
func PostJSON(ctx context.Context, client *http.Client, url string, payload any) (*SidecarResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sidecar marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("sidecar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("sidecar read: %w", err)
	}
	return &SidecarResponse{Status: resp.StatusCode, Body: body}, nil
}

// visualBatchEnvelope is the canonical sidecar batch response
// (VECTOR-001 envelope parity: model, model_version, dimensions, count).
type visualBatchEnvelope struct {
	Embeddings   [][]float64 `json:"embeddings"`
	Dimensions   int         `json:"dimensions"`
	Count        int         `json:"count"`
	Model        string      `json:"model"`
	ModelVersion string      `json:"model_version"`
}

// decodeVisualBatch validates the sidecar envelope against the registry
// spec and converts the embeddings to float32 slices (order-preserved).
func decodeVisualBatch(body []byte, spec VisualModelSpec) ([][]float32, error) {
	var parsed visualBatchEnvelope
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("visual embedder: decode: %w", err)
	}
	if parsed.Model == "" {
		return nil, fmt.Errorf("visual embedder: missing 'model' in sidecar response")
	}
	if parsed.Dimensions <= 0 {
		return nil, fmt.Errorf("visual embedder: missing or invalid 'dimensions' in sidecar response")
	}
	if parsed.Count != len(parsed.Embeddings) {
		return nil, fmt.Errorf("visual embedder: count=%d but len(embeddings)=%d", parsed.Count, len(parsed.Embeddings))
	}
	if !visualModelNameMatches(parsed.Model, spec.ModelID) {
		return nil, fmt.Errorf("%w: sidecar returned %q, registry pins %q", ErrVisualModelIdentityMismatch, parsed.Model, spec.ModelID)
	}
	if parsed.Dimensions != spec.Dim {
		return nil, fmt.Errorf("%w: sidecar returned %dd, registry pins %dd", ErrVisualDimMismatch, parsed.Dimensions, spec.Dim)
	}
	out := make([][]float32, 0, len(parsed.Embeddings))
	for i, vec := range parsed.Embeddings {
		if len(vec) != spec.Dim {
			return nil, fmt.Errorf("%w: embeddings[%d] is %dd", ErrVisualDimMismatch, i, len(vec))
		}
		v32 := make([]float32, len(vec))
		for j, v := range vec {
			v32[j] = float32(v)
		}
		out = append(out, v32)
	}
	if len(out) == 0 {
		return nil, ErrVisualEmptyResponse
	}
	return out, nil
}

// visualModelNameMatches compares a sidecar-returned model name against
// the registry identity, accepting an upstream vendor prefix on either
// side (e.g. "google/siglip-so400m-patch14-384" vs
// "siglip-so400m-patch14-384").
func visualModelNameMatches(sidecarModel, registryModel string) bool {
	return modelBaseNameOf(sidecarModel) == modelBaseNameOf(registryModel)
}

func modelBaseNameOf(modelID string) string {
	for i := len(modelID) - 1; i >= 0; i-- {
		if modelID[i] == '/' {
			return modelID[i+1:]
		}
	}
	return modelID
}

func TruncateForLog(b []byte) string {
	const max = 4096
	if len(b) > max {
		return string(b[:max])
	}
	return string(b)
}
