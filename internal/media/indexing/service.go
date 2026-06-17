package indexing

import (
	"go.uber.org/zap"
	"velox/go-master/internal/media/ingest"
)

type Service struct {
	log       *zap.Logger
	ingestSvc *ingest.Service
}

func NewService(log *zap.Logger) *Service {
	return &Service{log: log}
}

func (s *Service) SetIngestService(svc *ingest.Service) {
	s.ingestSvc = svc
}
