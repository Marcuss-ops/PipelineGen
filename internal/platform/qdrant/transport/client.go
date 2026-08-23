// Package qdrant is the typed HTTP surface for the Qdrant REST API.
//
// File layout (PR2 mechanical split, June 2026):
//
//	client.go                  Client type + NewClient + BaseURL + APIKey only
//	client_request.go          doJSON / prepareRequest / doRequest / DoWithHTTPClient
//	client_collections.go      Create / Get / List / Delete Collection
//	client_aliases.go          GetAliasTarget / UpdateAliases / CreateAlias / SwitchAlias
//	client_points.go           UpsertPoints / DeletePoints / CountPoints / DeletePayloadKeys
//	client_payload_indexes.go  CreatePayloadIndex
//	client_scroll.go           ScrollPoints
//	client_search.go           SearchPoints / HybridSearchPoints + shared query decoder
//	client_errors.go           parseError / parseErrorWith + op* label constants
//	client_snapshots.go        (doc-only marker — snapshot methods live in client_dr.go,
//	                            which was extracted by QDRANT-005C PR3 before PR2)
//	client_dr.go               Snapshot + OverwritePayload (pre-PR2 split, PR3 era)
//
// All methods stay on receiver *Client; no new interfaces or types
// were introduced. PR2 is purely a relocation pass — every method
// body and signature is preserved 1:1 against the pre-split client.go.
package transport

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// Client is a typed HTTP client for the Qdrant REST API.
// All Qdrant communication flows through this client.
type Client struct {
	baseURL    string
	apiKey     string // API key sent as X-Api-Key on every request (QDRANT-005 health probe relies on this)
	httpClient *http.Client
	log        *zap.Logger
}

// NewClient creates a Client with the configured timeout.
func NewClient(cfg *schema.Config, log *zap.Logger) *Client {
	if cfg == nil {
		cfg = schema.DefaultConfig()
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}
}

// BaseURL returns the configured Qdrant base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// APIKey returns the configured Qdrant API key (empty string if
// none). Exposed so the HealthProbe (QDRANT-005) and any future
// authenticated diagnostic endpoint can send X-Api-Key without
// round-tripping through private state.
func (c *Client) APIKey() string {
	if c == nil {
		return ""
	}
	return c.apiKey
}
