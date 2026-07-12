// Package operations — generation_submission_service.go owns the small
// orchestration shell for the canonical submission flow. Validation,
// idempotency decisions, persistence, and ID generation live in focused files
// in the same package.
package operations

import "context"

// Submit validates the request, serializes submissions for SQLite consistency,
// resolves idempotency, and commits a fresh submission when required.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	if err := validateSubmitRequest(req); err != nil {
		return nil, err
	}

	s.submitMu.Lock()
	defer s.submitMu.Unlock()

	prior, replay, err := s.resolveSubmission(ctx, req)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		return replay, nil
	}
	return s.persistSubmission(ctx, req, prior)
}
