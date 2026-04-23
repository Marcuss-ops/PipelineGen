package job

import (
	"errors"
	"sync"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

var (
	ErrJobNotFound      = errors.New("job not found")
	ErrJobAlreadyExists = errors.New("job already exists")
	ErrInvalidJobStatus = errors.New("invalid job status transition")
	ErrQueueFull        = errors.New("job queue is full")
)

// Service provides job management business logic
type Service struct {
	storage       StorageInterface
	cfg           *config.Config
	mu            sync.RWMutex
	queue         *models.Queue
	newJobsPaused bool
}
