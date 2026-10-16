// Command queued runs the central pull-based job queue.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/migrate"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/postgres"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/server"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
	"github.com/jackc/pgx/v5"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address")
	lease := flag.Duration("lease", 10*time.Minute, "job lease duration")
	maxAttempts := flag.Int("max-attempts", model.DefaultMaxAttempts, "max attempts before a job is permanently failed")
	expireInterval := flag.Duration("expire-interval", 5*time.Second, "lease expiry scan interval")
	// Requeue retry policy. The default (0) is the zero RetryConfig: one attempt
	// per sweep, exactly the behavior before the flags existed. Set
	// -requeue-retry-attempts > 1 to retry a transient lease-expiry failure with
	// exponential backoff (see service.RetryConfig). Before this wiring the
	// policy was configurable only from tests, so no deployment could turn
	// retries on.
	requeueRetryAttempts := flag.Int("requeue-retry-attempts", 0, "attempts per lease-expiry requeue when it fails transiently; values <= 1 disable retries")
	requeueRetryBaseDelay := flag.Duration("requeue-retry-base-delay", 0, "base delay before the first requeue retry (defaults to 100ms when retries are enabled)")
	requeueRetryJitter := flag.Float64("requeue-retry-jitter", 0, "fraction of the requeue retry delay to randomize, clamped to [0,1]")
	workerStale := flag.Duration("worker-stale-after", 90*time.Second, "worker heartbeat staleness threshold")
	// Graceful shutdown. A rolling deploy sends SIGTERM to every replica; with
	// the previous ListenAndServe + log.Fatal shape the process died mid-flight,
	// cutting long polls and submit requests with no chance to answer. This is
	// how long in-flight requests may finish before the listener is closed hard.
	shutdownTimeout := flag.Duration("shutdown-timeout", 25*time.Second, "how long to drain in-flight requests after SIGTERM/SIGINT before closing connections")
	// Connection pool bounds. The PostgreSQL claim transactions are short
	// (SKIP LOCKED single row) so a modest pool is enough; the LISTEN session is
	// separate. They are flags, not constants, because capacity is a property of
	// the DEPLOYMENT: N replicas share one database, so the per-replica bound is
	// what keeps the fleet from opening connections faster than the server can
	// serve them.
	dbMaxOpenConns := flag.Int("db-max-open-conns", 25, "maximum open PostgreSQL connections per queue replica")
	dbMaxIdleConns := flag.Int("db-max-idle-conns", 10, "maximum idle PostgreSQL connections per queue replica (must not exceed -db-max-open-conns)")
	dbConnMaxLifetime := flag.Duration("db-conn-max-lifetime", time.Hour, "maximum lifetime of a pooled PostgreSQL connection")
	dbConnMaxIdleTime := flag.Duration("db-conn-max-idle-time", 5*time.Minute, "maximum idle time of a pooled PostgreSQL connection")
	dbConnectTimeout := flag.Duration("db-connect-timeout", 30*time.Second, "how long to wait for the initial PostgreSQL connection to answer")
	dbURL := flag.String("db-url", "", "PostgreSQL DSN; enables the postgres repository when set (defaults to $DATABASE_URL)")
	// Backend selects the job repository explicitly: postgres persists to
	// PostgreSQL, memory is volatile process state for dev/test, auto keeps
	// the historical rule (postgres when a DSN is configured, else memory).
	// Production must pass -backend=postgres (the systemd unit does): an
	// unset DSN then fails startup instead of silently running production
	// traffic on a volatile queue that loses every job on restart.
	backend := flag.String("backend", "auto", "job repository backend: auto | postgres | memory")
	flag.Parse()

	if err := validateCapacity(*dbMaxOpenConns, *dbMaxIdleConns, *dbConnMaxLifetime, *dbConnMaxIdleTime, *dbConnectTimeout, *shutdownTimeout); err != nil {
		log.Fatalf("invalid capacity configuration: %v", err)
	}

	// Root context: one signal cancels everything that outlives a request — the
	// lease-expiry sweeper, the LISTEN session and the HTTP listener — so
	// shutdown is a single ordered sequence instead of N independent goroutines
	// racing the process exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	selected := strings.ToLower(strings.TrimSpace(*backend))
	switch selected {
	case "auto", "postgres", "memory":
	default:
		log.Fatalf("unknown -backend %q: want auto | postgres | memory", *backend)
	}
	if selected == "postgres" && databaseURL(*dbURL) == "" {
		log.Fatalf("-backend=postgres but no DSN: set -db-url or $DATABASE_URL")
	}

	// In-memory is the default backend; PostgreSQL is used when configured.
	// Both backends implement the job and worker contracts.
	memRepo := memory.New(*lease, *maxAttempts)
	repo := repository.JobRepository(memRepo)
	var workerRepo repository.WorkerRepository = memRepo
	var postgresDSN string
	usePostgres := selected == "postgres" || (selected == "auto" && databaseURL(*dbURL) != "")
	if usePostgres {
		dsn := databaseURL(*dbURL)
		db, err := openDatabase(dsn, *dbConnectTimeout)
		if err != nil {
			log.Fatalf("connect database: %v", err)
		}
		// Bound the pool: unbounded, one connection per concurrent long-poll
		// claim would let connection count scale with worker count instead of
		// load. Postgres claim transactions are short (SKIP LOCKED single-row)
		// so a modest pool is enough; the LISTEN session is separate.
		db.SetMaxOpenConns(*dbMaxOpenConns)
		db.SetMaxIdleConns(*dbMaxIdleConns)
		db.SetConnMaxIdleTime(*dbConnMaxIdleTime)
		db.SetConnMaxLifetime(*dbConnMaxLifetime)
		defer db.Close()

		// Migrations run under the root context: a SIGTERM during a long
		// migration now aborts it instead of being ignored until it completes.
		if err := migrate.Apply(ctx, db); err != nil {
			log.Fatalf("migrate database: %v", err)
		}
		pgRepo := postgres.New(db, *lease, *maxAttempts)
		repo = pgRepo
		workerRepo = pgRepo
		postgresDSN = dsn
		log.Printf("using postgres job repository")
	} else if selected == "memory" {
		log.Printf("WARNING: using volatile in-memory job repository by explicit -backend=memory: all jobs are lost on restart")
	} else {
		log.Printf("WARNING: no DSN configured, using volatile in-memory job repository: pass -backend=postgres with a DSN for production")
	}

	svc := service.New(repo)
	svc.SetWorkerRepository(workerRepo, *workerStale)
	svc.SetRequeueRetry(service.RetryConfig{
		MaxAttempts: *requeueRetryAttempts,
		BaseDelay:   *requeueRetryBaseDelay,
		Jitter:      *requeueRetryJitter,
	})
	m := metrics.New()
	svc.SetMetrics(m)

	// Background lease expiry: requeue jobs whose lease elapsed. Bound to the
	// root context so a shutdown stops sweeping instead of racing the process
	// teardown with one more database write.
	go func() {
		ticker := time.NewTicker(*expireInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Printf("lease expiry sweeper stopped")
				return
			case <-ticker.C:
			}
			n, err := svc.RequeueExpired(ctx, time.Now())
			if err != nil {
				log.Printf("requeue expired: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("requeued %d jobs with expired lease", n)
			}
			// Refresh worker liveness so ready/offline gauges decay as
			// heartbeats age without requiring a new heartbeat.
			svc.RefreshWorkerHealth(ctx)
		}
	}()

	srv := server.New(svc)
	srv.SetMetricsHandler(m.Handler())
	if postgresDSN != "" {
		// Every queue replica owns a dedicated LISTEN connection. Migrations 016
		// and 024 emit rendering_jobs notifications when a row becomes claimable
		// (pending/rendered) and when it reaches a terminal state
		// (completed/failed/cancelled); in both cases the notification only wakes
		// local long-poll requests (claim waiters and producer GET /jobs/{id}/wait
		// waiters). The repository remains the source of truth: every woken
		// waiter re-reads the row, and claims still use SKIP LOCKED.
		go listenForJobNotifications(ctx, postgresDSN, srv)
	}

	// Bind explicitly so a taken port is a startup failure with a readable
	// message rather than a Serve error inside the shutdown path.
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	log.Printf("job queue listening on %s (lease=%s, max-attempts=%d, requeue-retry-attempts=%d)", *addr, *lease, *maxAttempts, *requeueRetryAttempts)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := serve(ctx, httpServer, listener, *shutdownTimeout, log.Printf); err != nil {
		log.Fatalf("queue server: %v", err)
	}
	log.Printf("queue stopped")
}

// serve owns the HTTP lifecycle: it serves until the root context is cancelled
// (SIGTERM/SIGINT), then drains in-flight requests within shutdownTimeout.
//
// Upgrading into a drain is the whole point. The historical shape called
// ListenAndServe and log.Fatal, so SIGTERM during a rolling deploy killed the
// process mid-request: a producer's submit could be committed and its response
// lost, and every long-poll claim waiter was cut without an HTTP reply. Here the
// listener stops accepting, requests already being served get the full timeout
// to answer, and only then are connections closed.
//
// A drain that misses its deadline is REPORTED, not swallowed: the caller exits
// non-zero instead of pretending the shutdown was clean. The server is closed
// hard in that case so the process cannot hang on connections the deadline
// already declared unresponsive.
func serve(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration, logf func(string, ...any)) error {
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			// The expected result of our own Shutdown/Close.
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		// The server stopped on its own: a listener error, or the port going
		// away underneath it. Nothing to drain.
		return err
	case <-ctx.Done():
	}

	logf("queue shutdown: draining in-flight requests (timeout %s)", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		logf("queue shutdown: drain deadline exceeded, closing connections: %v", shutdownErr)
		_ = server.Close()
	}
	if err := <-serveErr; err != nil {
		return err
	}
	if shutdownErr != nil {
		return fmt.Errorf("drain in-flight requests: %w", shutdownErr)
	}
	return nil
}

// validateCapacity rejects a capacity configuration that cannot describe a
// working deployment, at startup, with the flag name in the message. A negative
// pool bound or an idle bound above the open bound silently becomes a
// different pool than the operator asked for, which is exactly the kind of
// "worked in staging, starves in production" misconfiguration a boot-time check
// removes.
func validateCapacity(maxOpen, maxIdle int, connMaxLifetime, connMaxIdleTime, connectTimeout, shutdownTimeout time.Duration) error {
	switch {
	case maxOpen < 1:
		return fmt.Errorf("-db-max-open-conns must be >= 1, got %d", maxOpen)
	case maxIdle < 0:
		return fmt.Errorf("-db-max-idle-conns must be >= 0, got %d", maxIdle)
	case maxIdle > maxOpen:
		return fmt.Errorf("-db-max-idle-conns (%d) must not exceed -db-max-open-conns (%d)", maxIdle, maxOpen)
	case connMaxLifetime <= 0:
		return fmt.Errorf("-db-conn-max-lifetime must be > 0, got %s", connMaxLifetime)
	case connMaxIdleTime <= 0:
		return fmt.Errorf("-db-conn-max-idle-time must be > 0, got %s", connMaxIdleTime)
	case connectTimeout <= 0:
		return fmt.Errorf("-db-connect-timeout must be > 0, got %s", connectTimeout)
	case shutdownTimeout <= 0:
		return fmt.Errorf("-shutdown-timeout must be > 0, got %s", shutdownTimeout)
	}
	return nil
}

// listenForJobNotifications keeps one lightweight PostgreSQL LISTEN session
// per queue replica and reconnects after transient database/network failures.
func listenForJobNotifications(ctx context.Context, dsn string, srv *server.Server) {
	const retryDelay = 2 * time.Second
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			log.Printf("queue notify connect: %v", err)
			if !sleepContext(ctx, retryDelay) {
				return
			}
			continue
		}

		if _, err := conn.Exec(ctx, "LISTEN rendering_jobs"); err != nil {
			log.Printf("queue notify LISTEN: %v", err)
			_ = conn.Close(context.Background())
			if !sleepContext(ctx, retryDelay) {
				return
			}
			continue
		}
		log.Printf("queue notifications listening on rendering_jobs")

		for ctx.Err() == nil {
			notification, err := conn.WaitForNotification(ctx)
			if err != nil {
				log.Printf("queue notify wait: %v", err)
				break
			}
			srv.NotifyState(model.State(notification.Payload))
		}
		_ = conn.Close(context.Background())
		if !sleepContext(ctx, retryDelay) {
			return
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// databaseURL returns the explicit flag value, falling back to $DATABASE_URL.
func databaseURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("DATABASE_URL")
}

// openDatabase opens and verifies a PostgreSQL connection.
func openDatabase(dsn string, connectTimeout time.Duration) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
