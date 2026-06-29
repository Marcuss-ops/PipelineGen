package delivery

import "context"

// Publisher is the canonical contract for uploading files to Google Drive.
// Every endpoint and every job that produces a file for Drive MUST go
// through this interface. The caller describes WHAT to publish via
// PublishRequest (DestinationKey + logical metadata); the Publisher
// resolves WHERE it lands on Drive (root folder + path hierarchy +
// folder creation + upload).
//
// The concrete implementation lives in
// internal/infrastructure/drive/publisher.go.
//
// Architecture rule (June 2026):
//
//	A caller that has a Publisher MUST NOT also hold a reference to
//	drive.Uploader or drive.DriveFolderManagerAdapter. The Publisher
//	is the single canal for all Drive writes.
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) (*PublishResult, error)
}
