package drivecleanup

import "context"

// Service is a compatibility shim for legacy drive cleanup wiring.
// The real logic lives in the upload/drive and core maintenance layers.
type Service struct{}

type Result struct {
	Deleted int `json:"deleted"`
	Kept    int `json:"kept"`
}

func NewService() *Service { return &Service{} }

func (s *Service) Reconcile(ctx context.Context, source, rootFolderID string, dryRun bool) (*Result, error) {
	_ = ctx
	_ = source
	_ = rootFolderID
	_ = dryRun
	return &Result{}, nil
}
