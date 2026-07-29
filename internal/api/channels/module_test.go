package channels

import (
	"context"
	"testing"

	appchannels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// stubRepo is the minimal Repository contract used by Build's
// wiring path. Real SQLite-backed testing lives in
// internal/infrastructure/database/sqlite/assets/channels_repository_test.go.
//
// stubRepo is stateful so GetByID returns the row Upsert captured,
// matching what a real round-trip through SQLite would do. Mutations
// happen via Upsert; reads return the most-recently-stored row.
type stubRepo struct {
	upsertCalls []*asset.CategoryChannel
	stored      map[string]*asset.CategoryChannel
}

func (s *stubRepo) ListAll(_ context.Context) ([]*asset.CategoryChannel, error) {
	if len(s.stored) == 0 {
		return nil, nil
	}
	out := make([]*asset.CategoryChannel, 0, len(s.stored))
	for _, ch := range s.stored {
		out = append(out, ch)
	}
	return out, nil
}
func (s *stubRepo) ListCategories(_ context.Context) ([]string, error) {
	return nil, nil
}
func (s *stubRepo) GetByID(_ context.Context, id string) (*asset.CategoryChannel, error) {
	if s.stored == nil {
		return nil, nil
	}
	ch, ok := s.stored[id]
	if !ok {
		return nil, nil
	}
	return ch, nil
}
func (s *stubRepo) Upsert(_ context.Context, ch *asset.CategoryChannel) error {
	s.upsertCalls = append(s.upsertCalls, ch)
	if s.stored == nil {
		s.stored = make(map[string]*asset.CategoryChannel)
	}
	s.stored[ch.ID] = ch
	return nil
}
func (s *stubRepo) ListEnabled(_ context.Context) ([]*asset.CategoryChannel, error) {
	return s.ListAll(nil)
}
func (s *stubRepo) Delete(_ context.Context, id string) error {
	delete(s.stored, id)
	return nil
}
func (s *stubRepo) MarkChecked(_ context.Context, _ appchannels.MarkCheckedCommand) error {
	return nil
}
func (s *stubRepo) ClaimDue(_ context.Context, _ appchannels.ClaimDueCommand) ([]*asset.CategoryChannel, error) {
	return nil, nil
}
func (s *stubRepo) UpdateCursor(_ context.Context, _ appchannels.UpdateCursorCommand) error {
	return nil
}

func TestBuild_ReturnsErrorOnNilRepository(t *testing.T) {
	_, err := Build(Dependencies{Logger: zap.NewNop()})
	if err == nil {
		t.Fatal("expected error when Repository is nil, got nil")
	}
}

func TestBuild_ReturnsValidDescriptor(t *testing.T) {
	repo := &stubRepo{}
	d, err := Build(Dependencies{Repository: repo, Logger: zap.NewNop()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d == nil {
		t.Fatal("Build returned nil descriptor")
	}
	if d.Name() != "channels" {
		t.Fatalf("Name = %q, want channels", d.Name())
	}
	if !d.Enabled() {
		t.Fatalf("Enabled = false, want true")
	}
	cd, ok := d.(*ChannelsDescriptor)
	if !ok {
		t.Fatalf("Descriptor type = %T, want *ChannelsDescriptor", d)
	}
	if cd.Service == nil {
		t.Fatal("ChannelsDescriptor.Service is nil, want non-nil")
	}
	if cd.Module == nil {
		t.Fatal("ChannelsDescriptor.Module is nil, want non-nil")
	}
}

func TestBuild_SubstitutesNopLoggerWhenNil(t *testing.T) {
	d, err := Build(Dependencies{Repository: &stubRepo{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if d.Name() != "channels" {
		t.Fatalf("Name = %q, want channels", d.Name())
	}
}

func TestBuild_HandlerRoutesAreRegistered(t *testing.T) {
	repo := &stubRepo{}
	d, err := Build(Dependencies{Repository: repo, Logger: zap.NewNop()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	rg := engine.Group("/api")
	d.RegisterRoutes(rg)

	wantRoutes := map[string]bool{
		"GET /api/channels":              false,
		"GET /api/channels/categories":   false,
		"GET /api/channels/:id":          false,
		"POST /api/channels":             false,
		"POST /api/channels/bulk-upsert": false,
		"DELETE /api/channels/:id":       false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := wantRoutes[key]; ok {
			wantRoutes[key] = true
		}
	}
	for path, found := range wantRoutes {
		if !found {
			t.Errorf("missing route: %s", path)
		}
	}
}

func TestService_UpsertAppliesDefaultsAndDerivesID(t *testing.T) {
	repo := &stubRepo{}
	svc := appchannels.NewService(repo, zap.NewNop())

	out, err := svc.Upsert(context.Background(), appchannels.UpsertChannelCommand{
		Category:   "tech",
		ChannelURL: "https://youtube.com/@example",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	wantID := svc.IDFor("tech", "https://youtube.com/@example")
	if out.ID != wantID {
		t.Errorf("returned ID = %q, want derived ID %q", out.ID, wantID)
	}
	if len(repo.upsertCalls) != 1 {
		t.Fatalf("upsertCalls len = %d, want 1", len(repo.upsertCalls))
	}
	got := repo.upsertCalls[0]
	if got.MaxClipDuration != appchannels.Default.MaxClipDuration {
		t.Errorf("MaxClipDuration = %d, want %d", got.MaxClipDuration, appchannels.Default.MaxClipDuration)
	}
	if got.Priority != appchannels.Default.Priority {
		t.Errorf("Priority = %d, want %d", got.Priority, appchannels.Default.Priority)
	}
	if got.CheckInterval != appchannels.Default.CheckInterval {
		t.Errorf("CheckInterval = %q, want %q", got.CheckInterval, appchannels.Default.CheckInterval)
	}
	if got.Keywords != "[]" {
		t.Errorf("Keywords = %q, want %q", got.Keywords, "[]")
	}
}

func TestService_DeleteRequiresID(t *testing.T) {
	repo := &stubRepo{}
	svc := appchannels.NewService(repo, zap.NewNop())
	if _, err := svc.Delete(context.Background(), ""); err == nil {
		t.Fatal("expected error when id is empty")
	}
}

func TestService_UpsertRejectsEmptyFields(t *testing.T) {
	repo := &stubRepo{}
	svc := appchannels.NewService(repo, zap.NewNop())
	if _, err := svc.Upsert(context.Background(), appchannels.UpsertChannelCommand{}); err == nil {
		t.Fatal("expected error when category and channel_url are empty")
	}
}

func TestService_UpsertBulkPartitionsCreatedAndUpdated(t *testing.T) {
	repo := &stubRepo{}
	svc := appchannels.NewService(repo, zap.NewNop())
	res, err := svc.UpsertBulk(context.Background(), appchannels.BulkUpsertChannelsCommand{
		Channels: []appchannels.UpsertChannelCommand{
			{Category: "tech", ChannelURL: "https://youtube.com/@new"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertBulk: %v", err)
	}
	if len(res.Created) != 1 {
		t.Errorf("Created len = %d, want 1", len(res.Created))
	}
	if len(res.Updated) != 0 {
		t.Errorf("Updated len = %d, want 0", len(res.Updated))
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors len = %d, want 0", len(res.Errors))
	}
}
