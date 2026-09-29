package scheduling

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ── Spool delivery policy ────────────────────────────────────────────
//
// The spool is ONE ordered directory, so its per-record policy decides
// between "this report is a little late" and "the calendar is dark". The
// three outcomes are deliberately different:
//
//   - A job whose Calendar card does not exist YET keeps its records and the
//     pass moves on to the next record. Waiting for a card is the whole point
//     of keying reports by job instead of by event key (the card may be
//     created after the job), but one unlinked job must never withhold every
//     other job's reports.
//   - A record the calendar PERMANENTLY rejects (4xx that is not a 404) is
//     dead-lettered as "<name>.dead" for the operator instead of being
//     retried forever at the head of the queue.
//   - A TRANSIENT failure (network, 5xx, 429, cancellation) stops the pass,
//     so per-job ordering is preserved for the next tick.
//
// The directory is bounded as well: past maxSpoolEntries the OLDEST records
// are dead-lettered. Heartbeats spool one record per running job per lease
// tick, so an unbounded spool is a disk leak whenever a linked card is
// missing — the failure mode a 5000-job day would hit first.
const (
	// defaultMaxSpoolEntries bounds the spool directory.
	defaultMaxSpoolEntries = 8192
	// defaultMaxResolvedCache bounds the positive job→event_key cache, which
	// spares one by-job lookup per spooled record of the same job.
	defaultMaxResolvedCache = 4096
	// defaultResolvedTTL expires a cached resolution so a card re-linked to a
	// different event key is observed.
	defaultResolvedTTL = 10 * time.Minute
	// defaultCardPendingTTL avoids repeating the same not-found lookup for every
	// heartbeat while still noticing a card created shortly after its job.
	defaultCardPendingTTL = 15 * time.Second
)

// Reporter durably spools progress updates before returning to the renderer.
// Run drains the directory in order; transient delivery failures leave the
// item on disk for a later tick or process restart.
type Reporter struct {
	client   *Client
	dir      string
	interval time.Duration

	// Drain state. A single mutex serializes drain passes (and guards the
	// resolution cache) so two Run loops over one spool interleave at record
	// granularity instead of racing on the cache map.
	drainMu         sync.Mutex
	maxSpoolEntries int
	maxResolved     int
	resolvedTTL     time.Duration
	resolved        map[string]cachedResolution
	resolvedOrder   []string
	cardPending     map[string]time.Time
}

// cachedResolution is one remembered job→event_key answer.
type cachedResolution struct {
	eventKey string
	at       time.Time
}

func NewReporter(client *Client, directory string, interval time.Duration) (*Reporter, error) {
	if client == nil {
		return nil, fmt.Errorf("calendar client is required")
	}
	if directory == "" {
		return nil, fmt.Errorf("calendar outbox directory is required")
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create calendar outbox: %w", err)
	}
	return &Reporter{
		client:          client,
		dir:             directory,
		interval:        interval,
		maxSpoolEntries: defaultMaxSpoolEntries,
		maxResolved:     defaultMaxResolvedCache,
		resolvedTTL:     defaultResolvedTTL,
		resolved:        map[string]cachedResolution{},
		cardPending:     map[string]time.Time{},
	}, nil
}

type progressEnvelope struct {
	EventKey string   `json:"event_key,omitempty"`
	JobID    string   `json:"job_id,omitempty"`
	Progress Progress `json:"progress"`
}

// EnqueueProgress durably stores an update and never waits for an HTTP request.
func (r *Reporter) EnqueueProgress(eventKey string, update Progress) error {
	if eventKey == "" {
		return fmt.Errorf("event key is required")
	}
	data, err := json.Marshal(progressEnvelope{EventKey: eventKey, Progress: update})
	if err != nil {
		return fmt.Errorf("encode calendar progress: %w", err)
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(nonce))
	tmp, err := os.CreateTemp(r.dir, ".pending-*.tmp")
	if err != nil {
		return fmt.Errorf("create calendar outbox record: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("persist calendar progress: %w", err)
	}
	if err = os.Rename(tmpName, filepath.Join(r.dir, name)); err != nil {
		return fmt.Errorf("publish calendar progress: %w", err)
	}
	if d, e := os.Open(r.dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	ok = true
	return nil
}

// EnqueueJobProgress stores a report by Job Master ID; the drain resolves the
// associated Calendar card later, allowing the card to be created after enqueue.
func (r *Reporter) EnqueueJobProgress(jobID string, update Progress) error {
	if jobID == "" {
		return fmt.Errorf("job id is required")
	}
	data, err := json.Marshal(progressEnvelope{JobID: jobID, Progress: update})
	if err != nil {
		return err
	}
	nonce := make([]byte, 8)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(nonce))
	tmp, err := os.CreateTemp(r.dir, ".pending-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, filepath.Join(r.dir, name)); err != nil {
		return err
	}
	if d, e := os.Open(r.dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	ok = true
	return nil
}

// Run drains queued records until ctx is cancelled. A failed report is retained
// and retried on subsequent intervals without holding up video work.
func (r *Reporter) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// drainOutcome is what one spooled record did to the current pass.
type drainOutcome int

const (
	// drainConsumed: the record left the spool (delivered or dead-lettered).
	drainConsumed drainOutcome = iota
	// drainKeep: the record stays for a later tick; the pass continues with
	// the next record, which belongs to a DIFFERENT job.
	drainKeep
	// drainStopPass: the calendar is unreachable or rejecting transiently;
	// stop now so ordering is preserved and a dead endpoint is not hammered.
	drainStopPass
)

func (r *Reporter) drain(ctx context.Context) {
	r.drainMu.Lock()
	defer r.drainMu.Unlock()
	now := time.Now()
	names := r.trimSpool(r.spoolNames())
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		if r.deliver(ctx, now, name) == drainStopPass {
			return
		}
	}
}

// spoolNames lists the deliverable records in spool order (the timestamp
// prefix makes lexicographic order chronological order).
func (r *Reporter) spoolNames() []string {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// trimSpool enforces the spool bound by dead-lettering the OLDEST overflow, so
// the newest reports (which describe work the operator is watching now) are the
// ones that survive.
func (r *Reporter) trimSpool(names []string) []string {
	overflow := len(names) - r.maxSpoolEntries
	if overflow <= 0 {
		return names
	}
	for _, name := range names[:overflow] {
		r.deadLetter(filepath.Join(r.dir, name))
	}
	return names[overflow:]
}

// deliver attempts exactly one record and reports what the pass should do next.
func (r *Reporter) deliver(ctx context.Context, now time.Time, name string) drainOutcome {
	path := filepath.Join(r.dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return drainKeep
	}
	var item progressEnvelope
	if json.Unmarshal(data, &item) != nil || (item.EventKey == "" && item.JobID == "") {
		r.deadLetter(path)
		return drainConsumed
	}
	eventKey := item.EventKey
	if eventKey == "" {
		eventKey, err = r.resolveEventKey(ctx, now, item.JobID)
		if errors.Is(err, errCardPending) {
			return drainKeep
		}
		if err != nil {
			return drainStopPass
		}
	}
	if _, err := r.client.UpdateProgress(ctx, eventKey, item.Progress); err != nil {
		if isPermanentRejection(err) {
			r.deadLetter(path)
			return drainConsumed
		}
		return drainStopPass
	}
	_ = os.Remove(path)
	return drainConsumed
}

// errCardPending marks "the card does not exist yet": a retryable state that
// must not stop the pass.
var errCardPending = errors.New("linked Calendar card not created yet")

func (r *Reporter) resolveEventKey(ctx context.Context, now time.Time, jobID string) (string, error) {
	if eventKey, ok := r.cachedEventKey(now, jobID); ok {
		return eventKey, nil
	}
	if missingAt, ok := r.cardPending[jobID]; ok && now.Sub(missingAt) <= defaultCardPendingTTL {
		return "", errCardPending
	}
	raw, err := r.client.GetEventByJobID(ctx, jobID)
	if errors.Is(err, ErrEventNotFound) {
		r.cardPending[jobID] = now
		return "", errCardPending
	}
	if err != nil {
		return "", err
	}
	var linked struct {
		EventKey string `json:"event_key"`
	}
	if json.Unmarshal(raw, &linked) != nil || linked.EventKey == "" {
		r.cardPending[jobID] = now
		return "", errCardPending
	}
	delete(r.cardPending, jobID)
	r.rememberEventKey(now, jobID, linked.EventKey)
	return linked.EventKey, nil
}

func (r *Reporter) cachedEventKey(now time.Time, jobID string) (string, bool) {
	res, ok := r.resolved[jobID]
	if !ok || now.Sub(res.at) > r.resolvedTTL {
		return "", false
	}
	return res.eventKey, true
}

func (r *Reporter) rememberEventKey(now time.Time, jobID, eventKey string) {
	if _, seen := r.resolved[jobID]; !seen {
		r.resolvedOrder = append(r.resolvedOrder, jobID)
	}
	r.resolved[jobID] = cachedResolution{eventKey: eventKey, at: now}
	for len(r.resolvedOrder) > r.maxResolved {
		oldest := r.resolvedOrder[0]
		r.resolvedOrder = r.resolvedOrder[1:]
		delete(r.resolved, oldest)
	}
}

func (r *Reporter) deadLetter(path string) {
	_ = os.Rename(path, path+".dead")
}

// isPermanentRejection reports whether the calendar answered with a decision no
// retry can change. The client already retried the transient shapes (network,
// 5xx, 429) three times before surfacing them, so a bare 4xx here is final.
//
// A 404 on the progress route lands here on purpose: the card was deleted
// between the lookup and the patch, so the record is undeliverable and belongs
// in the operator-visible dead-letter set rather than at the head of the queue.
func isPermanentRejection(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode >= 400 &&
		httpErr.StatusCode < 500 &&
		httpErr.StatusCode != http.StatusTooManyRequests &&
		httpErr.StatusCode != http.StatusRequestTimeout
}
