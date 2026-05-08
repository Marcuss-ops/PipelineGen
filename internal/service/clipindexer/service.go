package clipindexer

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// Config holds clipindexer configuration
type Config struct {
	Enabled      bool   `yaml:"enabled"`
	ScriptPath   string `yaml:"script_path"`
	PythonBin    string `yaml:"python_bin"`
	AutoIndexAfterArtlist bool `yaml:"auto_index_after_artlist"`
}

// DefaultConfig returns default clipindexer config
func DefaultConfig() *Config {
	return &Config{
		Enabled:                true,
		ScriptPath:             "scripts/index_clips.py",
		PythonBin:              "python3",
		AutoIndexAfterArtlist:  true,
	}
}

// Service provides clip indexing functionality
type Service struct {
	db     *sql.DB
	cfg    *Config
	log    *zap.Logger
}

// NewService creates a new clipindexer service
func NewService(cfg *Config, db *sql.DB, log *zap.Logger) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Service{
		db:  db,
		cfg: cfg,
		log: log,
	}
}

// IndexClip indexes a single clip by ID
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		s.log.Debug("clipindexer disabled, skipping", zap.String("clip_id", clipID))
		return nil
	}

	// Get clip info from DB
	var name, localPath string
	err := s.db.QueryRowContext(ctx, "SELECT name, local_path FROM clips WHERE id = ?", clipID).Scan(&name, &localPath)
	if err != nil {
		return fmt.Errorf("failed to get clip info: %w", err)
	}

	// Build command arguments
	args := []string{s.cfg.ScriptPath}

	if name != "" {
		args = append(args, "--clip-name", name)
	}
	if localPath != "" {
		args = append(args, "--clip-path", localPath)
	}
	args = append(args, "--clip-id", clipID)

	// Execute Python script
	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.cfg.ScriptPath)

	s.log.Info("indexing clip", zap.String("clip_id", clipID), zap.Strings("args", args))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to index clip %s: %w, output: %s", clipID, err, strings.TrimSpace(string(output)))
	}

	s.log.Info("clip indexed successfully", zap.String("clip_id", clipID))
	return nil
}

// IndexRunItems indexes multiple clips from a run
func (s *Service) IndexRunItems(ctx context.Context, items []map[string]interface{}) error {
	if !s.cfg.Enabled {
		return nil
	}

	for _, item := range items {
		clipID, _ := item["clip_id"].(string)
		if clipID == "" {
			continue
		}
		if err := s.IndexClip(ctx, clipID); err != nil {
			s.log.Warn("failed to index clip", zap.String("clip_id", clipID), zap.Error(err))
		}
	}
	return nil
}

// IsEnabled returns whether the service is enabled
func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
}
