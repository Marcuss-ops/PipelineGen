// Service is the admin-only hard-delete entry point. It is exported
// from internal/application/assets/deletion/admin (deliberately a
// subpackage) so production HTTP handlers and use cases cannot reach
// it without explicitly importing the admin namespace. CI gate
// scripts/ci-architectural-checks.sh::Check 5 enforces the
// production-side ban.
//
// TODO 5 (QDRANT-002-B, June 2026): HardDelete of media_assets rows
// is admin-only because the physical row drop is THE point of no
// return — once the row is gone, any handler that tried to look it
// up (or any Qdrant vector that referenced its id) becomes
// unrecoverable in SQLite. The 3-condition gate (DELETED lifecycle,
// Qdrant absent, zero pending outbox) lives in AssetVerifier.
//
// Composition: nothing is automatically wired. Production code paths
// (cmd/server, dispatcher, REST handlers) MUST NOT reach this
// type. The only legitimate caller is cmd/admin/hard_delete.go
// (operator CLI).
package admin

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// HardDeleteDispatcher is the narrowed surface of outbox.Dispatcher
// this Service requires. The full outbox.Dispatcher exposes
// EnqueueAnd{Index,Delete,Restore,HardDelete}; this surface pins
// only the HardDelete path so a future mis-import cannot accidently
// route a restore call through the admin path.
type HardDeleteDispatcher interface {
	EnqueueAndHardDelete(ctx context.Context, assetID string) error
}

// Service is the admin-only HardDelete orchestrator. Method is
// called "HardDelete" so the operation name is obvious in operator
// logs; it is intentionally not called "Purge" to avoid confusion
// with eventual lower-level tools that might bypass the verifier
// gate.
type Service struct {
	verifier   AssetVerifier
	dispatcher HardDeleteDispatcher
	log        *zap.Logger
}

// NewService constructs the admin-only HardDelete service. The
// verifier MUST be non-nil (operational invariant: every call
// routes through AssetVerifier.Verify); a nil verifier is a wiring
// bug and produces a hard error here rather than silent fall-through.
// The dispatcher MAY be nil in read-only / dry-run modes (the
// DryRun option below) but for a real HardDelete it must be
// non-nil too.
func NewService(verifier AssetVerifier, dispatcher HardDeleteDispatcher, log *zap.Logger) (*Service, error) {
	if verifier == nil {
		return nil, errors.New("admin.NewService: verifier is required (HardDelete MUST gate on AssetVerifier)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		verifier:   verifier,
		dispatcher: dispatcher,
		log:        log,
	}, nil
}

// HardDeleteRequest is the operator input to HardDelete. DryRun
// causes the Service to run the verifier gate and report the
// outcome WITHOUT invoking the dispatcher (used by the admin CLI to
// preview eligibility before committing).
type HardDeleteRequest struct {
	AssetID string
	DryRun  bool
}

// HardDeleteResult is the structured outcome. VerifierReport is
// ALWAYS populated (from the gate); DryRun bool mirrors the request;
// DispatcherInvoked is true only when the actual purge ran.
type HardDeleteResult struct {
	AssetID           string
	VerifierReport    *VerifyReport
	DryRun            bool
	DispatcherInvoked bool
}

// HardDelete runs the gate + (if DryRun=false) the canonical atomic
// delete via dispatcher.EnqueueAndHardDelete. Returns the structured
// outcome + typed error.
//
// Error contract:
//
//   - nil error                     → purge succeeded (DryRun=false) OR
//     verifier is green (DryRun=true).
//
//   - errors.Is(err, ErrAssetVerifier)
//     → gate failed; VerifierReport
//     carries the RefusalReason and
//     which booleans were false.
//     Admin CLI maps this to a non-2xx
//     operator response.
//
//   - other errors                  → infrastructure failure in the
//     verifier itself (DB or Qdrant
//     client). VerifierReport may be
//     nil in this case.
func (s *Service) HardDelete(ctx context.Context, req HardDeleteRequest) (*HardDeleteResult, error) {
	if req.AssetID == "" {
		return nil, errors.New("admin.Service.HardDelete: AssetID is required")
	}
	if !req.DryRun && s.dispatcher == nil {
		return nil, errors.New("admin.Service.HardDelete: dispatcher is required when DryRun=false (admin CLI misconfiguration)")
	}

	report, verr := s.verifier.Verify(ctx, req.AssetID)
	if verr != nil {
		// Even on verifier refusal, the report carries the booleans
		// we computed before the gate failed (so DryRun preview
		// surfaces the same blockage information).
		if report != nil {
			s.log.Warn("admin.HardDelete: gate refused",
				zap.String("asset_id", req.AssetID),
				zap.String("refusal_reason", report.RefusalReason),
				zap.Bool("dry_run", req.DryRun))
		}
		return &HardDeleteResult{
			AssetID:           req.AssetID,
			VerifierReport:    report,
			DryRun:            req.DryRun,
			DispatcherInvoked: false,
		}, verr
	}

	if req.DryRun {
		s.log.Info("admin.HardDelete: dry-run PASS",
			zap.String("asset_id", req.AssetID))
		return &HardDeleteResult{
			AssetID:           req.AssetID,
			VerifierReport:    report,
			DryRun:            true,
			DispatcherInvoked: false,
		}, nil
	}

	if err := s.dispatcher.EnqueueAndHardDelete(ctx, req.AssetID); err != nil {
		return nil, fmt.Errorf("admin.Service.HardDelete: dispatcher.EnqueueAndHardDelete: %w", err)
	}
	s.log.Info("admin.HardDelete: purge committed",
		zap.String("asset_id", req.AssetID),
		zap.Int64("outbox_pending_at_purge", report.OutboxPendingCount))
	return &HardDeleteResult{
		AssetID:           req.AssetID,
		VerifierReport:    report,
		DryRun:            false,
		DispatcherInvoked: true,
	}, nil
}
