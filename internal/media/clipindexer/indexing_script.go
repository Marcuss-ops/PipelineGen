package clipindexer

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// indexViaScript is the fallback path when the HTTP embedding server is
// unavailable. Invokes scripts/index_clips.py via python3 with the clip
// ID/name/local-path as CLI args, which performs the three embedding
// kinds in subprocess space.
func (s *Service) indexViaScript(ctx context.Context, clipID string) error {
	select {
	case globalScriptSem <- struct{}{}:
		defer func() { <-globalScriptSem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// local_path is a canonical column (migration 059).
	var name, localPath string
	err := s.db.QueryRowContext(ctx, "SELECT name, COALESCE(local_path, '') FROM media_assets WHERE id = ?", clipID).Scan(&name, &localPath)
	if err != nil {
		return fmt.Errorf("failed to get clip info: %w", err)
	}

	scriptName := filepath.Base(s.scriptPath)
	args := []string{scriptName}

	if s.dbPath != "" {
		args = append(args, "--db", s.dbPath)
	}
	if name != "" {
		args = append(args, "--clip-name", name)
	}
	if localPath != "" {
		args = append(args, "--clip-path", localPath)
	}
	args = append(args, "--clip-id", clipID)

	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.scriptPath)

	s.log.Info("indexing clip via script", zap.String("clip_id", clipID), zap.String("script", s.scriptPath))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to index clip %s: %w, output: %s", clipID, err, strings.TrimSpace(string(output)))
	}

	s.log.Info("clip indexed successfully via script", zap.String("clip_id", clipID))
	return nil
}
