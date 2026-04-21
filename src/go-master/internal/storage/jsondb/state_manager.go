// Package jsondb implementa lo storage usando file JSON (compatibilità con Python)
package jsondb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
)

// StateManager implementa storage.StateManager con gestione locale dello stato
type StateManager struct {
	workerStore  *WorkerStore
	jobStore     *JobStore
	mutex        sync.RWMutex
	globalState  *storage.GlobalState
	locks        map[string]*localLock
	stateFile    string
	observers    []chan<- storage.StateChange
	observersMu  sync.RWMutex
}

// localLock implementa storage.Lock a livello locale
type localLock struct {
	name       string
	acquiredAt time.Time
	ttl        time.Duration
	mu         sync.Mutex
}

// Release rilascia il lock
func (l *localLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Il lock verrà rimosso dallo StateManager
	return nil
}

// Refresh rinnova il lock
func (l *localLock) Refresh(ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acquiredAt = time.Now()
	l.ttl = ttl
	return nil
}

// IsValid verifica se il lock è ancora valido
func (l *localLock) IsValid() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Since(l.acquiredAt) < l.ttl
}

// NewStateManager crea un nuovo StateManager
func NewStateManager(dataDir string, workerStore *WorkerStore, jobStore *JobStore) (*StateManager, error) {
	stateFile := filepath.Join(dataDir, "global_state.json")

	sm := &StateManager{
		workerStore: workerStore,
		jobStore:    jobStore,
		globalState: &storage.GlobalState{
			Workers:     make(map[string]*models.Worker),
			LastUpdated: time.Now(),
			Version:     1,
		},
		locks:     make(map[string]*localLock),
		stateFile: stateFile,
		observers: make([]chan<- storage.StateChange, 0),
	}

	// Carica stato globale esistente
	if err := sm.loadGlobalState(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load global state: %w", err)
	}

	return sm, nil
}

// GetActiveWorkers restituisce i worker attivi
func (s *StateManager) GetActiveWorkers(ctx context.Context) map[string]*models.Worker {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Usa il timeout di default di 30 secondi
	timeout := 30 * time.Second
	workers, _ := s.workerStore.GetActiveWorkers(ctx, timeout)

	return workers
}

// UpdateWorkerStatus aggiorna lo stato di un worker
func (s *StateManager) UpdateWorkerStatus(ctx context.Context, id string, status models.WorkerStatus) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Aggiorna nel worker store
	if err := s.workerStore.UpdateWorkerStatus(ctx, id, status); err != nil {
		return err
	}

	// Aggiorna stato globale
	s.globalState.LastUpdated = time.Now()
	s.globalState.Version++

	// Notifica observers
	s.notifyChange(storage.StateChange{
		Type:      storage.ChangeWorkerStatus,
		EntityID:  id,
		NewValue:  status,
		Timestamp: time.Now(),
	})

	return s.saveGlobalState()
}

// GetWorkerStatus restituisce lo stato corrente di un worker
func (s *StateManager) GetWorkerStatus(ctx context.Context, id string) (models.WorkerStatus, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	worker, err := s.workerStore.GetWorker(ctx, id)
	if err != nil {
		return "", err
	}
	return worker.Status, nil
}

// AcquireLock acquisisce un lock distribuito
func (s *StateManager) AcquireLock(ctx context.Context, name string, ttl time.Duration) (storage.Lock, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Verifica se il lock esiste ed è valido
	if existingLock, exists := s.locks[name]; exists {
		if existingLock.IsValid() {
			return nil, fmt.Errorf("lock %s already acquired", name)
		}
		// Lock scaduto, rimuovilo
		delete(s.locks, name)
	}

	// Crea nuovo lock
	lock := &localLock{
		name:       name,
		acquiredAt: time.Now(),
		ttl:        ttl,
	}
	s.locks[name] = lock

	return lock, nil
}

// ReleaseLock rilascia un lock (metodo interno)
func (s *StateManager) ReleaseLock(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.locks[name]; !exists {
		return fmt.Errorf("lock %s not found", name)
	}

	delete(s.locks, name)
	return nil
}

// GetGlobalState restituisce lo stato globale completo
func (s *StateManager) GetGlobalState(ctx context.Context) (*storage.GlobalState, error) {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Calcola statistiche attuali
	stats, err := NewQueueStore(s.jobStore).GetQueueStats(ctx)
	if err != nil {
		return nil, err
	}

	// Ottieni worker attivi
	workers, err := s.workerStore.LoadWorkers(ctx)
	if err != nil {
		return nil, err
	}

	state := &storage.GlobalState{
		Workers:      workers,
		ActiveJobs:   stats.Processing,
		PendingJobs:  stats.Pending,
		LastUpdated:  s.globalState.LastUpdated,
		Version:      s.globalState.Version,
		MasterNodeID: s.globalState.MasterNodeID,
	}

	return state, nil
}

// UpdateGlobalState aggiorna lo stato globale
func (s *StateManager) UpdateGlobalState(ctx context.Context, state *storage.GlobalState) error {
	// Controlla cancellazione contesto
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.globalState = state
	s.globalState.LastUpdated = time.Now()
	s.globalState.Version++

	return s.saveGlobalState()
}

// Watch chiude un canale per ricevere aggiornamenti di stato
func (s *StateManager) Watch(ctx context.Context) (<-chan storage.StateChange, error) {
	ch := make(chan storage.StateChange, 100)

	s.observersMu.Lock()
	s.observers = append(s.observers, ch)
	s.observersMu.Unlock()

	// Gestisci la chiusura del contesto con timeout di sicurezza
	go func() {
		// Usa un timer di sicurezza per evitare goroutine leak
		// Se il contesto non viene chiuso, il goroutine termina dopo 1 ora
		timer := time.NewTimer(1 * time.Hour)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			// Contesto chiuso normalmente
		case <-timer.C:
			// Timeout di sicurezza - chiude il goroutine per prevenire leak
		}

		s.observersMu.Lock()
		defer s.observersMu.Unlock()

		for i, observer := range s.observers {
			if observer == ch {
				s.observers = append(s.observers[:i], s.observers[i+1:]...)
				break
			}
		}
		close(ch)
	}()

	return ch, nil
}

// notifyChange notifica tutti gli observer di un cambiamento
func (s *StateManager) notifyChange(change storage.StateChange) {
	s.observersMu.RLock()
	defer s.observersMu.RUnlock()

	for _, ch := range s.observers {
		select {
		case ch <- change:
		default:
			// Canale pieno, salta
		}
	}
}

// loadGlobalState carica lo stato globale dal disco
func (s *StateManager) loadGlobalState() error {
	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		return err
	}

	var state storage.GlobalState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal global state: %w", err)
	}

	s.globalState = &state
	return nil
}

// saveGlobalState salva lo stato globale su disco
func (s *StateManager) saveGlobalState() error {
	data, err := json.MarshalIndent(s.globalState, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal global state: %w", err)
	}

	tempFile := s.stateFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempFile, s.stateFile); err != nil {
		// Try to clean up the temp file, log error if it fails
		if removeErr := os.Remove(tempFile); removeErr != nil {
			// Log but don't fail - the main error is more important
			fmt.Printf("Warning: failed to remove temp file %s: %v\n", tempFile, removeErr)
		}
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// CleanupExpiredLocks rimuove i lock scaduti
func (s *StateManager) CleanupExpiredLocks() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for name, lock := range s.locks {
		if !lock.IsValid() {
			delete(s.locks, name)
		}
	}
}

// GetActiveLocks restituisce i lock attivi
func (s *StateManager) GetActiveLocks() map[string]time.Time {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	result := make(map[string]time.Time)
	for name, lock := range s.locks {
		if lock.IsValid() {
			result[name] = lock.acquiredAt
		}
	}

	return result
}

// SetMasterNodeID imposta l'ID del nodo master
func (s *StateManager) SetMasterNodeID(nodeID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.globalState.MasterNodeID = nodeID
	s.globalState.LastUpdated = time.Now()
	s.globalState.Version++

	return s.saveGlobalState()
}