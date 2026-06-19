// Package outbox provides the transactional outbox pattern for atomic
// media_assets UPSERT + outbox_events INSERT operations.
//
// The canonical ingestion entry point is Dispatcher.EnqueueAndIndex,
// which guarantees that the metadata write and the outbox_events row
// are committed atomically with no orphan jobs or orphan embeddings.
package outbox

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
)

// TxManager is the minimal transaction surface required by Dispatcher.
// Manager (below) is the production implementation backed by *sql.DB.
// Tests inject txMgrNoop or other fakes.
type TxManager interface {
	InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error
	DB() *sql.DB
}

// Manager wraps a *sql.DB and provides transactional helpers.
type Manager struct {
	db  *sql.DB
	log *zap.Logger
}

// NewManager creates a new transaction manager.
func NewManager(db *sql.DB, log *zap.Logger) *Manager {
	return &Manager{db: db, log: log}
}

// DB returns the underlying database connection.
func (m *Manager) DB() *sql.DB {
	return m.db
}

// InTransaction executes fn inside a transaction. If fn returns an error
// (or panics), the transaction is rolled back. On success, the transaction
// is committed.
func (m *Manager) InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			m.log.Warn("rollback failed", zap.Error(rbErr))
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
