// Package app — books/content service bundle construction (Wave 15 PR4d-final).
//
// Extracted from compose_media.go per architecture/current.yaml::Wave 15
// pending #1. See build_bundles_voiceover.go for the rationale on
// build_bundles_<capability>.go as the canonical target and the
// private-helper (lowercase `build*`) naming convention.
package app

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildBooksService creates the books processing service.
//
// PR4-H (June 2026): the SetDriveUploader setter was removed; driveUploader
// is now wired via the books.NewService constructor.
func buildBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, driveUploader *drive.Uploader, voiceoverSvc *voiceover.Service) *books.Service {
	booksSvc := books.NewService(
		&books.Config{
			Enabled:       cfg.Books.Enabled,
			ScriptPath:    cfg.Books.ScriptPath,
			PythonBin:     cfg.Books.PythonBin,
			DriveFolderID: cfg.Drive.BooksFolder(),
		},
		dbs.main.DB, cfg.Drive.BooksFolder(), log,
		voiceoverSvc, driveUploader,
	)
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc
}
