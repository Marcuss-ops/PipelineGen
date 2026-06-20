package ports

import "context"

// DocumentOutput describes what a successful DocumentBuilder.Create
// returns. The transport maps this into the JSON response; the scriptgen
// module uses it post-generation to wire up notifications and store
// linkage.
type DocumentOutput struct {
	DocID  string `json:"doc_id"`
	DocURL string `json:"doc_url"`
	Title  string `json:"title"`
}

// DocumentBuilder is the local contract for "produce an output document
// given a Script". It is intentionally minimal — Drive-specific
// behaviour (folder selection, permission handling, idempotency) is the
// adapter's problem, not this module's.
type DocumentBuilder interface {
	Create(ctx context.Context, title, body string, folderID string) (*DocumentOutput, error)
}
