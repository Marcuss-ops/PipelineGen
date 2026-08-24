// Package persistence — legacy canonical-surface DTOs.
//
// These request/result types are retained ONLY for the canonical
// surfaces E2E test (tests/e2e/canonical_surfaces_e2e_test.go),
// which exercises the cross-pipeline envelope shape without writing
// through the old AssetPersistenceWriter port.
//
// New code MUST use AssetCommitter and CommitRequest.
package assets

import "time"

// PersistAndIndexRequest carries the canonical write contract for
// media_assets + outbox_events. Retained for E2E shape tests.
//
// Deprecated: use CommitRequest for new code.
type PersistAndIndexRequest struct {
	AssetID        string
	Source         string
	Name           string
	Filename       string
	MediaType      string
	ContentHash    string
	Description    string
	DriveFileID    string
	DriveLink      string
	DownloadLink   string
	LocalPath      string
	FolderID       string
	FolderPath     string
	LifecycleState string
	IndexState     string
	SearchText     string
	MetadataJSON   []byte
	EventCreatedAt time.Time
	Extra          map[string]any
}

// PersistAndIndexResult carries the output of a PersistAndIndex call.
// Retained for E2E shape tests.
//
// Deprecated: use CommitResult for new code.
type PersistAndIndexResult struct {
	EventKey     string
	PayloadJSON  []byte
	RowsAffected int64
}
