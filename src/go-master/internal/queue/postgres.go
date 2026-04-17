package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/pkg/models"
)

// PostgresQueue implementa l'interfaccia Queue usando PostgreSQL.
// Utilizza SELECT ... FOR UPDATE SKIP LOCKED per una gestione efficiente e sicura della coda.
type PostgresQueue struct {
	db *sql.DB
}

// NewPostgresQueue crea una nuova istanza di coda Postgres.
func NewPostgresQueue(db *sql.DB) *PostgresQueue {
	return &PostgresQueue{db: db}
}

func (q *PostgresQueue) Backend() Backend {
	return BackendPostgres
}

// Publish inserisce un nuovo job nella tabella dei job (che funge da coda).
func (q *PostgresQueue) Publish(ctx context.Context, msg Message) error {
	// In questo sistema, i job sono già persistiti in Postgres.
	// Se il job non esiste, potremmo crearlo o aggiornare lo stato a 'pending'.
	query := `UPDATE jobs SET status = 'pending', updated_at = NOW() WHERE id = $1`
	_, err := q.db.ExecContext(ctx, query, msg.JobID)
	return err
}

// LeaseNext preleva il prossimo job disponibile usando SKIP LOCKED per evitare conflitti.
func (q *PostgresQueue) LeaseNext(ctx context.Context, consumer string) (*Lease, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// SELECT FOR UPDATE SKIP LOCKED è il pattern ideale per code su Postgres
	query := `
		SELECT id, type, payload 
		FROM jobs 
		WHERE status = 'pending' 
		ORDER BY priority DESC, created_at ASC 
		LIMIT 1 
		FOR UPDATE SKIP LOCKED`
	
	var msg Message
	var payloadJSON []byte
	err = tx.QueryRowContext(ctx, query).Scan(&msg.JobID, &msg.Topic, &payloadJSON)
	
	if err == sql.ErrNoRows {
		return nil, nil // Coda vuota
	}
	if err != nil {
		return nil, err
	}

	// Aggiorna lo stato a 'running' e assegna al consumer (worker)
	leaseToken := fmt.Sprintf("lease_%d", time.Now().UnixNano())
	updateQuery := `
		UPDATE jobs 
		SET status = 'running', worker_id = $1, updated_at = NOW(), started_at = NOW() 
		WHERE id = $2`
	
	_, err = tx.ExecContext(ctx, updateQuery, consumer, msg.JobID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	msg.Payload = payloadJSON
	msg.Consumer = consumer
	msg.Attempt = 1

	return &Lease{
		Message:    msg,
		LeaseToken: leaseToken,
	}, nil
}

// Ack segna il job come completato.
func (q *PostgresQueue) Ack(ctx context.Context, lease Lease) error {
	query := `UPDATE jobs SET status = 'completed', completed_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := q.db.ExecContext(ctx, query, lease.Message.JobID)
	return err
}

// Nack rimette il job in coda o lo segna come fallito.
func (q *PostgresQueue) Nack(ctx context.Context, lease Lease, requeue bool) error {
	status := "failed"
	if requeue {
		status = "pending"
	}
	
	query := `UPDATE jobs SET status = $1, updated_at = NOW() WHERE id = $2`
	_, err := q.db.ExecContext(ctx, query, status, lease.Message.JobID)
	return err
}

func (q *PostgresQueue) Close() error {
	// Il DB è gestito esternamente dallo storage factory
	return nil
}
