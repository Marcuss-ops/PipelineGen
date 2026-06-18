package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type Scanner struct {
	repo *Repository
	log  *zap.Logger
}

func NewScanner(repo *Repository, log *zap.Logger) *Scanner {
	return &Scanner{repo: repo, log: log}
}

func (s *Scanner) RequeueExpiredLeases(ctx context.Context) error {
	results, err := s.repo.RequeueExpiredLeases(ctx, time.Now(), 1000)
	if err != nil {
		s.log.Error("failed to requeue expired leases", zap.Error(err))
		return err
	}
	s.log.Info("requeued expired leases", zap.Int("count", len(results)))
	return nil
}

func (s *Scanner) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.log.Info("starting lease scanner", zap.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			s.log.Info("stopping lease scanner")
			return
		case <-ticker.C:
			if err := s.RequeueExpiredLeases(ctx); err != nil {
				s.log.Error("lease scan failed", zap.Error(err))
			}
		}
	}
}
