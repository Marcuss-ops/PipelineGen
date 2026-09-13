package wiring

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

// recordingSQLiteOutboxHandler captures the canonical envelope the adapter
// hands to a capability handler, so the mapping can be asserted without a live
// Drive stack.
type recordingSQLiteOutboxHandler struct {
	got []outboxevents.Event
	err error
}

func (r *recordingSQLiteOutboxHandler) Handle(_ context.Context, evt outboxevents.Event) error {
	r.got = append(r.got, evt)
	return r.err
}

// stubAssetEmbedder satisfies pgmedia.AssetEmbedder so a PostgresIndexWorker can
// be constructed in tests without the E5 sidecar. Registration never embeds.
type stubAssetEmbedder struct{}

func (stubAssetEmbedder) EmbedAssetText(context.Context, string) ([]float32, error) {
	return []float32{0}, nil
}

// TestPgOutboxHandlerAdapter_MapsClaimEnvelope pins the lossless mapping of the
// PostgreSQL claim onto the canonical SQLite outboxevents.Event shape for every
// field the capability handlers read.
func TestPgOutboxHandlerAdapter_MapsClaimEnvelope(t *testing.T) {
	rec := &recordingSQLiteOutboxHandler{}
	h := pgOutboxHandlerAdapter{handler: rec}

	claim := &pgmedia.OutboxClaim{
		WorkerID: "worker-1",
		LeaseID:  "lease-1",
		Event: pgmedia.OutboxEvent{
			ID:            42,
			EventType:     "asset.drive.delete_requested",
			AggregateID:   "asset-1",
			AggregateType: "media_asset",
			PayloadJSON:   `{"schema_version":"asset.drive.delete_requested.v1"}`,
			Status:        "processing",
			AttemptCount:  2,
			MaxAttempts:   5,
			LastError:     "boom",
			EventKey:      "drive_delete:asset-1",
			Priority:      5,
			CreatedAt:     "2026-09-13T00:00:00Z",
			UpdatedAt:     "2026-09-13T00:01:00Z",
		},
	}

	if err := h.Handle(context.Background(), claim); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("handler must receive exactly one envelope; got %d", len(rec.got))
	}
	got := rec.got[0]
	if got.ID != 42 ||
		got.EventType != "asset.drive.delete_requested" ||
		got.AggregateID != "asset-1" ||
		got.AggregateType != "media_asset" ||
		got.PayloadJSON != claim.Event.PayloadJSON ||
		got.Status != "processing" ||
		got.AttemptCount != 2 ||
		got.MaxAttempts != 5 ||
		got.LastError != "boom" ||
		got.EventKey != "drive_delete:asset-1" ||
		got.Priority != 5 ||
		got.CreatedAt != claim.Event.CreatedAt ||
		got.UpdatedAt != claim.Event.UpdatedAt {
		t.Fatalf("envelope mapping lost data: %+v", got)
	}
}

// TestPgOutboxHandlerAdapter_FailsClosedOnNilInput pins the godlike/07 contract:
// a nil claim or an unwired handler is an error, never a silent ack.
func TestPgOutboxHandlerAdapter_FailsClosedOnNilInput(t *testing.T) {
	if err := (pgOutboxHandlerAdapter{handler: &recordingSQLiteOutboxHandler{}}).Handle(context.Background(), nil); err == nil {
		t.Fatal("nil claim must fail closed")
	}
	if err := (pgOutboxHandlerAdapter{}).Handle(context.Background(), &pgmedia.OutboxClaim{}); err == nil {
		t.Fatal("unwired handler must fail closed")
	}
}

// TestRegisterPostgresDeleteSagaHandlers_NilWorkerIsNoop pins the non-PG path:
// without a PostgreSQL media worker there is nothing to register, and the
// composition root must not abort.
func TestRegisterPostgresDeleteSagaHandlers_NilWorkerIsNoop(t *testing.T) {
	if err := registerPostgresDeleteSagaHandlers(nil, nil, jobsoutbox.DriveDeleteDeps{}, zap.NewNop()); err != nil {
		t.Fatalf("nil worker must be a no-op; got %v", err)
	}
}

// TestDeleteSagaEventsRegisteredOnPostgresWorker is the AST pin for audit
// finding #3: BOTH hops of the deletion saga must be registered with
// RegisterHandler on the PostgreSQL media worker inside
// registerPostgresDeleteSagaHandlers. Without the index hop, every asset the
// Drive hop advances dead-letters at INDEX_DELETE_PENDING.
func TestDeleteSagaEventsRegisteredOnPostgresWorker(t *testing.T) {
	const src = "build_outbox_handlers.go"
	if _, err := os.Stat(src); err != nil {
		t.Skipf("wiring source not available: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	want := map[string]bool{
		"jobsoutbox.DriveDeleteEventType":              false,
		"outboxevents.EventAssetIndexDeleteRequested":  false,
		"outboxevents.EventAssetIndexRestoreRequested": false,
	}
	found := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RegisterHandler" {
				return true
			}
			for _, arg := range call.Args {
				name := qualifiedExprName(arg)
				if _, wanted := want[name]; !wanted {
					continue
				}
				found[name] = append(found[name], fn.Name.Name)
			}
			return true
		})
	}

	for name := range want {
		got := found[name]
		if len(got) != 1 {
			t.Fatalf("%s: want exactly 1 PostgreSQL-worker registration, got %d (%v). The saga event is emitted by the canonical PG committer, so an unregistered hop dead-letters.", name, len(got), got)
		}
		if got[0] != "registerPostgresDeleteSagaHandlers" {
			t.Fatalf("%s: registered by %q, want registerPostgresDeleteSagaHandlers", name, got[0])
		}
	}
}

// TestRegisterPostgresDeleteSagaHandlers_RegistersBothHops is the live-PG
// behavioural pin: after registration, a second RegisterHandler for either
// event type MUST fail as a duplicate — which proves both hops actually own a
// consumer on the PostgreSQL worker.
func TestRegisterPostgresDeleteSagaHandlers_RegistersBothHops(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping live PostgreSQL delete-saga registration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres (is the test container up?): %v", err)
	}

	box := pgmedia.NewOutboxRepository(db)
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	committer := pgmedia.NewPostgresMediaCommitter(db, box, ledger, zap.NewNop())
	worker := pgmedia.NewPostgresIndexWorker(box, pgmedia.NewVectorSurfaceWriter(db), stubAssetEmbedder{}, "test-model")

	// The PostgreSQL committer itself satisfies all three saga state ports.
	deps := jobsoutbox.DriveDeleteDeps{
		DriveDeleteHandler:   dummyDriveDeleter{},
		DrivePatchLifecycle:  committer,
		DrivePatchLifecycleW: committer,
		DrivePatchStateAdv:   committer,
	}
	if err := registerPostgresDeleteSagaHandlers(worker, committer, deps, zap.NewNop()); err != nil {
		t.Fatalf("registerPostgresDeleteSagaHandlers: %v", err)
	}

	for _, eventType := range []string{
		jobsoutbox.DriveDeleteEventType,
		outboxevents.EventAssetIndexDeleteRequested,
		outboxevents.EventAssetIndexRestoreRequested,
	} {
		if err := worker.RegisterHandler(eventType, pgOutboxHandlerAdapter{}); err == nil {
			t.Errorf("%s must already be registered on the PostgreSQL media worker", eventType)
		}
	}
}
