package app

import (
	"go.uber.org/zap"
)

func runLegacyClipMigrations(dbs *databases, log *zap.Logger) error {
	// TODO remove after all deployed DBs are confirmed migrated.
	log.Info("legacy clip migration skipped; database already expected to be migrated")
	return nil
}
