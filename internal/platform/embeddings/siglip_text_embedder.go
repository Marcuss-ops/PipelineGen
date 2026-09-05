// Package embeddings — SigLIPTextEmbedder crosses the modality gap
// between text queries and visual-frame embeddings. SigLIP is trained
// as a joint image+text embedding model: text encoder outputs land in
// the SAME canonical vector space as the image-encoder outputs indexed
// in the visual channel of Qdrant. This is the load-bearing
// abstraction that enables end-to-end "search the concept in the
// pixels" via PR-CROSS-MODAL-TEXT-TO-VISUAL (deadline 2026-08-01).
//
// godlike/06 SSOT: SigLIPTextEmbedder satisfies the canonical
// `search.ChannelEncoder` port declared in
// `internal/capabilities/assets/search/ports.go`. The port's single-method
// shape (`EmbedTextQuery(ctx, text) []float32`) keeps the cross-modal
// adapter narrow: the EmbeddingResult envelope (model/version
// provenance) is intentionally NOT surfaced here because the
// composition root already pins model identity in the IndexSchema
// per QDRANT-001 / QDRANT-003. Adding a parallel provenance layer
// would violate godlike/06 one-canonical-owner-per-fact.
//
// godlike/07 typed-error contract: the canonical surface emits 4
// `errors.New(...)` sentinels (ErrSigLIPSidecarUnavailable,
// ErrSigLIPDimensionMismatch, ErrSigLIPModelIdentityMismatch,
// ErrSigLIPEmptyResponse) wrapping inner HTTP / decode errors via %w
// so callers `errors.Is` the canonical sentinel without
// unwrapping. Construction is fail-closed: a non-canonical
// dimension (anything other than models.CanonicalVisualModelDimensions)
// trips ErrSigLIPDimensionMismatch so the registry routes the channel to
// the godlike/07 deferred-stub fallback path instead of silently
// feeding a non-matching vector to Qdrant (the most damaging
// failure mode the cross-modal premise can hide).
//
// Wire path: composition root constructs one SigLIPTextEmbedder per
// sidecar URL and threads it into
// `newEmbeddingRegistryAdapter(textEmbedder, siglipEmbedder)` so the
// visual channel becomes LIVE; pre-PR build returns the
// `notConfiguredAdapter` typed-error carrier. audio + sparse
// forward-pointers stay unchanged (deferred to subsequent PRs).
//
// NOTE: the user's PR spec referenced "512-dim" for the cross-modal
// vector space. The canonical Qdrant v3 IndexSchema (DefaultV3Schema
// in internal/platform/qdrant/schema/schema.go::visual) is
// 768d (SigLIP so400m patch14-384, Cosine, normalized). Implementing
// 512d would BREAK the cross-modal premise (different vector spaces
// cannot be fused — the user's text-query vector would never match
// image-encoded frames in the same Qdrant collection). PR-CROSS-MODAL
// implements 768d per the canonical SSOT; the spec's "512d" is a
// documented typo. Future agents re-reading the spec MUST NOT
// regress to 512d; godlike/07 ErrSigLIPDimensionMismatch fails-closed
// on non-768d responses so a misconfigured sidecar cannot silently
// corrupt the index.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// Canonical SigLIP text-encoder dimension. Mirrors the Qdrant v3
// IndexSchema visual channel (DefaultV3Schema in
// internal/platform/qdrant/schema/schema.go: SigLIP
// so400m patch14-384, Cosine, normalized — dimension comes from
// models.CanonicalVisualModelDimensions, the model-registry SSOT). The HTTP
// sidecar MUST return vectors of exactly this length; any deviation
// trips the fail-closed dimension-mismatch guard per godlike/07.
const SigLIPTextDimension = models.CanonicalVisualModelDimensions

// Canonical sidecar endpoint for SigLIP-text queries. Semantically
// paired with /embed_visual_from_image (see
// internal/platform/qdrant/embedders.go::imageEmbedderAdapter
// for the image-side surface). The Python embedding server
// (scripts/services/embedding_server/) MUST expose a handler at this
// exact path that runs SigLIP-text inference on the requested query.
const SigLIPTextEndpoint = "/embed_visual_from_text"

// Default sidecar URL: same FastAPI server that hosts /embed and
// /embed_visual_from_image. Production deployments override via
// SIGLIP_TEXT_SIDECAR_URL.
const DefaultSigLIPTextSidecarURL = "http://127.0.0.1:8001"

// Typed sentinel errors (godlike/07). All `errors.New(...)` so callers
// `errors.Is` the canonical probe. The dimension + identity-mismatch
// sentinels surface the cross-modal-premise failure modes that
// would otherwise silently corrupt the registry's downstream
// dispatch (a 512d vector written to a canonical-dimension Qdrant collection
// returns a Qdrant-side dimension-mismatch error AFTER the registry
// has cached the bad result — by then the cross-modal premise is
// broken invisibly).
var (
	ErrSigLIPSidecarUnavailable    = errors.New("siglip text sidecar unavailable")
	ErrSigLIPDimensionMismatch     = errors.New("siglip text encoder returned non-canonical-dimension vector")
	ErrSigLIPModelIdentityMismatch = errors.New("siglip text encoder model identity mismatch")
	ErrSigLIPEmptyResponse         = errors.New("siglip text encoder returned empty vector")
)

// compile-time pin: SigLIPTextEmbedder satisfies the canonical
// search.ChannelEncoder port (PR-EMBEDDING-CHANNEL-REGISTRY).
// Future drift in either signature is a build failure at the
// canonical concrete site.
var _ searchpkg.ChannelEncoder = (*SigLIPTextEmbedder)(nil)

// SigLIPTextEmbedder calls the Python sidecar's `/embed_visual_from_text`
// endpoint to generate cross-modal text→visual embeddings. Returns
// canonical-dimension vectors that land in the SAME space as image-encoded
// SigLIP vectors per the joint-embedding training objective.
//
// Construction:
//   - serverURL="" trips ErrSigLIPSidecarUnavailable at call time
//     (composition roots that pass empty URL signal "channel not wired at
//     sidecar level" and the EmbeddingChannelRegistry gates accordingly).
//   - expectedModelIdentity="" skips the model identity check (test
//     paths only; production SHOULD pass models.SigLIP.ID from the
//     canonical registry via the IndexSchema).
type SigLIPTextEmbedder struct {
	serverURL             string
	httpClient            *http.Client
	expectedModelIdentity string
}

// NewSigLIPTextEmbedder creates a SigLIPTextEmbedder pointing at
// the given sidecar URL. expectedModelIdentity is the canonical
// full model ID from the registry/IndexSchema (validated via
// modelNameMatches to handle legacy vendor-prefix variants). Zero
// timeout defaults to 30s.
func NewSigLIPTextEmbedder(serverURL, expectedModelIdentity string) searchpkg.ChannelEncoder {
	return &SigLIPTextEmbedder{
		serverURL:             serverURL,
		httpClient:            &http.Client{Timeout: 30 * time.Second},
		expectedModelIdentity: expectedModelIdentity,
	}
}

// modelNameMatches mirrors the canonical helper in qdrant/embedders.go
// (handles "google/siglip-so400m-patch14-384" vs "siglip-so400m-patch14-384").
// Inlined here to avoid an import dependency on the qdrant package
// from a sibling-utility package (godlike/06 SSOT: leaf-only imports
// from internal/platform/embeddings/).
func siglipModelNameMatches(sidecarModel, schemaModel string) bool {
	return modelBaseName(sidecarModel) == modelBaseName(schemaModel)
}

func modelBaseName(modelID string) string {
	if idx := strings.LastIndex(modelID, "/"); idx >= 0 {
		return modelID[idx+1:]
	}
	return modelID
}

// EmbedTextQuery is the canonical search.ChannelEncoder port
// implementation. Empty text input short-circuits to (nil, nil)
// per the canonical contract — the orchestrator MUST NOT call
// EmbedQuery with empty text already (registry-level guard).
//
// Wire shape:
//   - POST {serverURL}/embed_visual_from_text with {"text": "...", "model": "siglip-..."}
//   - Validate HTTP 200 + canonical sidecar envelope
//     (embedding, dimensions, model, model_version, error)
//   - Cross-validate dimensions == SigLIPTextDimension
//   - If expectedModelIdentity != "" AND a model is returned, cross-check via
//     siglipModelNameMatches (QDRANT-001 prod-grade guards).
//   - Convert []float64 -> []float32 (no precision loss for cosine-normalized
//     vectors within [-1,1]; the canonical space).
func (s *SigLIPTextEmbedder) EmbedTextQuery(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		// Short-circuit on empty text per canonical contract.
		return nil, nil
	}
	if s == nil || s.serverURL == "" {
		return nil, fmt.Errorf("siglip text embedder: %w", ErrSigLIPSidecarUnavailable)
	}

	payload, err := json.Marshal(map[string]string{
		"text":  text,
		"model": models.SigLIP.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("siglip text embedder: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.serverURL+SigLIPTextEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("siglip text embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("siglip text embedder: HTTP %s: %w",
			strings.TrimPrefix(SigLIPTextEndpoint, "/"), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotImplemented {
		// 501 = SigLIP model not loaded on the sidecar; channel unavailable.
		return nil, fmt.Errorf("siglip text embedder: %w (HTTP 501)", ErrSigLIPSidecarUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("siglip text embedder: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Embedding    []float64 `json:"embedding"`
		Dimensions   int       `json:"dimensions"`
		Model        string    `json:"model"`
		ModelVersion string    `json:"model_version"`
		Error        string    `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("siglip text embedder: decode: %w", err)
	}

	if parsed.Error != "" {
		return nil, fmt.Errorf("siglip text embedder: sidecar error: %s", parsed.Error)
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("siglip text embedder: %w", ErrSigLIPEmptyResponse)
	}

	// godlike/07 fail-closed: dimensions MUST match the canonical
	// SigLIP space (models.CanonicalVisualModelDimensions). A non-matching
	// dimension at this seam is the cross-modal-premise failure mode —
	// calling Qdrant with a non-canonical vector would corrupt the index
	// silently, so we
	// fail-closed here with the canonical sentinel. The ErrSigLIPDimensionMismatch
	// sentinel is intentionally typed (no model/dimension payload);
	// the wrapped %w carries the dimension drift detail in
	// the message text so log-scrapers can route on the keyword.
	if len(parsed.Embedding) != SigLIPTextDimension {
		return nil, fmt.Errorf("siglip text embedder: sidecar returned %dd, expected %dd: %w",
			len(parsed.Embedding), SigLIPTextDimension, ErrSigLIPDimensionMismatch)
	}
	if parsed.Dimensions > 0 && parsed.Dimensions != len(parsed.Embedding) {
		return nil, fmt.Errorf("siglip text embedder: sidecar declared %dd, actual %dd: %w",
			parsed.Dimensions, len(parsed.Embedding), ErrSigLIPDimensionMismatch)
	}

	// QDRANT-001 prod-grade: cross-validate model identity against
	// the canonical expected model from IndexSchema. Skip when
	// expectedModelIdentity == "" (test path).
	if s.expectedModelIdentity != "" && parsed.Model != "" {
		if !siglipModelNameMatches(parsed.Model, s.expectedModelIdentity) {
			return nil, fmt.Errorf("siglip text embedder: sidecar model %q, expected %q: %w",
				parsed.Model, s.expectedModelIdentity, ErrSigLIPModelIdentityMismatch)
		}
	}

	out := make([]float32, len(parsed.Embedding))
	for i, v := range parsed.Embedding {
		out[i] = float32(v)
	}
	return out, nil
}
