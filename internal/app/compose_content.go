// Package app — books/content service construction (PR4.3, June 2026).
//
// Extracted from dependencies.go so composition helpers are split per
// capability (AGENTS.md Pattern 5). Called from BuildDomainBundle in
// composition.go.

package app

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// initBooksService creates the books processing service.
//
// PR4-H (June 2026): the SetDriveUploader setter was removed; driveUploader
// is now wired via the books.NewService constructor.
func initBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, driveUploader *drive.Uploader, voiceoverSvc *voiceover.Service) *books.Service {
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
