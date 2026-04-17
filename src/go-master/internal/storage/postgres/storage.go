package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"velox/go-master/internal/core/worker"
	"velox/go-master/pkg/models"
)

// Storage implementa StorageInterface usando PostgreSQL.
type Storage struct {
	db  *sql.DB
	dsn string
}

// NewStorage crea una nuova istanza di storage Postgres.
func NewStorage(dsn string) (*Storage, error) {
	if dsn == "" {
		return nil, errors.New("postgres dsn is required")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Configurazione pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &Storage{
		db:  db,
		dsn: dsn,
	}, nil
}

// GetDB restituisce l'istanza sql.DB (usata per la coda).
func (s *Storage) GetDB() *sql.DB {
	return s.db
}

// HealthCheck verifica la connessione al database.
func (s *Storage) HealthCheck() error {
	return s.db.Ping()
}

// Close chiude la connessione al database.
func (s *Storage) Close() error {
	return s.db.Close()
}

// === Job Operations ===

func (s *Storage) LoadQueue(ctx context.Context) (*models.Queue, error) {
	// In Postgres, la coda è una vista logica sulla tabella jobs.
	// Carichiamo tutti i job non terminali.
	query := `SELECT id, type, status, priority, project, video_name, created_at, updated_at, 
	                 started_at, completed_at, worker_id, payload, result, error, retries, 
					 retry_count, max_retries, progress, lease_expiry 
			  FROM jobs 
			  WHERE status NOT IN ('completed', 'failed', 'cancelled')
			  ORDER BY priority DESC, created_at ASC`
	
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	queue := &models.Queue{
		Jobs:      []*models.Job{},
		UpdatedAt: time.Now(),
	}

	for rows.Next() {
		var j models.Job
		var payloadJSON, resultJSON []byte
		err := rows.Scan(
			&j.ID, &j.Type, &j.Status, &j.Priority, &j.Project, &j.VideoName, &j.CreatedAt, &j.UpdatedAt,
			&j.StartedAt, &j.CompletedAt, &j.WorkerID, &payloadJSON, &resultJSON, &j.Error, &j.Retries,
			&j.RetryCount, &j.MaxRetries, &j.Progress, &j.LeaseExpiry,
		)
		if err != nil {
			return nil, err
		}
		
		json.Unmarshal(payloadJSON, &j.Payload)
		json.Unmarshal(resultJSON, &j.Result)
		
		queue.Jobs = append(queue.Jobs, &j)
	}

	return queue, nil
}

func (s *Storage) SaveQueue(ctx context.Context, queue *models.Queue) error {
	// Metodo bulk non atomico in Postgres se non con transazione. 
	// In Postgres solitamente salviamo i job singolarmente.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, j := range queue.Jobs {
		if err := s.saveJobTx(ctx, tx, j); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Storage) GetJob(ctx context.Context, id string) (*models.Job, error) {
	query := `SELECT id, type, status, priority, project, video_name, created_at, updated_at, 
	                 started_at, completed_at, worker_id, payload, result, error, retries, 
					 retry_count, max_retries, progress, lease_expiry 
			  FROM jobs WHERE id = $1`
	
	row := s.db.QueryRowContext(ctx, query, id)
	
	var j models.Job
	var payloadJSON, resultJSON []byte
	err := row.Scan(
		&j.ID, &j.Type, &j.Status, &j.Priority, &j.Project, &j.VideoName, &j.CreatedAt, &j.UpdatedAt,
		&j.StartedAt, &j.CompletedAt, &j.WorkerID, &payloadJSON, &resultJSON, &j.Error, &j.Retries,
		&j.RetryCount, &j.MaxRetries, &j.Progress, &j.LeaseExpiry,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal(payloadJSON, &j.Payload)
	json.Unmarshal(resultJSON, &j.Result)

	return &j, nil
}

func (s *Storage) SaveJob(ctx context.Context, j *models.Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.saveJobTx(ctx, tx, j); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Storage) saveJobTx(ctx context.Context, tx *sql.Tx, j *models.Job) error {
	payloadJSON, _ := json.Marshal(j.Payload)
	resultJSON, _ := json.Marshal(j.Result)

	query := `INSERT INTO jobs (
		id, type, status, priority, project, video_name, created_at, updated_at, 
		started_at, completed_at, worker_id, payload, result, error, retries, 
		retry_count, max_retries, progress, lease_expiry
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	ON CONFLICT (id) DO UPDATE SET
		status = EXCLUDED.status,
		priority = EXCLUDED.priority,
		updated_at = EXCLUDED.updated_at,
		started_at = EXCLUDED.started_at,
		completed_at = EXCLUDED.completed_at,
		worker_id = EXCLUDED.worker_id,
		payload = EXCLUDED.payload,
		result = EXCLUDED.result,
		error = EXCLUDED.error,
		retries = EXCLUDED.retries,
		retry_count = EXCLUDED.retry_count,
		progress = EXCLUDED.progress,
		lease_expiry = EXCLUDED.lease_expiry`

	_, err := tx.ExecContext(ctx, query,
		j.ID, j.Type, j.Status, j.Priority, j.Project, j.VideoName, j.CreatedAt, j.UpdatedAt,
		j.StartedAt, j.CompletedAt, j.WorkerID, payloadJSON, resultJSON, j.Error, j.Retries,
		j.RetryCount, j.MaxRetries, j.Progress, j.LeaseExpiry,
	)
	return err
}

func (s *Storage) DeleteJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM jobs WHERE id = $1", id)
	return err
}

func (s *Storage) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	// Implementazione di base del filtraggio
	query := `SELECT id, type, status, priority, project, video_name, created_at, updated_at, 
	                 started_at, completed_at, worker_id, payload, result, error, retries, 
					 retry_count, max_retries, progress, lease_expiry 
			  FROM jobs WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if filter.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, *filter.Status)
		argIdx++
	}

	if filter.Type != nil {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, *filter.Type)
		argIdx++
	}

	if filter.WorkerID != "" {
		query += fmt.Sprintf(" AND worker_id = $%d", argIdx)
		args = append(args, filter.WorkerID)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.Job
	for rows.Next() {
		var j models.Job
		var payloadJSON, resultJSON []byte
		err := rows.Scan(
			&j.ID, &j.Type, &j.Status, &j.Priority, &j.Project, &j.VideoName, &j.CreatedAt, &j.UpdatedAt,
			&j.StartedAt, &j.CompletedAt, &j.WorkerID, &payloadJSON, &resultJSON, &j.Error, &j.Retries,
			&j.RetryCount, &j.MaxRetries, &j.Progress, &j.LeaseExpiry,
		)
		if err != nil {
			return nil, err
		}
		json.Unmarshal(payloadJSON, &j.Payload)
		json.Unmarshal(resultJSON, &j.Result)
		jobs = append(jobs, &j)
	}

	return jobs, nil
}

func (s *Storage) LogJobEvent(ctx context.Context, event *models.JobEvent) error {
	query := `INSERT INTO job_events (job_id, event_type, message, created_at) 
	          VALUES ($1, $2, $3, $4)`
	_, err := s.db.ExecContext(ctx, query, event.JobID, event.Type, event.Message, event.Timestamp)
	return err
}

func (s *Storage) GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error) {
	query := `SELECT id, job_id, event_type, message, created_at 
	          FROM job_events WHERE job_id = $1 ORDER BY created_at DESC LIMIT $2`
	
	rows, err := s.db.QueryContext(ctx, query, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.JobEvent
	for rows.Next() {
		var e models.JobEvent
		err := rows.Scan(&e.ID, &e.JobID, &e.Type, &e.Message, &e.Timestamp)
		if err != nil {
			return nil, err
		}
		events = append(events, &e)
	}
	return events, nil
}

// === Worker Operations ===

func (s *Storage) LoadWorkers(ctx context.Context) (map[string]*models.Worker, error) {
	query := `SELECT id, status, hostname, ip_address, capabilities, metadata, 
	                 created_at, updated_at, last_heartbeat_at FROM workers`
	
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	workers := make(map[string]*models.Worker)
	for rows.Next() {
		var w models.Worker
		var capsJSON, metaJSON []byte
		err := rows.Scan(
			&w.ID, &w.Status, &w.Hostname, &w.IPAddress, &capsJSON, &metaJSON,
			&w.CreatedAt, &w.UpdatedAt, &w.LastHeartbeatAt,
		)
		if err != nil {
			return nil, err
		}
		json.Unmarshal(capsJSON, &w.Capabilities)
		json.Unmarshal(metaJSON, &w.Metadata)
		workers[w.ID] = &w
	}
	return workers, nil
}

func (s *Storage) SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, w := range workers {
		if err := s.saveWorkerTx(ctx, tx, w); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Storage) GetWorker(ctx context.Context, id string) (*models.Worker, error) {
	query := `SELECT id, status, hostname, ip_address, capabilities, metadata, 
	                 created_at, updated_at, last_heartbeat_at 
			  FROM workers WHERE id = $1`
	
	row := s.db.QueryRowContext(ctx, query, id)
	var w models.Worker
	var capsJSON, metaJSON []byte
	err := row.Scan(
		&w.ID, &w.Status, &w.Hostname, &w.IPAddress, &capsJSON, &metaJSON,
		&w.CreatedAt, &w.UpdatedAt, &w.LastHeartbeatAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(capsJSON, &w.Capabilities)
	json.Unmarshal(metaJSON, &w.Metadata)
	return &w, nil
}

func (s *Storage) SaveWorker(ctx context.Context, w *models.Worker) error {
	return s.saveWorkerTx(ctx, s.db, w)
}

// execer è un'interfaccia helper per accettare sql.DB o sql.Tx
type execer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func (s *Storage) saveWorkerTx(ctx context.Context, ex execer, w *models.Worker) error {
	capsJSON, _ := json.Marshal(w.Capabilities)
	metaJSON, _ := json.Marshal(w.Metadata)

	query := `INSERT INTO workers (
		id, status, hostname, ip_address, capabilities, metadata, created_at, updated_at, last_heartbeat_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (id) DO UPDATE SET
		status = EXCLUDED.status,
		hostname = EXCLUDED.hostname,
		ip_address = EXCLUDED.ip_address,
		capabilities = EXCLUDED.capabilities,
		metadata = EXCLUDED.metadata,
		updated_at = EXCLUDED.updated_at,
		last_heartbeat_at = EXCLUDED.last_heartbeat_at`

	_, err := ex.ExecContext(ctx, query,
		w.ID, w.Status, w.Hostname, w.IPAddress, capsJSON, metaJSON,
		w.CreatedAt, w.UpdatedAt, w.LastHeartbeatAt,
	)
	return err
}

func (s *Storage) DeleteWorker(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM workers WHERE id = $1", id)
	return err
}

func (s *Storage) SaveWorkerCommand(ctx context.Context, command *models.WorkerCommand) error {
	payloadJSON, _ := json.Marshal(command.Payload)
	query := `INSERT INTO worker_commands (id, worker_id, type, payload, created_at, acknowledged)
	          VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.db.ExecContext(ctx, query, command.ID, command.WorkerID, command.Type, payloadJSON, time.Now(), command.Acknowledged)
	return err
}

func (s *Storage) GetWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	query := `SELECT id, worker_id, type, payload, created_at, acknowledged 
	          FROM worker_commands WHERE worker_id = $1 AND acknowledged = false`
	
	rows, err := s.db.QueryContext(ctx, query, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []*models.WorkerCommand
	for rows.Next() {
		var cmd models.WorkerCommand
		var payloadJSON []byte
		err := rows.Scan(&cmd.ID, &cmd.WorkerID, &cmd.Type, &payloadJSON, &cmd.CreatedAt, &cmd.Acknowledged)
		if err != nil {
			return nil, err
		}
		json.Unmarshal(payloadJSON, &cmd.Payload)
		commands = append(commands, &cmd)
	}
	return commands, nil
}

func (s *Storage) AckWorkerCommand(ctx context.Context, commandID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE worker_commands SET acknowledged = true WHERE id = $1", commandID)
	return err
}

func (s *Storage) LoadRevokedWorkers(ctx context.Context) (map[string]bool, error) {
	// TODO: Tabella dedicata se necessario, o colonna in workers
	return map[string]bool{}, nil
}

func (s *Storage) SaveRevokedWorkers(ctx context.Context, revoked map[string]bool) error {
	return nil
}

func (s *Storage) LoadQuarantinedWorkers(ctx context.Context) (map[string]*worker.QuarantineInfo, error) {
	// TODO: Tabella dedicata
	return map[string]*worker.QuarantineInfo{}, nil
}

func (s *Storage) SaveQuarantinedWorkers(ctx context.Context, quarantined map[string]*worker.QuarantineInfo) error {
	return nil
}
