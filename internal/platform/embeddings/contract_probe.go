// Package embeddings contains concrete adapters for the embedding sidecar.
package embeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// ContractProbe reads the runtime embedding contract from the Python sidecar.
type ContractProbe struct {
	serverURL  string
	httpClient *http.Client
}

func NewContractProbe(serverURL string) *ContractProbe {
	return &ContractProbe{serverURL: serverURL, httpClient: &http.Client{Timeout: 5 * time.Second}}
}

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
	ContractHash            string `json:"contract_hash"`
}

// Fetch fails closed when the sidecar is unreachable, incomplete, or predates
// the /contract endpoint.
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: read response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: sidecar %q does not expose /contract", p.serverURL)
	}
	if resp.StatusCode != http.StatusOK {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var envelope contractEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: decode response: %w", err)
	}
	contract := coreembedding.Contract{
		ContractVersion: envelope.ContractVersion, ModelID: envelope.ModelID, ModelRevision: envelope.ModelRevision,
		Dimension: envelope.Dimension, Normalization: envelope.Normalization, Distance: envelope.Distance,
		QueryPrefix: envelope.QueryPrefix, DocumentPrefix: envelope.DocumentPrefix, SemanticDocumentVersion: envelope.SemanticDocumentVersion,
	}
	if contract.ModelID == "" || contract.Dimension <= 0 {
		return coreembedding.Contract{}, fmt.Errorf("embedding contract probe: incomplete contract (model_id=%q dimension=%d)", contract.ModelID, contract.Dimension)
	}
	// A self-consistent hash is not sufficient: a sidecar could report
	// another model and hash that alternate contract correctly. The
	// observed identity must also match the registry-backed canonical
	// contract.
	if contract.ModelID != models.E5.ID || !contract.Equal(coreembedding.CanonicalText) || envelope.ContractHash == "" || envelope.ContractHash != contract.Hash() {
		return coreembedding.Contract{}, &coreembedding.MismatchError{
			Component:    coreembedding.ComponentSidecar,
			Expected:     coreembedding.CanonicalText,
			Got:          contract,
			ExpectedHash: coreembedding.CanonicalText.Hash(),
			GotHash:      envelope.ContractHash,
		}
	}
	return contract, nil
}
