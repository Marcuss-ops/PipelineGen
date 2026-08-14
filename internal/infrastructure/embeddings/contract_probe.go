// Package embeddings — contract_probe.go reads the runtime embedding
// contract from the Python sidecar for the boot-time handshake.
//
// The sidecar exposes GET /contract with the canonical contract fields
// (model id, revision, dimension, normalization, distance, prefixes). The
// boot gate (internal/kernel/embedding.Verify) compares this runtime contract
// against the canonical EmbeddingContract SSOT, the Qdrant active collection
// metadata, and the query embedder. This probe is the "embedding sidecar
// runtime" leg of that handshake.
//
// Fail-closed: a sidecar that predates /contract (HTTP 404) is an explicit
// error — we do NOT silently reconstruct a partial contract from /embed and
// stamp a false green. The operator must upgrade the sidecar.
package embeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// ContractProbe reads the runtime embedding contract from the Python sidecar.
type ContractProbe struct {
	serverURL  string
	httpClient *http.Client
}

// NewContractProbe creates a ContractProbe pointing at the given sidecar URL.
func NewContractProbe(serverURL string) *ContractProbe {
	return &ContractProbe{
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// contractEnvelope is the GET /contract wire shape returned by
// scripts/services/embedding_server/text.py.
type contractEnvelope struct {
	ContractVersion         string `json:"contract_version"`
	ModelID                 string `json:"model_id"`
	ModelRevision           string `json:"model_revision"`
	Dimension               int    `json:"dimension"`
	Normalization           string `json:"normalization"`
	Distance                string `json:"distance"`
	QueryPrefix             string `json:"query_prefix"`
	DocumentPrefix          string `json:"document_prefix"`
	SemanticDocumentVersion string `json:"semantic_document_version"`
}

// Fetch returns the sidecar's runtime contract. It fails closed when the
// sidecar is unreachable, unconfigured, or predates the /contract endpoint.
func (p *ContractProbe) Fetch(ctx context.Context) (coreembedding.Contract, error) {
	if p == nil || p.serverURL == "" {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: sidecar URL not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.serverURL+"/contract", nil)
	if err != nil {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: build request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if readErr != nil {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: read response: %w", readErr)
	}
	if resp.StatusCode == http.StatusNotFound {
		return coreembedding.Contract{}, fmt.Errorf(
			"embedding contract probe: sidecar %q does not expose /contract (HTTP 404); upgrade the embedding sidecar to the version that reports the canonical contract", p.serverURL)
	}
	if resp.StatusCode != http.StatusOK {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var envelope contractEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: decode response: %w", err)
	}

	contract := coreembedding.Contract{
		ContractVersion:         envelope.ContractVersion,
		ModelID:                 envelope.ModelID,
		ModelRevision:           envelope.ModelRevision,
		Dimension:               envelope.Dimension,
		Normalization:           envelope.Normalization,
		Distance:                envelope.Distance,
		QueryPrefix:             envelope.QueryPrefix,
		DocumentPrefix:          envelope.DocumentPrefix,
		SemanticDocumentVersion: envelope.SemanticDocumentVersion,
	}
	if contract.ModelID == "" || contract.Dimension <= 0 {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: sidecar returned an incomplete contract (model_id=%q dimension=%d)", contract.ModelID, contract.Dimension)
	}
	return contract, nil
}
