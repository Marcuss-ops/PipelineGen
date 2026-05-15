package sketchfab

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"go.uber.org/zap"
	"velox/go-master/pkg/config"
	"velox/go-master/internal/repository/sketchfab"
)

type Service struct {
	cfg     *config.Config
	repo    *sketchfab.Repository
	dbPath  string
	log     *zap.Logger
}

func NewService(cfg *config.Config, repo *sketchfab.Repository, dbPath string, log *zap.Logger) *Service {
	return &Service{
		cfg:    cfg,
		repo:   repo,
		dbPath: dbPath,
		log:    log,
	}
}

// SearchLive searches Sketchfab via Python script and updates the local DB
func (s *Service) SearchLive(ctx context.Context, query string) error {
	token := s.cfg.External.SketchfabConfig.APIToken
	if token == "" {
		return fmt.Errorf("sketchfab API token not configured")
	}

	scriptPath := filepath.Join("scripts", "sketchfab_client.py")
	
	args := []string{
		scriptPath,
		"--db", s.dbPath,
		"--token", token,
		"--search", query,
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		s.log.Error("sketchfab search failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("sketchfab search script failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// GetDownloadURL requests a temporary download link for a model
func (s *Service) GetDownloadURL(ctx context.Context, uid string) error {
	token := s.cfg.External.SketchfabConfig.APIToken
	if token == "" {
		return fmt.Errorf("sketchfab API token not configured")
	}

	scriptPath := filepath.Join("scripts", "sketchfab_client.py")
	
	args := []string{
		scriptPath,
		"--db", s.dbPath,
		"--token", token,
		"--download", uid,
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		s.log.Error("sketchfab download request failed", zap.Error(err), zap.String("output", string(output)))
		return fmt.Errorf("sketchfab download script failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// ListModels retrieves models from the local database
func (s *Service) ListModels(ctx context.Context, query string) ([]*sketchfab.Model3D, error) {
	return s.repo.SearchModels(ctx, query)
}
