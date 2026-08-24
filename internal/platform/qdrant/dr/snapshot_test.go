package dr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── Stub ports ──────────────────────────────────────────────────────

// stubSnapshotStore is an in-memory SnapshotStore used by all dr/ unit
// tests. Tracks createCalls / listCalls / deleteCalls / getURLCalls so
// tests can assert each delegation. RestoreSnapshot is a pass-through
// because the restore pipeline's success/failure is verified through the
// switcher + creator stubs downstream, not through RestoreSnapshot itself.
//
// Cycle break (June 2026): the stub's createResp / listResp are now
// dr.SnapshotDescription (dr-owned), not qdrant.SnapshotDescription.
// This file no longer imports the qdrant infrastructure package.
type stubSnapshotStore struct {
	createCalls []string
	listCalls   []string
	deleteCalls [][2]string
	getURLCalls [][2]string
	createResp  *SnapshotDescription
	createErr   error
	listResp    []SnapshotDescription
	listErr     error
	deleteErr   error
	getURLResp  string
	getURLErr   error
	restoreErr  error
}

func (s *stubSnapshotStore) CreateSnapshot(_ context.Context, collection string) (*SnapshotDescription, error) {
	s.createCalls = append(s.createCalls, collection)
	if s.createErr != nil {
		return nil, s.createErr
	}
	return s.createResp, nil
}

func (s *stubSnapshotStore) ListSnapshots(_ context.Context, collection string) ([]SnapshotDescription, error) {
	s.listCalls = append(s.listCalls, collection)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listResp, nil
}

func (s *stubSnapshotStore) DeleteSnapshot(_ context.Context, collection, snapshotName string) error {
	s.deleteCalls = append(s.deleteCalls, [2]string{collection, snapshotName})
	return s.deleteErr
}

func (s *stubSnapshotStore) GetSnapshotURL(_ context.Context, collection, snapshotName string) (string, error) {
	s.getURLCalls = append(s.getURLCalls, [2]string{collection, snapshotName})
	return s.getURLResp, s.getURLErr
}

func (s *stubSnapshotStore) RestoreSnapshot(_ context.Context, _ string, _ string) error {
	// Pass-through: RestoreSnapshot's success/failure is verified downstream
	// through the creator/switcher/verifier stubs. Recording the call here
	// would be dead-code in tests (no assertion reads restoreCalls).
	return s.restoreErr
}

// stubSwitcher captures alias-flip calls; fails on demand.
type stubSwitcher struct {
	switchCalls [][4]string
	switchErr   error
}

func (s *stubSwitcher) SwitchAlias(_ context.Context, alias, oldTarget, newTarget string) error {
	s.switchCalls = append(s.switchCalls, [4]string{alias, oldTarget, newTarget, ""})
	return s.switchErr
}

// stubCreator captures collection-create calls; fails on demand.
type stubCreator struct {
	createCalls []string
	createErr   error
}

func (s *stubCreator) CreateCollection(_ context.Context, name string) error {
	s.createCalls = append(s.createCalls, name)
	return s.createErr
}

// stubVerifier returns a canned VerifyReport; lets tests drive Ready
// outcomes + error counts without depending on the qdrant verifier.
type stubVerifier struct {
	verifyCalls [][2]int
	report      *VerifyReport
	err         error
}

func (s *stubVerifier) VerifyReindex(_ context.Context, _ string, expectedPoints int) (*VerifyReport, error) {
	s.verifyCalls = append(s.verifyCalls, [2]int{expectedPoints, expectedPoints})
	return s.report, s.err
}

// stubMetrics captures DRMetrics calls.
type stubMetrics struct {
	switchCalls   []stubSwitchCall
	aliasBindings []stubAliasBinding
}

type stubSwitchCall struct {
	action          string
	durationSeconds float64
}

type stubAliasBinding struct {
	alias      string
	collection string
}

func (s *stubMetrics) RecordAliasSwitch(action string, durationSeconds float64) {
	s.switchCalls = append(s.switchCalls, stubSwitchCall{action: action, durationSeconds: durationSeconds})
}
func (s *stubMetrics) SetAliasCurrent(alias, collection string) {
	s.aliasBindings = append(s.aliasBindings, stubAliasBinding{alias: alias, collection: collection})
}

// stubExecutor captures RetentionConfig; returns the canned *RetentionResult
// (pointer-mirroring the canonical port signature).
//
// BLOC-2.B QDRANT-DR-FIX (June 2026, PR-check-5 follow-up): the canonical
// RetentionExecutor port expects a pointer return (*qdrantdr.RetentionResult,
// error), not the (RetentionResult, error) value-return the stub originally
// used. This drift surfaced as a `go vet ./...` failure at snapshot_test.go:432
// the first time the test was wired against the post-QDRANT-DR-WIRE-MIRROR
// port surface. Aligning the stub to match the canonical interface signature
// (structurally *qdrantdr.RetentionResult via the
// `type RetentionResult = qdrantdr.RetentionResult` alias declared in
// internal/platform/qdrant/dr/types.go) unblocks the vet failure without
// changing test semantics:
//   - test caller at line 425 supplies &stubExecutor{resp: &RetentionResult{...}},
//     so the success branch returns s.resp (a non-nil *RetentionResult);
//   - callers of executor.CleanupWithConfig already either dereference or
//     nil-check the result; returning nil on err is the conventional
//     Go error-pattern.
type stubExecutor struct {
	calls []RetentionConfig
	resp  *RetentionResult
	err   error
}

func (s *stubExecutor) Apply(ctx context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	return s.CleanupWithConfig(ctx, cfg)
}

func (s *stubExecutor) CleanupWithConfig(_ context.Context, cfg RetentionConfig) (*RetentionResult, error) {
	s.calls = append(s.calls, cfg)
	if s.err != nil {
		return nil, s.err
	}
	if s.resp == nil {
		return &RetentionResult{}, nil
	}
	return s.resp, nil
}

// ── SnapshotService tests ───────────────────────────────────────────

func TestSnapshotService_Take_RoundTrip(t *testing.T) {
	store := &stubSnapshotStore{
		createResp: &SnapshotDescription{
			Name:         "snap-1",
			Size:         1234,
			CreationTime: mustParseTime(t, "2026-06-01T12:00:00Z"),
		},
	}
	svc := NewSnapshotServiceFromDeps(SnapshotServiceDeps{Store: store, Log: zap.NewNop()})

	snap, err := svc.Take(context.Background(), "media_assets_v3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Name != "snap-1" || snap.Size != 1234 {
		t.Fatalf("got snap %+v; want Name=snap-1 Size=1234", snap)
	}
	if len(store.createCalls) != 1 || store.createCalls[0] != "media_assets_v3" {
		t.Fatalf("expected 1 CreateSnapshot call, got %+v", store.createCalls)
	}
}

func TestSnapshotService_Take_EmptyCollection(t *testing.T) {
	store := &stubSnapshotStore{}
	svc := NewSnapshotServiceFromDeps(SnapshotServiceDeps{Store: store, Log: zap.NewNop()})
	_, err := svc.Take(context.Background(), "")
	if err == nil {
		t.Fatalf("expected error for empty collection, got nil")
	}
	if len(store.createCalls) != 0 {
		t.Fatalf("must not call store on validation failure; got %d calls", len(store.createCalls))
	}
}

func TestSnapshotService_List_Delegates(t *testing.T) {
	store := &stubSnapshotStore{
		listResp: []SnapshotDescription{
			{Name: "snap-1"}, {Name: "snap-2"},
		},
	}
	svc := NewSnapshotServiceFromDeps(SnapshotServiceDeps{Store: store, Log: zap.NewNop()})
	snaps, err := svc.List(context.Background(), "media_assets_v3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
}

func TestSnapshotService_Delete_PassesName(t *testing.T) {
	store := &stubSnapshotStore{}
	svc := NewSnapshotServiceFromDeps(SnapshotServiceDeps{Store: store, Log: zap.NewNop()})
	if err := svc.Delete(context.Background(), "media_assets_v3", "snap-old"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.deleteCalls) != 1 || store.deleteCalls[0] != [2]string{"media_assets_v3", "snap-old"} {
		t.Fatalf("expected [[media_assets_v3 snap-old]], got %+v", store.deleteCalls)
	}
}

func TestSnapshotService_PanicOnNilStore(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on nil Store, got none")
		}
	}()
	_ = NewSnapshotServiceFromDeps(SnapshotServiceDeps{Log: zap.NewNop()})
}

// ── RestoreService tests ────────────────────────────────────────────

func TestRestoreService_PanicOnNilCore(t *testing.T) {
	cases := []struct {
		name    string
		mutator func(*RestoreServiceDeps)
	}{
		{"nil Store", func(d *RestoreServiceDeps) {
			d.Store = nil
			d.Switcher = &stubSwitcher{}
			d.Creator = &stubCreator{}
			d.Verifier = &stubVerifier{}
		}},
		{"nil Switcher", func(d *RestoreServiceDeps) {
			d.Switcher = nil
			d.Store = &stubSnapshotStore{}
			d.Creator = &stubCreator{}
			d.Verifier = &stubVerifier{}
		}},
		{"nil Creator", func(d *RestoreServiceDeps) {
			d.Creator = nil
			d.Store = &stubSnapshotStore{}
			d.Switcher = &stubSwitcher{}
			d.Verifier = &stubVerifier{}
		}},
		{"nil Verifier", func(d *RestoreServiceDeps) {
			d.Verifier = nil
			d.Store = &stubSnapshotStore{}
			d.Switcher = &stubSwitcher{}
			d.Creator = &stubCreator{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := RestoreServiceDeps{
				Store:    &stubSnapshotStore{},
				Switcher: &stubSwitcher{},
				Creator:  &stubCreator{},
				Verifier: &stubVerifier{},
				Metrics:  &stubMetrics{},
				Log:      zap.NewNop(),
			}
			tc.mutator(&deps)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic, got nil")
				}
			}()
			_ = NewRestoreServiceFromDeps(deps)
		})
	}
}

func TestRestoreService_VerifyFailKeepsCandidate(t *testing.T) {
	fixedNow := mustParseTime(t, "2026-06-01T12:00:00Z")
	store := &stubSnapshotStore{getURLResp: "http://qdrant/snap-blob"}
	switcher := &stubSwitcher{}
	creator := &stubCreator{}
	verifier := &stubVerifier{
		report: &VerifyReport{
			Ready:          false,
			ExpectedPoints: 10,
			ActualPoints:   8,
			MissingCount:   2,
			PayloadIssues:  1,
			Errors:         []string{"missing 2 points"},
		},
	}
	mtr := &stubMetrics{}
	svc := NewRestoreServiceFromDeps(RestoreServiceDeps{
		Store: store, Switcher: switcher, Creator: creator,
		Verifier: verifier, Metrics: mtr, Log: zap.NewNop(),
		Now: func() time.Time { return fixedNow },
	})

	report, err := svc.Restore(context.Background(), RestoreOptions{
		Collection: "media_assets_v3", SnapshotName: "snap-1",
		ExpectedPoints: 10, Alias: "media_assets_current",
	})
	if err != nil {
		t.Fatalf("verify-fail must NOT return an error (gate-blocked is exit 0 by design); got %v", err)
	}
	if report.Applied {
		t.Fatalf("verify-fail must set Applied=false; got true")
	}
	if report.Verify == nil || report.Verify.Ready {
		t.Fatalf("expected Verify.Ready=false in report")
	}
	if len(switcher.switchCalls) != 0 {
		t.Fatalf("verify-fail must NOT call Switcher; got %+v", switcher.switchCalls)
	}
	if len(creator.createCalls) != 1 {
		t.Fatalf("expected exactly 1 CreateCollection call (timestamped target), got %d", len(creator.createCalls))
	}
	if len(mtr.switchCalls) != 1 || mtr.switchCalls[0].action != "rehydrate" {
		t.Fatalf("expected 1 RecordAliasSwitch(action=rehydrate), got %+v", mtr.switchCalls)
	}
	if len(mtr.aliasBindings) != 1 || mtr.aliasBindings[0] != (stubAliasBinding{alias: "media_assets_current", collection: "media_assets_v3"}) {
		t.Fatalf("expected SetAliasCurrent(alias=media_assets_current, collection=media_assets_v3) — keeps OLD binding; got %+v", mtr.aliasBindings)
	}
}

func TestRestoreService_VerifyPassSwitchesAlias(t *testing.T) {
	fixedNow := mustParseTime(t, "2026-06-01T12:00:00Z")
	store := &stubSnapshotStore{getURLResp: "http://qdrant/snap-blob"}
	switcher := &stubSwitcher{}
	creator := &stubCreator{}
	verifier := &stubVerifier{
		report: &VerifyReport{Ready: true, ExpectedPoints: 10, ActualPoints: 10},
	}
	mtr := &stubMetrics{}
	svc := NewRestoreServiceFromDeps(RestoreServiceDeps{
		Store: store, Switcher: switcher, Creator: creator,
		Verifier: verifier, Metrics: mtr, Log: zap.NewNop(),
		Now: func() time.Time { return fixedNow },
	})

	report, err := svc.Restore(context.Background(), RestoreOptions{
		Collection: "media_assets_v3", SnapshotName: "snap-1",
		ExpectedPoints: 10, Alias: "media_assets_current",
	})
	if err != nil {
		t.Fatalf("verify-pass path failed: %v", err)
	}
	if !report.Applied {
		t.Fatalf("verify-pass must set Applied=true")
	}
	if len(switcher.switchCalls) != 1 {
		t.Fatalf("expected 1 SwitchAlias call, got %+v", switcher.switchCalls)
	}
	got := switcher.switchCalls[0]
	if got[0] != "media_assets_current" || got[1] != "media_assets_v3" {
		t.Fatalf("expected SwitchAlias(media_assets_current, media_assets_v3, <target>); got %+v", got)
	}
	if !strings.HasPrefix(got[2], "media_assets_v3__restore_") {
		t.Fatalf("expected target prefix media_assets_v3__restore_, got %q", got[2])
	}
	if len(mtr.switchCalls) != 1 || mtr.switchCalls[0].action != "rehydrate" {
		t.Fatalf("expected 1 RecordAliasSwitch(action=rehydrate), got %+v", mtr.switchCalls)
	}
	if len(mtr.aliasBindings) != 1 || mtr.aliasBindings[0].collection != got[2] {
		t.Fatalf("expected SetAliasCurrent(... collection=target); got %+v target=%s", mtr.aliasBindings, got[2])
	}
}

func TestRestoreService_GetURLFailureShortCircuits(t *testing.T) {
	store := &stubSnapshotStore{getURLErr: errors.New("snapshot not found")}
	svc := NewRestoreServiceFromDeps(RestoreServiceDeps{
		Store: store, Switcher: &stubSwitcher{}, Creator: &stubCreator{},
		Verifier: &stubVerifier{}, Metrics: &stubMetrics{}, Log: zap.NewNop(),
	})
	_, err := svc.Restore(context.Background(), RestoreOptions{
		Collection: "media_assets_v3", SnapshotName: "missing-snap",
		ExpectedPoints: 10, Alias: "media_assets_current",
	})
	if err == nil {
		t.Fatalf("expected error when GetSnapshotURL fails, got nil")
	}
	if !strings.Contains(err.Error(), "missing-snap") {
		t.Fatalf("error should mention the snapshot name for ops diagnosis; got %v", err)
	}
}

func TestRestoreService_RejectsZeroExpectedPoints(t *testing.T) {
	svc := NewRestoreServiceFromDeps(RestoreServiceDeps{
		Store: &stubSnapshotStore{}, Switcher: &stubSwitcher{}, Creator: &stubCreator{},
		Verifier: &stubVerifier{}, Metrics: &stubMetrics{}, Log: zap.NewNop(),
	})
	_, err := svc.Restore(context.Background(), RestoreOptions{
		Collection: "media_assets_v3", SnapshotName: "snap-1",
		Alias: "media_assets_current",
	})
	if err == nil || !strings.Contains(err.Error(), "ExpectedPoints must be > 0") {
		t.Fatalf("expected ExpectedPoints > 0 error; got %v", err)
	}
}

func TestBuildRestoreTarget_TimestampedAndSafe(t *testing.T) {
	now := mustParseTime(t, "2026-06-01T12:34:56.789012345Z")
	got := buildRestoreTarget("media_assets_v3.foo", func() time.Time { return now })
	if strings.ContainsAny(got, ":T-+.") {
		t.Fatalf("target must not contain Qdrant-illegal chars; got %q", got)
	}
	if !strings.HasPrefix(got, "media_assets_v3_foo__restore_") {
		t.Fatalf("target must start with sanitized source + separator; got %q", got)
	}
	got2 := buildRestoreTarget("media_assets_v3.foo", func() time.Time {
		return now.Add(time.Nanosecond)
	})
	if got == got2 {
		t.Fatalf("nanosecond-resolution targets must be distinct; got %q twice", got)
	}
}

// ── RetentionService tests ──────────────────────────────────────────

func TestRetentionService_Apply_DefaultsEnforced(t *testing.T) {
	executor := &stubExecutor{
		resp: &RetentionResult{
			CollectionsDropped: 3,
			CollectionsKept:    2,
			DroppedNames:       []string{"old-1", "old-2", "old-3"},
		},
	}
	svc := NewRetentionServiceFromDeps(RetentionServiceDeps{Executor: executor, Log: zap.NewNop()})
	res, err := svc.Apply(context.Background(), RetentionConfig{
		RetentionDays:           1,
		KeepLastN:               0, // should be lifted to 2 by the floor
		ProtectedRollbackTarget: "media_assets_v3_old",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("expected 1 executor call, got %d", len(executor.calls))
	}
	got := executor.calls[0]
	if got.KeepLastN < 2 {
		t.Fatalf("expected the floor to lift KeepLastN to >=2, got %d", got.KeepLastN)
	}
	if res.CollectionsDropped != 3 {
		t.Fatalf("expected 3 dropped, got %d", res.CollectionsDropped)
	}
}

func TestRetentionService_Apply_RejectsZeroRetentionDays(t *testing.T) {
	executor := &stubExecutor{}
	svc := NewRetentionServiceFromDeps(RetentionServiceDeps{Executor: executor, Log: zap.NewNop()})
	_, err := svc.Apply(context.Background(), RetentionConfig{RetentionDays: 0})
	if err == nil || !strings.Contains(err.Error(), "RetentionDays must be > 0") {
		t.Fatalf("expected RetentionDays > 0 error; got %v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor must not be called on validation failure; got %d", len(executor.calls))
	}
}

func TestRetentionService_PanicOnNilExecutor(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Executor, got none")
		}
	}()
	_ = NewRetentionServiceFromDeps(RetentionServiceDeps{Log: zap.NewNop()})
}

// ── Misc helpers ────────────────────────────────────────────────────

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return v
}

// (intentionally no drift-anchor at end: a discarded reflect.TypeOf is a
// no-op, so its drift-catching comment was misleading. The real drift
// gate lives in dr_adapter.go's VerifierAdapter manual field-copy — a
// missed field surfaces immediately as a Go compile error there, not
// as a silent zero at runtime.)
