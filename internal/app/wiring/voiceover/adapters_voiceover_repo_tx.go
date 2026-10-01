package voiceover

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

func (a *UseCaseRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	if rec == nil {
		return fmt.Errorf("UseCaseRepoAdapter.InsertTx: nil record")
	}
	return a.repo.InsertTx(ctx, tx, a.toInfraRecord(rec))
}

func (a *UseCaseRepoAdapter) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return fmt.Errorf("UseCaseRepoAdapter.DeleteByIDTx: empty id")
	}
	return a.repo.DeleteByIDTx(ctx, tx, id)
}

// FindByIdempotencyKeyTx runs the FASE 3 idempotency gate (July 2026)
// INSIDE the caller-owned tx. Scans voiceovers for an existing row
// with the same idempotency_key. Returns (matchedID, nil) when a
// match is found (idempotency gate fires); returns ("", sql.ErrNoRows)
// when no match exists (first-time run).
//
// Empty idempotencyKey short-circuits to ("", sql.ErrNoRows, nil) —
// the gate is intentionally skipped for pre-FASE-3 callers.
func (a *UseCaseRepoAdapter) FindByIdempotencyKeyTx(
	ctx context.Context,
	tx *sql.Tx,
	idempotencyKey string,
) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	if tx == nil {
		return "", fmt.Errorf("UseCaseRepoAdapter.FindByIdempotencyKeyTx: nil tx")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var matchedID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM voiceovers WHERE idempotency_key = ? LIMIT 1`,
		idempotencyKey,
	).Scan(&matchedID)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", fmt.Errorf("FindByIdempotencyKeyTx: scan: %w", err)
	}
	return matchedID, nil
}

func (a *UseCaseRepoAdapter) PreReadByID(ctx context.Context, id string) (*persistence.VoiceoverRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("UseCaseRepoAdapter.PreReadByID: empty id")
	}
	r, err := a.repo.PreReadByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("UseCaseRepoAdapter.PreReadByID: %w", err)
	}
	if r == nil {
		return nil, nil
	}
	return a.fromInfraRecord(r), nil
}

// timeutil import is used here for FormatRFC3339 fallback in
// InsertTx; the canonical timeutil location avoids bringing in
// time.UTC().Format() boilerplate per Adapter struct.
