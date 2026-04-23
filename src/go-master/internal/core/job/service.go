package job

import (
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

// NewService creates a new job service
func NewService(storage StorageInterface, cfg *config.Config) *Service {
	if cfg == nil {
		cfg = config.Get()
	}
	return &Service{
		storage:       storage,
		cfg:           cfg,
		queue:         &models.Queue{Jobs: []*models.Job{}, UpdatedAt: time.Now()},
		newJobsPaused: cfg.Jobs.NewJobsPaused,
	}
}
